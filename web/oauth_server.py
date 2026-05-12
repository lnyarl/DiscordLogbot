"""MCP용 OAuth 2.0 인증 서버.

AI 클라이언트(Claude Desktop 등)가 표준 OAuth 2.0 + PKCE 플로우로
Discord 계정 기반 인증을 처리할 수 있게 한다.

플로우:
  1. AI 클라이언트 → GET /oauth/authorize (PKCE code_challenge 포함)
  2. 서버 → Discord OAuth로 리다이렉트 (state에 클라이언트 컨텍스트 인코딩)
  3. Discord → GET /oauth/discord_callback
  4. 서버 → 유저 채널 권한 계산 → auth code 발급 → AI 클라이언트 redirect_uri로 리다이렉트
  5. AI 클라이언트 → POST /oauth/token (code_verifier로 PKCE 검증)
  6. 서버 → access token 발급 (24시간)
"""
import base64
import hashlib
import logging
import os
import re
import secrets
import time
from datetime import datetime, timedelta, timezone
from urllib.parse import urlencode

import httpx
from fastapi import APIRouter, Form, HTTPException, Request
from fastapi.responses import JSONResponse, RedirectResponse
from jose import JWTError, jwt

from web.auth import (
    DISCORD_API,
    DISCORD_CLIENT_ID,
    DISCORD_CLIENT_SECRET,
    JWT_ALGORITHM,
    JWT_SECRET,
)
from web.permissions import compute_accessible_channels, write_cache

router = APIRouter()

BASE_URL = os.getenv("BASE_URL", "http://localhost:8080")
_DISCORD_CB = f"{BASE_URL}/oauth/discord_callback"

# ── client_id 허용 목록 ─────────────────────────────────────────────────────
# MCP_CLIENT_IDS: 쉼표 구분 허용 client_id 목록
# 미설정 시 빈 set → 모든 client_id 거부 (운영 환경에서는 반드시 설정 필요)
_ALLOWED_CLIENT_IDS: set[str] = {
    c.strip()
    for c in os.getenv("MCP_CLIENT_IDS", "").split(",")
    if c.strip()
}

if not _ALLOWED_CLIENT_IDS:
    logging.warning(
        "MCP_CLIENT_IDS 환경변수가 설정되지 않았습니다. "
        "모든 OAuth 요청이 거부됩니다. .env에 MCP_CLIENT_IDS를 설정하세요."
    )


def _is_client_id_allowed(client_id: str) -> bool:
    """등록된 client_id인지 확인. 허용 목록이 비어 있으면 전체 거부."""
    return bool(_ALLOWED_CLIENT_IDS) and client_id in _ALLOWED_CLIENT_IDS


# ── redirect_uri 허용 목록 ───────────────────────────────────────────────────
# MCP_ALLOWED_REDIRECT_URIS: 쉼표 구분 정확 일치 URI (외부 클라이언트용)
# localhost/127.0.0.1 는 포트 무관 자동 허용 (Claude Desktop 등 데스크톱 클라이언트 대응)
_STATIC_ALLOWED: set[str] = {
    u.strip()
    for u in os.getenv("MCP_ALLOWED_REDIRECT_URIS", "").split(",")
    if u.strip()
}
_LOCALHOST_RE = re.compile(r"^https?://(localhost|127\.0\.0\.1)(?::(\d{1,5}))?(/.*)?$")


def _is_redirect_uri_allowed(uri: str) -> bool:
    if uri in _STATIC_ALLOWED:
        return True
    m = _LOCALHOST_RE.match(uri)
    if m:
        port_str = m.group(2)
        if port_str is not None and not (1 <= int(port_str) <= 65535):
            return False
        return True
    return False


# ── JWT 헬퍼 ────────────────────────────────────────────────────────────────

def _encode_state(data: dict, minutes: int = 10) -> str:
    """Discord OAuth state로 사용할 단기 서명 토큰."""
    exp = datetime.now(timezone.utc) + timedelta(minutes=minutes)
    return jwt.encode({**data, "exp": exp}, JWT_SECRET, algorithm=JWT_ALGORITHM)


def _decode_state(token: str) -> dict | None:
    try:
        return jwt.decode(token, JWT_SECRET, algorithms=[JWT_ALGORITHM])
    except JWTError:
        return None


