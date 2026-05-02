"""채널 권한 캐시 무효화 헬퍼.

봇과 웹 양쪽에서 import — Discord API 의존성(httpx) 없이 PostgreSQL 작업만
수행하는 가벼운 모듈로 분리. 다음 invalidation 패턴을 지원:

  - invalidate_user(user_id)         : 특정 사용자의 모든 길드 캐시 삭제
                                       (멤버 탈퇴/밴/역할 변경 시)
  - invalidate_guild(guild_id)       : 그 길드를 포함한 모든 사용자 캐시 삭제
                                       (역할 권한·채널 overwrite 변경 시)
  - invalidate_guilds(guild_ids)     : 여러 길드를 한 번에 청소
                                       (봇 게이트웨이 re-IDENTIFY 시 등)

다음 사용자 요청에서 lazy fill로 자동 재계산되는 게 전제. 즉 invalidate는
"강제 갱신"이 아니라 "다음에 stale 안 쓰게 표시"하는 역할.
"""
import logging

import asyncpg

log = logging.getLogger(__name__)


async def invalidate_user(pool: asyncpg.Pool, user_id: str) -> None:
    """특정 사용자의 캐시 행 삭제 (멤버-스코프 이벤트용)."""
    async with pool.acquire() as conn:
        result = await conn.execute(
            "DELETE FROM channel_access_cache WHERE user_id = $1", user_id,
        )
    log.info("cache invalidated user=%s (%s)", user_id, result)


async def invalidate_guild(pool: asyncpg.Pool, guild_id: str) -> None:
    """guild_id가 포함된 모든 캐시 행 삭제 (길드-스코프 이벤트용).

    GIN 인덱스(idx_cac_guilds)로 효율적인 검색.
    """
    async with pool.acquire() as conn:
        result = await conn.execute(
            "DELETE FROM channel_access_cache WHERE $1 = ANY(guild_ids)",
            guild_id,
        )
    log.info("cache invalidated guild=%s (%s)", guild_id, result)


async def invalidate_guilds(pool: asyncpg.Pool, guild_ids: list[str]) -> None:
    """여러 길드 한 번에 청소 (게이트웨이 re-IDENTIFY 등).

    빈 리스트면 no-op.
    """
    if not guild_ids:
        return
    async with pool.acquire() as conn:
        result = await conn.execute(
            "DELETE FROM channel_access_cache WHERE guild_ids && $1::text[]",
            guild_ids,
        )
    log.info("cache invalidated guilds=%d (%s)", len(guild_ids), result)
