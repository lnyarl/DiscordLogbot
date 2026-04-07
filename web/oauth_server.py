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
from fastapi import APIRouter, Form, HTTPException
from fastapi.responses import JSONResponse, RedirectResponse
from jose import JWTError, jwt

from web.auth import (
    DISCORD_API,
    DISCORD_CLIENT_ID,
    DISCORD_CLIENT_SECRET,
    JWT_ALGORITHM,
    JWT_SECRET,
    get_accessible_channels,
)

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
    user_id: str, username: str, channels: list, code_challenge: str, client_id: str
) -> str:
    """10분짜리 authorization code (JWT). jti로 일회성 보장."""
    exp = datetime.now(timezone.utc) + timedelta(minutes=10)
    return jwt.encode(
        {
            "type": "mcp_auth_code",
            "jti": secrets.token_urlsafe(16),
            "sub": user_id,
            "username": username,
            "channels": channels,
            "cc": code_challenge,
            "cid": client_id,   # token 엔드포인트에서 재검증
            "exp": exp,
        },
        JWT_SECRET,
        algorithm=JWT_ALGORITHM,
    )


def make_access_token(user_id: str, username: str, channels: list) -> str:
    """24시간짜리 MCP access token (JWT)."""
    exp = datetime.now(timezone.utc) + timedelta(hours=24)
    return jwt.encode(
        {
            "type": "mcp_access",
            "sub": user_id,
            "username": username,
            "channels": channels,
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
        "grant_types_supported": ["authorization_code"],
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
async def discord_callback(code: str, state: str):
    """Discord 인증 완료 → 채널 권한 계산 → AI 클라이언트로 auth code 전달."""
    state_data = _decode_state(state)
    if not state_data:
        raise HTTPException(400, "Invalid or expired state")

    async with httpx.AsyncClient() as client:
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

        r = await client.get(
            f"{DISCORD_API}/users/@me",
            headers={"Authorization": f"Bearer {discord_token}"},
        )
        r.raise_for_status()
        user = r.json()

    user_id = user.get("id")
    username = user.get("username")
    if not user_id or not username:
        raise HTTPException(502, "Discord did not return valid user info")

    try:
        channels = await get_accessible_channels(discord_token, user_id)
    except Exception:
        logging.exception("채널 권한 수집 실패 (user_id=%s)", user_id)
        channels = []

    auth_code = _make_auth_code(
        user_id, username, channels, state_data["cc"], state_data.get("cid", "")
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
    code: str = Form(...),
    code_verifier: str = Form(...),
    redirect_uri: str = Form(default=""),
    client_id: str = Form(default=""),
):
    """auth code + PKCE 검증 → access token 발급."""
    if grant_type != "authorization_code":
        raise HTTPException(400, "Only authorization_code grant type supported")

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
    channels = payload.get("channels")
    if not sub or username is None or channels is None:
        raise HTTPException(400, "Invalid auth code: missing claims")

    access_token = make_access_token(sub, username, channels)
    return JSONResponse({
        "access_token": access_token,
        "token_type": "Bearer",
        "expires_in": 86400,
    })
