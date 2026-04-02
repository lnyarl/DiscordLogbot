import json
from datetime import datetime, timezone

import asyncpg

from db.base import AbstractDatabase


class PostgreSQLDatabase(AbstractDatabase):
    def __init__(self, database_url: str):
        self.database_url = database_url
        self._pool: asyncpg.Pool | None = None

    async def connect(self) -> None:
        self._pool = await asyncpg.create_pool(self.database_url)
        await self._create_tables()

    async def close(self) -> None:
        if self._pool:
            await self._pool.close()
            self._pool = None

    @property
    def pool(self) -> asyncpg.Pool:
        assert self._pool is not None, "Database not connected"
        return self._pool

    async def _create_tables(self) -> None:
        # asyncpg는 단일 execute()에 다중 statement 불가 — 각각 분리 호출
        async with self.pool.acquire() as conn:
            await conn.execute("""
                CREATE TABLE IF NOT EXISTS messages (
                    id SERIAL PRIMARY KEY,
                    message_id TEXT UNIQUE NOT NULL,
                    guild_id TEXT NOT NULL,
                    channel_id TEXT NOT NULL,
                    channel_name TEXT NOT NULL,
                    author_id TEXT NOT NULL,
                    author_name TEXT NOT NULL,
                    content TEXT NOT NULL,
                    attachments TEXT NOT NULL DEFAULT '[]',
                    created_at TEXT NOT NULL
                )
            """)
            await conn.execute("""
                CREATE TABLE IF NOT EXISTS message_edits (
                    id SERIAL PRIMARY KEY,
                    message_id TEXT NOT NULL,
                    old_content TEXT NOT NULL,
                    new_content TEXT NOT NULL,
                    edited_at TEXT NOT NULL
                )
            """)
            await conn.execute("""
                CREATE TABLE IF NOT EXISTS message_deletes (
                    id SERIAL PRIMARY KEY,
                    message_id TEXT NOT NULL,
                    deleted_at TEXT NOT NULL
                )
            """)
            await conn.execute("""
                CREATE TABLE IF NOT EXISTS log_channels (
                    guild_id TEXT NOT NULL,
                    channel_id TEXT NOT NULL,
                    PRIMARY KEY (guild_id, channel_id)
                )
            """)
            await conn.execute("""
                CREATE INDEX IF NOT EXISTS idx_messages_channel
                    ON messages (guild_id, channel_id)
            """)
            await conn.execute("""
                CREATE INDEX IF NOT EXISTS idx_messages_message_id
                    ON messages (message_id)
            """)
            await conn.execute("""
                CREATE INDEX IF NOT EXISTS idx_messages_author
                    ON messages (guild_id, author_id)
            """)
            await conn.execute("""
                CREATE INDEX IF NOT EXISTS idx_messages_created_at
                    ON messages (created_at)
            """)
            await conn.execute("""
                CREATE INDEX IF NOT EXISTS idx_edits_message_id
                    ON message_edits (message_id)
            """)
            await conn.execute("""
                CREATE INDEX IF NOT EXISTS idx_deletes_message_id
                    ON message_deletes (message_id)
            """)
            await conn.execute("""
                CREATE TABLE IF NOT EXISTS guild_events (
                    id SERIAL PRIMARY KEY,
                    event_type TEXT NOT NULL,
                    guild_id TEXT NOT NULL,
                    actor_id TEXT,
                    actor_name TEXT,
                    target_id TEXT,
                    target_name TEXT,
                    details TEXT NOT NULL DEFAULT '{}',
                    occurred_at TEXT NOT NULL
                )
            """)
            await conn.execute("""
                CREATE INDEX IF NOT EXISTS idx_guild_events_guild
                    ON guild_events (guild_id, occurred_at)
            """)
            await conn.execute("""
                CREATE INDEX IF NOT EXISTS idx_guild_events_type
                    ON guild_events (guild_id, event_type)
            """)

    # ── Messages ──

    async def save_message(
        self,
        message_id: str,
        guild_id: str,
        channel_id: str,
        channel_name: str,
        author_id: str,
        author_name: str,
        content: str,
        attachments: list[dict],
        created_at: datetime,
    ) -> None:
        async with self.pool.acquire() as conn:
            await conn.execute(
                """
                INSERT INTO messages
                    (message_id, guild_id, channel_id, channel_name,
                     author_id, author_name, content, attachments, created_at)
                VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
                ON CONFLICT (message_id) DO NOTHING
                """,
                message_id,
                guild_id,
                channel_id,
                channel_name,
                author_id,
                author_name,
                content,
                json.dumps(attachments),
                created_at.isoformat(),
            )

    async def save_edit(
        self,
        message_id: str,
        old_content: str,
        new_content: str,
    ) -> None:
        now = datetime.now(timezone.utc).isoformat()
        async with self.pool.acquire() as conn:
            await conn.execute(
                """
                INSERT INTO message_edits (message_id, old_content, new_content, edited_at)
                VALUES ($1, $2, $3, $4)
                """,
                message_id,
                old_content,
                new_content,
                now,
            )

    async def save_delete(self, message_id: str) -> None:
        now = datetime.now(timezone.utc).isoformat()
        async with self.pool.acquire() as conn:
            await conn.execute(
                """
                INSERT INTO message_deletes (message_id, deleted_at)
                VALUES ($1, $2)
                """,
                message_id,
                now,
            )

    # ── Log channel settings ──

    async def add_log_channel(self, guild_id: str, channel_id: str) -> None:
        async with self.pool.acquire() as conn:
            await conn.execute(
                """
                INSERT INTO log_channels (guild_id, channel_id)
                VALUES ($1, $2)
                ON CONFLICT (guild_id, channel_id) DO NOTHING
                """,
                guild_id,
                channel_id,
            )

    async def remove_log_channel(self, guild_id: str, channel_id: str) -> bool:
        async with self.pool.acquire() as conn:
            result = await conn.execute(
                "DELETE FROM log_channels WHERE guild_id = $1 AND channel_id = $2",
                guild_id,
                channel_id,
            )
            return result.split()[-1] != "0"

    async def get_log_channels(self, guild_id: str) -> list[str]:
        async with self.pool.acquire() as conn:
            rows = await conn.fetch(
                "SELECT channel_id FROM log_channels WHERE guild_id = $1",
                guild_id,
            )
            return [row["channel_id"] for row in rows]

    async def is_channel_logged(self, guild_id: str, channel_id: str) -> bool:
        channels = await self.get_log_channels(guild_id)
        if not channels:
            return False
        return channel_id in channels

    # ── Guild events ──

    async def save_guild_event(
        self,
        event_type: str,
        guild_id: str,
        actor_id: str | None,
        actor_name: str | None,
        target_id: str | None,
        target_name: str | None,
        details: dict,
        occurred_at: datetime,
    ) -> None:
        async with self.pool.acquire() as conn:
            await conn.execute(
                """
                INSERT INTO guild_events
                    (event_type, guild_id, actor_id, actor_name, target_id, target_name, details, occurred_at)
                VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
                """,
                event_type, guild_id, actor_id, actor_name,
                target_id, target_name,
                json.dumps(details, ensure_ascii=False),
                occurred_at.isoformat(),
            )

    # ── Stats ──

    async def get_message_count(self, guild_id: str) -> int:
        async with self.pool.acquire() as conn:
            row = await conn.fetchrow(
                "SELECT COUNT(*) AS cnt FROM messages WHERE guild_id = $1",
                guild_id,
            )
            return row["cnt"] if row else 0

    # ── Query (for AI features) ──

    async def get_messages_by_channel(
        self,
        guild_id: str,
        channel_id: str,
        since: datetime | None = None,
        limit: int = 500,
    ) -> list[dict]:
        async with self.pool.acquire() as conn:
            if since:
                rows = await conn.fetch(
                    """
                    SELECT message_id, channel_name, author_name, content, created_at
                    FROM messages
                    WHERE guild_id = $1 AND channel_id = $2 AND created_at >= $3
                    ORDER BY created_at ASC
                    LIMIT $4
                    """,
                    guild_id,
                    channel_id,
                    since.isoformat(),
                    limit,
                )
            else:
                rows = await conn.fetch(
                    """
                    SELECT message_id, channel_name, author_name, content, created_at
                    FROM messages
                    WHERE guild_id = $1 AND channel_id = $2
                    ORDER BY created_at DESC
                    LIMIT $3
                    """,
                    guild_id,
                    channel_id,
                    limit,
                )
            return [dict(r) for r in rows]

    async def get_messages_by_author(
        self,
        guild_id: str,
        author_id: str,
        since: datetime | None = None,
        limit: int = 500,
    ) -> list[dict]:
        async with self.pool.acquire() as conn:
            if since:
                rows = await conn.fetch(
                    """
                    SELECT message_id, channel_name, author_name, content, created_at
                    FROM messages
                    WHERE guild_id = $1 AND author_id = $2 AND created_at >= $3
                    ORDER BY created_at ASC
                    LIMIT $4
                    """,
                    guild_id,
                    author_id,
                    since.isoformat(),
                    limit,
                )
            else:
                rows = await conn.fetch(
                    """
                    SELECT message_id, channel_name, author_name, content, created_at
                    FROM messages
                    WHERE guild_id = $1 AND author_id = $2
                    ORDER BY created_at DESC
                    LIMIT $3
                    """,
                    guild_id,
                    author_id,
                    limit,
                )
            return [dict(r) for r in rows]

    async def search_messages(
        self,
        guild_id: str,
        keyword: str,
        limit: int = 200,
    ) -> list[dict]:
        # PostgreSQL ILIKE는 ESCAPE 절 미지원 — LIKE로 대체 (대소문자 구분 없이 처리하려면 lower() 사용)
        escaped = keyword.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")
        async with self.pool.acquire() as conn:
            rows = await conn.fetch(
                """
                SELECT message_id, channel_name, author_name, content, created_at
                FROM messages
                WHERE guild_id = $1 AND lower(content) LIKE lower($2) ESCAPE '\\'
                ORDER BY created_at DESC
                LIMIT $3
                """,
                guild_id,
                f"%{escaped}%",
                limit,
            )
            return [dict(r) for r in rows]
