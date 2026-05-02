"""Discord OAuth2 인증 핸들러."""
import logging
import os
import secrets
import sys
from datetime import datetime, timedelta, timezone

import httpx
from fastapi import APIRouter, Cookie, Request
from fastapi.responses import HTMLResponse, RedirectResponse
from fastapi.templating import Jinja2Templates
from jose import JWTError, jwt

log = logging.getLogger(__name__)

TEMPLATES = Jinja2Templates(directory=os.path.join(os.path.dirname(__file__), "templates"))

DISCORD_CLIENT_ID = os.getenv("DISCORD_CLIENT_ID", "")
DISCORD_CLIENT_SECRET = os.getenv("DISCORD_CLIENT_SECRET", "")
DISCORD_REDIRECT_URI = os.getenv("DISCORD_REDIRECT_URI", "http://localhost:8080/auth/callback")
DISCORD_BOT_TOKEN = os.getenv("DISCORD_TOKEN", "")
JWT_SECRET = os.getenv("JWT_SECRET")
if not JWT_SECRET:
    import sys
    sys.exit("오류: JWT_SECRET 환경변수가 설정되지 않았습니다. openssl rand -hex 32 로 생성하세요.")
JWT_ALGORITHM = "HS256"
JWT_EXPIRE_HOURS = 24

DISCORD_API = "https://discord.com/api/v10"
OAUTH_SCOPES = "identify guilds guilds.members.read"

router = APIRouter()


def _make_jwt(user_id: str, username: str) -> str:
    expire = datetime.now(timezone.utc) + timedelta(hours=JWT_EXPIRE_HOURS)
    return jwt.encode(
        {"sub": user_id, "username": username, "exp": expire},
        JWT_SECRET,
        algorithm=JWT_ALGORITHM,
    )


def decode_jwt(token: str) -> dict | None:
    try:
        return jwt.decode(token, JWT_SECRET, algorithms=[JWT_ALGORITHM])
    except JWTError:
        return None


@router.get("/", response_class=HTMLResponse)
async def index(request: Request, session: str | None = Cookie(default=None)):
    payload = decode_jwt(session) if session else None
    if not payload:
        return TEMPLATES.TemplateResponse(request=request, name="login.html")
    return RedirectResponse("/search")


@router.get("/auth/login")
async def login():
    state = secrets.token_urlsafe(16)
    url = (
        f"https://discord.com/oauth2/authorize"
        f"?client_id={DISCORD_CLIENT_ID}"
        f"&redirect_uri={DISCORD_REDIRECT_URI}"
        f"&response_type=code"
        f"&scope={OAUTH_SCOPES.replace(' ', '%20')}"
        f"&state={state}"
    )
    response = RedirectResponse(url)
    # samesite=none: Discord OAuth2 redirect는 cross-site이므로 lax면 차단될 수 있음
    response.set_cookie("oauth_state", state, httponly=True, samesite="none", secure=True, max_age=300)
    return response


@router.get("/auth/callback")
async def callback(
    code: str,
    state: str,
    request: Request,
    oauth_state: str | None = Cookie(default=None),
):
    if state != oauth_state:
        log.warning("OAuth state mismatch: state=%s, cookie=%s", state, oauth_state)
        return HTMLResponse("Invalid state", status_code=400)

    try:
        async with httpx.AsyncClient() as client:
            # 토큰 교환
            r = await client.post(
                "https://discord.com/api/oauth2/token",
                data={
                    "client_id": DISCORD_CLIENT_ID,
                    "client_secret": DISCORD_CLIENT_SECRET,
                    "grant_type": "authorization_code",
                    "code": code,
                    "redirect_uri": DISCORD_REDIRECT_URI,
                },
            )
            if r.status_code != 200:
                log.warning("Token exchange failed: %s %s", r.status_code, r.text)
                return RedirectResponse("/auth/login")
            token_data = r.json()
            access_token = token_data["access_token"]

            # 유저 정보 조회
            r = await client.get(
                f"{DISCORD_API}/users/@me",
                headers={"Authorization": f"Bearer {access_token}"},
            )
            if r.status_code != 200:
                log.error("Discord /users/@me failed: %s %s", r.status_code, r.text)
                return HTMLResponse("Discord 사용자 정보를 가져올 수 없습니다. 다시 시도해주세요.", status_code=502)
            user = r.json()
    except httpx.HTTPError as exc:
        log.exception("OAuth callback HTTP error")
        return HTMLResponse("Discord API 연결 실패. 잠시 후 다시 시도해주세요.", status_code=502)

    user_id = user["id"]
    username = user["username"]

    # 접근 가능한 길드 ID만 JWT에 포함 (채널 목록은 DB에서 실시간 조회)
    try:
        async with httpx.AsyncClient() as client:
            r = await client.get(
                f"{DISCORD_API}/users/@me/guilds",
                headers={"Authorization": f"Bearer {access_token}"},
            )
            r.raise_for_status()
            guild_ids = [g["id"] for g in r.json()]
    except Exception:
        import logging
        logging.exception("길드 목록 수집 실패 (user_id=%s)", user_id)
        guild_ids = []

    expire = datetime.now(timezone.utc) + timedelta(hours=JWT_EXPIRE_HOURS)
    token = jwt.encode(
        {
            "sub": user_id,
            "username": username,
            "exp": expire,
            "guild_ids": guild_ids,
        },
        JWT_SECRET,
        algorithm=JWT_ALGORITHM,
    )

    response = RedirectResponse("/search")
    response.set_cookie(
        "session", token,
        httponly=True,
        secure=True,
        samesite="lax",
        max_age=JWT_EXPIRE_HOURS * 3600,
    )
    response.delete_cookie("oauth_state")
    return response


@router.post("/auth/logout")
async def logout(request: Request):
    origin = request.headers.get("origin") or request.headers.get("referer", "")
    allowed = DISCORD_REDIRECT_URI.rsplit("/", 2)[0]  # https://historian.stashy.in
    if allowed and not origin.startswith(allowed):
        return HTMLResponse("Forbidden", status_code=403)
    response = RedirectResponse("/", status_code=303)
    response.delete_cookie("session")
    return response
