from abc import ABC, abstractmethod
from datetime import datetime


class AbstractDatabase(ABC):
    """Database interface for the logbot.

    All DB backends must implement this interface.
    """

    @abstractmethod
    async def connect(self) -> None: ...

    @abstractmethod
    async def close(self) -> None: ...

    # ── Messages ──

    @abstractmethod
    async def save_message(
        self,
        message_id: str,
        guild_id: str,
        channel_id: str,
        channel_name: str,
        author_id: str,
        author_name: str,
        content: str,
        attachments: list[dict],  # [{url, filename, content_type, size}]
        created_at: datetime,
    ) -> None: ...

    @abstractmethod
    async def save_edit(
        self,
        message_id: str,
        old_content: str,
        new_content: str,
    ) -> None: ...

    @abstractmethod
    async def save_delete(self, message_id: str) -> None: ...

    # ── Log channel settings ──

    @abstractmethod
    async def add_log_channel(self, guild_id: str, channel_id: str) -> None: ...

    @abstractmethod
    async def remove_log_channel(self, guild_id: str, channel_id: str) -> bool: ...

    @abstractmethod
    async def get_log_channels(self, guild_id: str) -> list[str]: ...

    @abstractmethod
    async def is_channel_logged(self, guild_id: str, channel_id: str) -> bool: ...

    # ── Guild events ──

    @abstractmethod
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
    ) -> None: ...

    # ── Stats ──

    @abstractmethod
    async def get_message_count(self, guild_id: str) -> int: ...

    # ── Query (for AI features) ──

    @abstractmethod
    async def get_messages_by_channel(
        self,
        guild_id: str,
        channel_id: str,
        since: datetime | None = None,
        limit: int = 500,
    ) -> list[dict]: ...

    @abstractmethod
    async def get_messages_by_author(
        self,
        guild_id: str,
        author_id: str,
        since: datetime | None = None,
        limit: int = 500,
    ) -> list[dict]: ...

    @abstractmethod
    async def search_messages(
        self,
        guild_id: str,
        keyword: str,
        limit: int = 200,
    ) -> list[dict]: ...