def _make_auth_code(
    user_id: str, username: str, code_challenge: str, client_id: str
) -> str:
    """10분짜리 authorization code (JWT). jti로 일회성 보장.

    채널 권한은 더 이상 토큰에 박지 않는다 — 캐시 테이블에서 user_id로 조회.
    """
    exp = datetime.now(timezone.utc) + timedelta(minutes=10)
    return jwt.encode(
        {
            "type": "mcp_auth_code",
            "jti": secrets.token_urlsafe(16),
            "sub": user_id,
            "username": username,
            "cc": code_challenge,
            "cid": client_id,   # token 엔드포인트에서 재검증
            "exp": exp,
        },
        JWT_SECRET,
        algorithm=JWT_ALGORITHM,
    )


def make_access_token(user_id: str, username: str) -> str:
    """24시간짜리 MCP access token (JWT). 채널 권한은 토큰에 포함하지 않고
    매 요청 시 channel_access_cache에서 user_id로 조회한다."""
    exp = datetime.now(timezone.utc) + timedelta(hours=24)
    return jwt.encode(
        {
            "type": "mcp_access",
            "sub": user_id,
            "username": username,
            "exp": exp,
        },
        JWT_SECRET,
        algorithm=JWT_ALGORITHM,
    )


def make_refresh_token(user_id: str, username: str, client_id: str) -> str:
    """30일짜리 refresh token (JWT). access_token 만료 시 OAuth 콜백 listener를
    띄우지 않고 조용히 새 access_token을 받기 위한 용도.

    JTI 일회성: 새 refresh 발급마다 JTI 갱신 (rotation). 이전 refresh는 재사용 불가.
    """
    exp = datetime.now(timezone.utc) + timedelta(days=30)
    return jwt.encode(
        {
            "type": "mcp_refresh",
            "jti": secrets.token_urlsafe(16),
            "sub": user_id,
            "username": username,
            "cid": client_id,
            "exp": exp,
        },
        JWT_SECRET,
        algorithm=JWT_ALGORITHM,
    )


def _verify_pkce(verifier: str, challenge: str) -> bool:
    """S256 PKCE 검증."""
    digest = hashlib.sha256(verifier.encode()).digest()
    computed = base64.urlsafe_b64encode(digest).rstrip(b"=").decode()
    return computed == challenge


# ── auth code 일회성 저장소 ─────────────────────────────────────────────────
# jti → 만료 타임스탬프(float). 단일 프로세스용 in-memory 구현.
# 멀티 워커 환경에서는 Redis 등 외부 스토어로 교체할 것.
_USED_JTIS: dict[str, float] = {}
_AUTH_CODE_TTL = 10 * 60  # 10분 (초)


def _purge_expired_jtis() -> None:
    """만료된 JTI 항목 정리 (토큰 엔드포인트 호출 시마다 실행)."""
    now = time.monotonic()
    expired = [jti for jti, exp in _USED_JTIS.items() if now > exp]
    for jti in expired:
        del _USED_JTIS[jti]


def _consume_jti(jti: str) -> bool:
    """JTI를 소비한다. 이미 소비된 경우 False 반환."""
    if jti in _USED_JTIS:
        return False
    _USED_JTIS[jti] = time.monotonic() + _AUTH_CODE_TTL
    _purge_expired_jtis()
    return True


# ── OAuth 2.0 엔드포인트 ────────────────────────────────────────────────────

@router.get("/.well-known/oauth-authorization-server")
async def oauth_metadata():
    """RFC 8414 OAuth 2.0 서버 메타데이터."""
    return JSONResponse({
        "issuer": BASE_URL,
        "authorization_endpoint": f"{BASE_URL}/oauth/authorize",
        "token_endpoint": f"{BASE_URL}/oauth/token",
        "response_types_supported": ["code"],
        "grant_types_supported": ["authorization_code", "refresh_token"],
        "code_challenge_methods_supported": ["S256"],
        "token_endpoint_auth_methods_supported": ["none"],
    })


