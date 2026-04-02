import aiosqlite
import json
import os
from datetime import datetime, timezone

from db.base import AbstractDatabase


class SQLiteDatabase(AbstractDatabase):
    def __init__(self, db_path: str):
        self.db_path = db_path
        self._connection: aiosqlite.Connection | None = None

    async def connect(self) -> None:
        os.makedirs(os.path.dirname(self.db_path), exist_ok=True)
        self._connection = await aiosqlite.connect(self.db_path)
        self._connection.row_factory = aiosqlite.Row
        await self._connection.execute("PRAGMA journal_mode=WAL")
        await self._create_tables()

    async def close(self) -> None:
        if self._connection:
            await self._connection.close()
            self._connection = None

    @property
    def conn(self) -> aiosqlite.Connection:
        assert self._connection is not None, "Database not connected"
        return self._connection

    async def _create_tables(self) -> None:
        await self.conn.executescript("""
            CREATE TABLE IF NOT EXISTS messages (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                message_id TEXT NOT NULL,
                guild_id TEXT NOT NULL,
                channel_id TEXT NOT NULL,
                channel_name TEXT NOT NULL,
                author_id TEXT NOT NULL,
                author_name TEXT NOT NULL,
                content TEXT NOT NULL,
                attachments TEXT NOT NULL DEFAULT '[]',
                action TEXT NOT NULL DEFAULT 'add',
                created_at TEXT NOT NULL
            );

            CREATE TABLE IF NOT EXISTS log_channels (
                guild_id TEXT NOT NULL,
                channel_id TEXT NOT NULL,
                guild_name TEXT NOT NULL DEFAULT '',
                channel_name TEXT NOT NULL DEFAULT '',
                PRIMARY KEY (guild_id, channel_id)
            );

            CREATE INDEX IF NOT EXISTS idx_messages_channel
                ON messages (guild_id, channel_id);
            CREATE INDEX IF NOT EXISTS idx_messages_message_id
                ON messages (message_id);
            CREATE INDEX IF NOT EXISTS idx_messages_author
                ON messages (guild_id, author_id);
            CREATE INDEX IF NOT EXISTS idx_messages_created_at
                ON messages (created_at);
            CREATE INDEX IF NOT EXISTS idx_messages_action
                ON messages (message_id, action);

            CREATE TABLE IF NOT EXISTS guild_events (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                event_type TEXT NOT NULL,
                guild_id TEXT NOT NULL,
                actor_id TEXT,
                actor_name TEXT,
                target_id TEXT,
                target_name TEXT,
                details TEXT NOT NULL DEFAULT '{}',
                occurred_at TEXT NOT NULL
            );

            CREATE INDEX IF NOT EXISTS idx_guild_events_guild
                ON guild_events (guild_id, occurred_at);
            CREATE INDEX IF NOT EXISTS idx_guild_events_type
                ON guild_events (guild_id, event_type);
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
        action: str = "add",
    ) -> None:
        await self.conn.execute(
            """
            INSERT INTO messages
                (message_id, guild_id, channel_id, channel_name,
                 author_id, author_name, content, attachments, action, created_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                message_id, guild_id, channel_id, channel_name,
                author_id, author_name, content,
                json.dumps(attachments), action, created_at.isoformat(),
            ),
        )
        await self.conn.commit()

    async def save_edit(
        self,
        message_id: str,
        old_content: str,
        new_content: str,
    ) -> None:
        cursor = await self.conn.execute(
            "SELECT guild_id, channel_id, channel_name, author_id, author_name, attachments FROM messages WHERE message_id = ? ORDER BY id DESC LIMIT 1",
            (message_id,),
        )
        row = await cursor.fetchone()
        if row:
            await self.conn.execute(
                """
                INSERT INTO messages
                    (message_id, guild_id, channel_id, channel_name,
                     author_id, author_name, content, attachments, action, created_at)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'update', ?)
                """,
                (message_id, row[0], row[1], row[2], row[3], row[4], new_content, row[5], datetime.now(timezone.utc).isoformat()),
            )
            await self.conn.commit()

    async def save_delete(self, message_id: str) -> None:
        cursor = await self.conn.execute(
            "SELECT guild_id, channel_id, channel_name, author_id, author_name, content, attachments FROM messages WHERE message_id = ? ORDER BY id DESC LIMIT 1",
            (message_id,),
        )
        row = await cursor.fetchone()
        if row:
            await self.conn.execute(
                """
                INSERT INTO messages
                    (message_id, guild_id, channel_id, channel_name,
                     author_id, author_name, content, attachments, action, created_at)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'delete', ?)
                """,
                (message_id, row[0], row[1], row[2], row[3], row[4], row[5], row[6], datetime.now(timezone.utc).isoformat()),
            )
            await self.conn.commit()

    # ── Log channel settings ──

    async def add_log_channel(self, guild_id: str, channel_id: str, guild_name: str = "", channel_name: str = "") -> None:
        await self.conn.execute(
            "INSERT INTO log_channels (guild_id, channel_id, guild_name, channel_name) VALUES (?, ?, ?, ?) ON CONFLICT (guild_id, channel_id) DO UPDATE SET guild_name = ?, channel_name = ?",
            (guild_id, channel_id, guild_name, channel_name, guild_name, channel_name),
        )
        await self.conn.commit()

    async def remove_log_channel(self, guild_id: str, channel_id: str) -> bool:
        cursor = await self.conn.execute(
            "DELETE FROM log_channels WHERE guild_id = ? AND channel_id = ?",
            (guild_id, channel_id),
        )
        await self.conn.commit()
        return cursor.rowcount > 0

    async def get_log_channels(self, guild_id: str) -> list[str]:
        cursor = await self.conn.execute(
            "SELECT channel_id FROM log_channels WHERE guild_id = ?",
            (guild_id,),
        )
        rows = await cursor.fetchall()
        return [row[0] for row in rows]

    async def is_channel_logged(self, guild_id: str, channel_id: str) -> bool:
        channels = await self.get_log_channels(guild_id)
        if not channels:
            return False
        return channel_id in channels

    # ── Message info ──

    async def get_latest_message_info(self, message_id: str) -> dict | None:
        cursor = await self.conn.execute(
            "SELECT content, author_name FROM messages WHERE message_id = ? ORDER BY id DESC LIMIT 1",
            (message_id,),
        )
        row = await cursor.fetchone()
        return {"content": row[0], "author_name": row[1]} if row else None

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
        await self.conn.execute(
            """
            INSERT INTO guild_events
                (event_type, guild_id, actor_id, actor_name, target_id, target_name, details, occurred_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                event_type, guild_id, actor_id, actor_name,
                target_id, target_name,
                json.dumps(details, ensure_ascii=False),
                occurred_at.isoformat(),
            ),
        )
        await self.conn.commit()

    # ── Stats ──

    async def get_message_count(self, guild_id: str) -> int:
        cursor = await self.conn.execute(
            "SELECT COUNT(*) FROM messages WHERE guild_id = ?",
            (guild_id,),
        )
        row = await cursor.fetchone()
        return row[0] if row else 0

    # ── Query (for AI features) ──

    async def get_messages_by_channel(
        self,
        guild_id: str,
        channel_id: str,
        since: datetime | None = None,
        limit: int = 500,
    ) -> list[dict]:
        if since:
            cursor = await self.conn.execute(
                """
                SELECT message_id, channel_name, author_name, content, created_at
                FROM messages
                WHERE guild_id = ? AND channel_id = ? AND created_at >= ?
                ORDER BY created_at ASC
                LIMIT ?
                """,
                (guild_id, channel_id, since.isoformat(), limit),
            )
        else:
            cursor = await self.conn.execute(
                """
                SELECT message_id, channel_name, author_name, content, created_at
                FROM messages
                WHERE guild_id = ? AND channel_id = ?
                ORDER BY created_at DESC
                LIMIT ?
                """,
                (guild_id, channel_id, limit),
            )
        rows = await cursor.fetchall()
        return [
            {
                "message_id": r[0],
                "channel_name": r[1],
                "author_name": r[2],
                "content": r[3],
                "created_at": r[4],
            }
            for r in rows
        ]

    async def get_messages_by_author(
        self,
        guild_id: str,
        author_id: str,
        since: datetime | None = None,
        limit: int = 500,
    ) -> list[dict]:
        if since:
            cursor = await self.conn.execute(
                """
                SELECT message_id, channel_name, author_name, content, created_at
                FROM messages
                WHERE guild_id = ? AND author_id = ? AND created_at >= ?
                ORDER BY created_at ASC
                LIMIT ?
                """,
                (guild_id, author_id, since.isoformat(), limit),
            )
        else:
            cursor = await self.conn.execute(
                """
                SELECT message_id, channel_name, author_name, content, created_at
                FROM messages
                WHERE guild_id = ? AND author_id = ?
                ORDER BY created_at DESC
                LIMIT ?
                """,
                (guild_id, author_id, limit),
            )
        rows = await cursor.fetchall()
        return [
            {
                "message_id": r[0],
                "channel_name": r[1],
                "author_name": r[2],
                "content": r[3],
                "created_at": r[4],
            }
            for r in rows
        ]

    async def search_messages(
        self,
        guild_id: str,
        keyword: str,
        limit: int = 200,
    ) -> list[dict]:
        escaped = keyword.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")
        cursor = await self.conn.execute(
            """
            SELECT message_id, channel_name, author_name, content, created_at
            FROM messages
            WHERE guild_id = ? AND content LIKE ? ESCAPE '\\'
            ORDER BY created_at DESC
            LIMIT ?
            """,
            (guild_id, f"%{escaped}%", limit),
        )
        rows = await cursor.fetchall()
        return [
            {
                "message_id": r[0],
                "channel_name": r[1],
                "author_name": r[2],
                "content": r[3],
                "created_at": r[4],
            }
            for r in rows
        ]
