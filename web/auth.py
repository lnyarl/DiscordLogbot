"""Discord OAuth2 인증 핸들러."""
import os
import secrets
import sys
from datetime import datetime, timedelta, timezone

import httpx
from fastapi import APIRouter, Cookie, Request
from fastapi.responses import HTMLResponse, RedirectResponse
from fastapi.templating import Jinja2Templates
from jose import JWTError, jwt

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


async def get_accessible_channels(user_access_token: str, user_id: str) -> list[dict]:
    """유저가 접근 가능한 채널 목록 반환 (봇이 로깅 중인 채널 한정)."""
    headers_user = {"Authorization": f"Bearer {user_access_token}"}
    headers_bot = {"Authorization": f"Bot {DISCORD_BOT_TOKEN}"}

    async with httpx.AsyncClient() as client:
        # 유저가 속한 길드 목록
        r = await client.get(f"{DISCORD_API}/users/@me/guilds", headers=headers_user)
        r.raise_for_status()
        user_guilds = {g["id"] for g in r.json()}

        # 봇이 있는 길드 목록
        r = await client.get(f"{DISCORD_API}/users/@me/guilds", headers=headers_bot)
        r.raise_for_status()
        bot_guilds = {g["id"]: g for g in r.json()}

        shared_guild_ids = user_guilds & bot_guilds.keys()

        accessible = []
        for guild_id in shared_guild_ids:
            # 해당 길드에서 유저의 멤버 정보 조회
            r = await client.get(
                f"{DISCORD_API}/guilds/{guild_id}/members/{user_id}",
                headers=headers_bot,
            )
            if r.status_code != 200:
                continue
            member = r.json()
            member_roles = set(member.get("roles", []))

            # 길드 채널 목록 조회
            r = await client.get(
                f"{DISCORD_API}/guilds/{guild_id}/channels",
                headers=headers_bot,
            )
            if r.status_code != 200:
                continue
            channels = r.json()

            # 길드 정보 (권한 계산용 owner_id)
            r = await client.get(f"{DISCORD_API}/guilds/{guild_id}", headers=headers_bot)
            if r.status_code != 200:
                continue
            guild_info = r.json()
            is_owner = guild_info.get("owner_id") == user_id

            for ch in channels:
                if ch.get("type") not in (0, 5):  # 0=text, 5=announcement
                    continue
                if is_owner:
                    accessible.append({
                        "channel_id": ch["id"],
                        "channel_name": ch.get("name", ""),
                        "guild_id": guild_id,
                        "guild_name": bot_guilds[guild_id]["name"],
                    })
                    continue

                # permission_overwrites 기반 view_channel 체크 (Discord 사양 준수)
                VIEW_CHANNEL = 1 << 10
                ADMINISTRATOR = 1 << 3

                # 1단계: @everyone role의 permissions 비트필드에서 기본 권한 시작
                everyone_role = next(
                    (r for r in guild_info.get("roles", []) if r["id"] == guild_id), None
                )
                perms = int(everyone_role["permissions"]) if everyone_role else 0

                # ADMINISTRATOR는 채널 overwrite와 무관하게 무조건 허용
                if perms & ADMINISTRATOR:
                    accessible.append({
                        "channel_id": ch["id"],
                        "channel_name": ch.get("name", ""),
                        "guild_id": guild_id,
                        "guild_name": bot_guilds[guild_id]["name"],
                    })
                    continue

                # 멤버 role의 기본 permissions 합산
                for role in guild_info.get("roles", []):
                    if role["id"] in member_roles:
                        perms |= int(role["permissions"])

                if perms & ADMINISTRATOR:
                    accessible.append({
                        "channel_id": ch["id"],
                        "channel_name": ch.get("name", ""),
                        "guild_id": guild_id,
                        "guild_name": bot_guilds[guild_id]["name"],
                    })
                    continue

                # 2단계: @everyone role channel overwrite
                for ow in ch.get("permission_overwrites", []):
                    if ow["id"] == guild_id:
                        perms &= ~int(ow["deny"])
                        perms |= int(ow["allow"])

                # 3단계: 멤버 role overwrites — Discord 사양상 배치 처리
                role_deny = 0
                role_allow = 0
                for ow in ch.get("permission_overwrites", []):
                    if ow["id"] in member_roles:
                        role_deny |= int(ow["deny"])
                        role_allow |= int(ow["allow"])
                perms &= ~role_deny
                perms |= role_allow

                # 4단계: 멤버 개인 overwrite (최우선)
                for ow in ch.get("permission_overwrites", []):
                    if ow["id"] == user_id:
                        perms &= ~int(ow["deny"])
                        perms |= int(ow["allow"])

                if perms & VIEW_CHANNEL:
                    accessible.append({
                        "channel_id": ch["id"],
                        "channel_name": ch.get("name", ""),
                        "guild_id": guild_id,
                        "guild_name": bot_guilds[guild_id]["name"],
                    })

    return accessible


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
        return HTMLResponse("Invalid state", status_code=400)

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
            return RedirectResponse("/auth/login")
        token_data = r.json()
        access_token = token_data["access_token"]

        # 유저 정보 조회
        r = await client.get(
            f"{DISCORD_API}/users/@me",
            headers={"Authorization": f"Bearer {access_token}"},
        )
        r.raise_for_status()
        user = r.json()

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