@router.get("/oauth/authorize")
async def authorize(
    client_id: str,
    redirect_uri: str,
    response_type: str,
    code_challenge: str,
    code_challenge_method: str = "S256",
    state: str = "",
    scope: str = "",
):
    """AI 클라이언트의 인증 요청 → Discord OAuth로 리다이렉트."""
    # RFC 6749: client_id → redirect_uri 순으로 먼저 검증, 이후 나머지 파라미터
    if not _is_client_id_allowed(client_id):
        raise HTTPException(400, "Unknown client_id")
    if not _is_redirect_uri_allowed(redirect_uri):
        raise HTTPException(400, "redirect_uri not allowed")
    if response_type != "code":
        raise HTTPException(400, "Only response_type=code supported")
    if code_challenge_method != "S256":
        raise HTTPException(400, "Only S256 code_challenge_method supported")

    # 클라이언트 컨텍스트를 Discord state에 인코딩 (client_id 포함 → auth code에 바인딩)
    state_token = _encode_state({
        "redirect_uri": redirect_uri,
        "client_state": state,
        "cc": code_challenge,
        "cid": client_id,
    })

    params = urlencode({
        "client_id": DISCORD_CLIENT_ID,
        "redirect_uri": _DISCORD_CB,
        "response_type": "code",
        "scope": "identify guilds guilds.members.read",
        "state": state_token,
    })
    return RedirectResponse(f"https://discord.com/oauth2/authorize?{params}")


@router.get("/oauth/discord_callback")
async def discord_callback(code: str, state: str, request: Request):
    """Discord 인증 완료 → 채널 권한 캐시 갱신 → AI 클라이언트로 auth code 전달.

    Fresh 로그인은 캐시의 권위 있는 갱신 트리거다 — 봇 이벤트가 누락된 변경도
    여기서 자가 치료된다. 토큰 자체에는 user_id만 들어가고 채널 목록은 캐시에서 조회.
    """
    t0 = time.monotonic()
    state_data = _decode_state(state)
    if not state_data:
        raise HTTPException(400, "Invalid or expired state")

    async with httpx.AsyncClient(timeout=httpx.Timeout(10.0)) as client:
        r = await client.post(
            "https://discord.com/api/oauth2/token",
            data={
                "client_id": DISCORD_CLIENT_ID,
                "client_secret": DISCORD_CLIENT_SECRET,
                "grant_type": "authorization_code",
                "code": code,
                "redirect_uri": _DISCORD_CB,
            },
        )
        r.raise_for_status()
        token_data = r.json()
        discord_token = token_data.get("access_token")
        if not discord_token:
            raise HTTPException(502, "Discord did not return an access token")
        t_token = time.monotonic()

        r = await client.get(
            f"{DISCORD_API}/users/@me",
            headers={"Authorization": f"Bearer {discord_token}"},
        )
        r.raise_for_status()
        user = r.json()
        t_user = time.monotonic()

    user_id = user.get("id")
    username = user.get("username")
    if not user_id or not username:
        raise HTTPException(502, "Discord did not return valid user info")

    # 권한 계산 — 봇 토큰만으로 가능 (compute_accessible_channels가 user 토큰 의존성 없음).
    # 실패해도 빈 리스트로 진행하고 캐시는 덮어쓰지 않음 → 다음 lazy fill에서 재시도.
    try:
        channels = await compute_accessible_channels(user_id)
        await write_cache(request.app.state.pool, user_id, channels)
    except Exception:
        logging.exception("채널 권한 수집 실패 (user_id=%s)", user_id)
        channels = []
    t_channels = time.monotonic()

    logging.info(
        "discord_callback timing user_id=%s token=%.2fs identify=%.2fs channels=%.2fs total=%.2fs (channels_count=%d)",
        user_id,
        t_token - t0,
        t_user - t_token,
        t_channels - t_user,
        t_channels - t0,
        len(channels),
    )

    auth_code = _make_auth_code(
        user_id, username, state_data["cc"], state_data.get("cid", "")
    )

    target_uri = state_data["redirect_uri"]
    # defense in depth: state JWT가 변조될 수 없더라도 콜백 시점에도 재검증
    if not _is_redirect_uri_allowed(target_uri):
        raise HTTPException(400, "redirect_uri not allowed")

    params: dict[str, str] = {"code": auth_code}
    if state_data.get("client_state"):
        params["state"] = state_data["client_state"]

    return RedirectResponse(f"{target_uri}?{urlencode(params)}")


