"""사용자별 접근 가능한 Discord 채널 계산 및 캐시.

순수 평가 함수와 Discord API를 호출하는 계산 함수, PostgreSQL 캐시 read/write를
한 모듈에 묶었다. 호출자:
  - OAuth 콜백 — 로그인 시 fresh 계산하고 캐시 덮어쓰기
  - MCP 핸들러 — 매 호출 시 캐시 조회, miss 시 lazy fill
  - (향후) 봇 이벤트 핸들러 — invalidate (delete) 트리거

Discord 권한 명세 전체를 적용한다(서버 권한 → ADMINISTRATOR → 카테고리 overwrites
→ 채널 overwrites). 기존 구현은 카테고리 단계가 빠져 있었음.
"""
import asyncio
import json
import logging
import os
from datetime import timedelta

import asyncpg
import httpx

DISCORD_API = "https://discord.com/api/v10"
DISCORD_BOT_TOKEN = os.getenv("DISCORD_TOKEN", "")

# Discord 권한 비트
VIEW_CHANNEL = 1 << 10
ADMINISTRATOR = 1 << 3

# 캐시 TTL — 이벤트 누락 안전망. 봇이 invalidate 못한 변경도 6h 안에 자가 치료.
CACHE_TTL = timedelta(hours=6)

log = logging.getLogger(__name__)


# ── 순수 평가 ────────────────────────────────────────────────────────────────

def _apply_overwrites(
    perms: int,
    overwrites: list,
    member_roles: set[str],
    user_id: str,
    guild_id: str,
) -> int:
    """단일 채널/카테고리의 permission_overwrites를 Discord 명세 순서대로 적용.

    순서: @everyone → 멤버 역할(deny 합산 → allow 합산) → 멤버 개인 (최우선)
    """
    # @everyone overwrite
    for ow in overwrites:
        if ow["id"] == guild_id:
            perms &= ~int(ow["deny"])
            perms |= int(ow["allow"])

    # 역할 overwrites — 명세상 deny 전체 합산 후 allow 전체 합산 (배치)
    role_deny = 0
    role_allow = 0
    for ow in overwrites:
        if ow["id"] in member_roles:
            role_deny |= int(ow["deny"])
            role_allow |= int(ow["allow"])
    perms &= ~role_deny
    perms |= role_allow

    # 멤버 개인 overwrite (최우선)
    for ow in overwrites:
        if ow["id"] == user_id:
            perms &= ~int(ow["deny"])
            perms |= int(ow["allow"])

    return perms


def can_view_channel(
    channel: dict,
    member_roles: set[str],
    guild_info: dict,
    user_id: str,
    guild_id: str,
    categories: dict[str, dict],
) -> bool:
    """사용자가 해당 채널에 VIEW_CHANNEL 권한을 가지는지 반환.

    Discord 명세 평가 순서:
      1. @everyone 서버 권한 (베이스)
      2. 멤버 역할 서버 권한 OR 합산
      3. ADMINISTRATOR 비트면 모든 채널 통과
      4. 카테고리 overwrites (채널이 카테고리에 속하면)
      5. 채널 overwrites (카테고리를 덮어씀)
    """
    if guild_info.get("owner_id") == user_id:
        return True

    everyone_role = next(
        (r for r in guild_info.get("roles", []) if r["id"] == guild_id), None
    )
    perms = int(everyone_role["permissions"]) if everyone_role else 0

    for role in guild_info.get("roles", []):
        if role["id"] in member_roles:
            perms |= int(role["permissions"])

    if perms & ADMINISTRATOR:
        return True

    # 카테고리 overwrites 먼저
    parent_id = channel.get("parent_id")
    if parent_id and parent_id in categories:
        perms = _apply_overwrites(
            perms,
            categories[parent_id].get("permission_overwrites", []),
            member_roles, user_id, guild_id,
        )

    # 채널 overwrites
    perms = _apply_overwrites(
        perms,
        channel.get("permission_overwrites", []),
        member_roles, user_id, guild_id,
    )

    return bool(perms & VIEW_CHANNEL)


# ── Discord API 계산 ─────────────────────────────────────────────────────────