@router.post("/oauth/token")
async def token(
    grant_type: str = Form(...),
    code: str = Form(default=""),
    code_verifier: str = Form(default=""),
    refresh_token: str = Form(default=""),
    redirect_uri: str = Form(default=""),
    client_id: str = Form(default=""),
):
    """OAuth 2.0 token endpoint — authorization_code 및 refresh_token grant 지원.

    refresh_token grant는 OAuth 콜백 listener(localhost 임의 포트) 없이 조용히
    access token을 갱신할 수 있어, mcp-remote가 24h마다 브라우저로 재인증할 필요가
    사라진다 → localhost 포트 충돌 케이스 해소.

    Refresh rotation: 매 갱신마다 새 refresh token도 함께 발급, 이전 jti는 일회성 소진.
    """
    if grant_type == "authorization_code":
        return await _token_from_authorization_code(
            code=code, code_verifier=code_verifier, client_id=client_id,
        )
    if grant_type == "refresh_token":
        return await _token_from_refresh_token(
            refresh_token=refresh_token, client_id=client_id,
        )
    raise HTTPException(
        400, "Only authorization_code and refresh_token grant types supported"
    )


async def _token_from_authorization_code(
    code: str, code_verifier: str, client_id: str
) -> JSONResponse:
    if not code or not code_verifier:
        raise HTTPException(400, "code and code_verifier required")

    try:
        payload = jwt.decode(code, JWT_SECRET, algorithms=[JWT_ALGORITHM])
    except JWTError:
        raise HTTPException(400, "Invalid or expired auth code")

    if payload.get("type") != "mcp_auth_code":
        raise HTTPException(400, "Invalid token type")

    # auth code에 바인딩된 client_id 재검증 — authorize 단계 우회 방지
    # Public client(PKCE)도 token 요청 시 client_id 전송이 RFC 6749 §4.1.3 기준 필수
    if not client_id:
        raise HTTPException(400, "client_id is required")
    code_client_id = payload.get("cid", "")
    if not _is_client_id_allowed(code_client_id):
        raise HTTPException(400, "Unknown client_id in auth code")
    if client_id != code_client_id:
        raise HTTPException(400, "client_id mismatch")

    jti = payload.get("jti")
    if not jti:
        raise HTTPException(400, "Invalid auth code: missing jti")
    if not _consume_jti(jti):
        raise HTTPException(400, "Auth code already used")

    if not _verify_pkce(code_verifier, payload.get("cc", "")):
        raise HTTPException(400, "PKCE verification failed")

    sub = payload.get("sub")
    username = payload.get("username")
    if not sub or username is None:
        raise HTTPException(400, "Invalid auth code: missing claims")

    return JSONResponse({
        "access_token": make_access_token(sub, username),
        "refresh_token": make_refresh_token(sub, username, client_id),
        "token_type": "Bearer",
        "expires_in": 86400,
    })


async def _token_from_refresh_token(
    refresh_token: str, client_id: str
) -> JSONResponse:
    if not refresh_token:
        raise HTTPException(400, "refresh_token required")
    if not client_id:
        raise HTTPException(400, "client_id is required")

    try:
        payload = jwt.decode(refresh_token, JWT_SECRET, algorithms=[JWT_ALGORITHM])
    except JWTError:
        raise HTTPException(400, "Invalid or expired refresh token")

    if payload.get("type") != "mcp_refresh":
        raise HTTPException(400, "Invalid token type")

    rt_client_id = payload.get("cid", "")
    if not _is_client_id_allowed(rt_client_id):
        raise HTTPException(400, "Unknown client_id in refresh token")
    if client_id != rt_client_id:
        raise HTTPException(400, "client_id mismatch")

    jti = payload.get("jti")
    if not jti:
        raise HTTPException(400, "Invalid refresh token: missing jti")
    # 일회성 소진 — 새 refresh로 rotation, 이전 토큰 재사용 차단
    if not _consume_jti(jti):
        raise HTTPException(400, "Refresh token already used")

    sub = payload.get("sub")
    username = payload.get("username")
    if not sub or username is None:
        raise HTTPException(400, "Invalid refresh token: missing claims")

    return JSONResponse({
        "access_token": make_access_token(sub, username),
        "refresh_token": make_refresh_token(sub, username, client_id),
        "token_type": "Bearer",
        "expires_in": 86400,
    })