async def compute_accessible_channels(user_id: str) -> list[dict]:
    """봇이 속한 모든 길드를 순회하며 사용자가 접근 가능한 채널 목록 계산.

    user의 Discord access token이 필요 없다 — 봇 토큰만으로 모든 정보 수집.
    이로써 캐시 lazy fill (사용자 토큰 없을 때)에서도 동일하게 사용 가능.

    반환 항목:
      {channel_id, channel_name, category_id, category_name, guild_id, guild_name}
    """
    headers_bot = {"Authorization": f"Bot {DISCORD_BOT_TOKEN}"}

    async with httpx.AsyncClient(timeout=httpx.Timeout(15.0)) as client:
        r = await client.get(f"{DISCORD_API}/users/@me/guilds", headers=headers_bot)
        r.raise_for_status()
        bot_guilds = {g["id"]: g for g in r.json()}

        async def fetch_guild(guild_id: str) -> list[dict]:
            try:
                member_r, channels_r, guild_r = await asyncio.gather(
                    client.get(
                        f"{DISCORD_API}/guilds/{guild_id}/members/{user_id}",
                        headers=headers_bot,
                    ),
                    client.get(
                        f"{DISCORD_API}/guilds/{guild_id}/channels",
                        headers=headers_bot,
                    ),
                    client.get(f"{DISCORD_API}/guilds/{guild_id}", headers=headers_bot),
                )
            except httpx.HTTPError:
                log.exception("Discord API 호출 실패 (guild_id=%s)", guild_id)
                return []

            # 사용자가 길드에 없으면 멤버 조회 404 → 그 길드는 스킵
            if member_r.status_code == 404:
                return []
            if (
                member_r.status_code != 200
                or channels_r.status_code != 200
                or guild_r.status_code != 200
            ):
                return []

            member = member_r.json()
            channels = channels_r.json()
            guild_info = guild_r.json()
            member_roles = set(member.get("roles", []))
            guild_name = bot_guilds[guild_id]["name"]

            # 카테고리 (type=4)만 추려 parent_id 역참조용 맵 구성
            categories = {ch["id"]: ch for ch in channels if ch.get("type") == 4}

            result = []
            for ch in channels:
                if ch.get("type") not in (0, 5):  # 0=text, 5=announcement
                    continue
                if not can_view_channel(
                    ch, member_roles, guild_info, user_id, guild_id, categories
                ):
                    continue
                parent_id = ch.get("parent_id")
                result.append({
                    "channel_id": ch["id"],
                    "channel_name": ch.get("name", ""),
                    "category_id": parent_id,
                    "category_name": (
                        categories[parent_id].get("name", "") if parent_id and parent_id in categories else ""
                    ),
                    "guild_id": guild_id,
                    "guild_name": guild_name,
                })
            return result

        per_guild = await asyncio.gather(*(fetch_guild(g) for g in bot_guilds))

    return [c for guild_result in per_guild for c in guild_result]


# ── 캐시 read/write ──────────────────────────────────────────────────────────

async def write_cache(pool: asyncpg.Pool, user_id: str, channels: list[dict]) -> None:
    """user_id의 캐시를 새 채널 목록으로 덮어쓰기."""
    guild_ids = sorted({c["guild_id"] for c in channels})
    async with pool.acquire() as conn:
        await conn.execute(
            """
            INSERT INTO channel_access_cache
                (user_id, channels, guild_ids, computed_at, expires_at)
            VALUES ($1, $2::jsonb, $3, now(), now() + $4)
            ON CONFLICT (user_id) DO UPDATE SET
                channels    = EXCLUDED.channels,
                guild_ids   = EXCLUDED.guild_ids,
                computed_at = EXCLUDED.computed_at,
                expires_at  = EXCLUDED.expires_at
            """,
            user_id, json.dumps(channels), guild_ids, CACHE_TTL,
        )


async def read_cache(pool: asyncpg.Pool, user_id: str) -> list[dict] | None:
    """캐시 hit 시 채널 리스트 반환, miss/expired 시 None."""
    async with pool.acquire() as conn:
        row = await conn.fetchrow(
            """
            SELECT channels FROM channel_access_cache
             WHERE user_id = $1 AND expires_at > now()
            """,
            user_id,
        )
    if row is None:
        return None
    # asyncpg가 JSONB를 dict/list로 디코드해 주지만 환경에 따라 string일 수 있어 방어적 처리
    raw = row["channels"]
    return raw if isinstance(raw, list) else json.loads(raw)


async def get_or_compute_channels(pool: asyncpg.Pool, user_id: str) -> list[dict]:
    """캐시에서 채널 조회, miss 시 Discord API로 계산하고 캐시에 채워넣기."""
    cached = await read_cache(pool, user_id)
    if cached is not None:
        return cached
    log.info("channel_access_cache miss user_id=%s — Discord API로 재계산", user_id)
    channels = await compute_accessible_channels(user_id)
    await write_cache(pool, user_id, channels)
    return channels
