import discord
from discord.ext import commands
from datetime import datetime, timezone
from typing import Optional

from db.base import AbstractDatabase
from utils.attachments import download_attachment, download_emojis


class LoggingCog(commands.Cog):
    def __init__(self, bot: commands.Bot, db: AbstractDatabase):
        self.bot = bot
        self.db = db
        self._pinned_cache: dict[str, set[str]] = {}

    @commands.Cog.listener()
    async def on_ready(self):
        """봇 시작 시 로깅 대상 채널의 핀 목록을 캐싱."""
        for guild in self.bot.guilds:
            guild_id = str(guild.id)
            channel_ids = await self.db.get_log_channels(guild_id)
            for ch_id in channel_ids:
                channel = self.bot.get_channel(int(ch_id))
                if channel is None:
                    continue
                try:
                    pinned = await channel.pins()
                    self._pinned_cache[ch_id] = {str(m.id) for m in pinned}
                except Exception:
                    self._pinned_cache[ch_id] = set()

    @commands.Cog.listener()
    async def on_message(self, message: discord.Message):
        if message.author.bot:
            return
        if message.guild is None:
            return
        if message.type != discord.MessageType.default and message.type != discord.MessageType.reply:
            return

        guild_id = str(message.guild.id)
        channel_id = str(message.channel.id)

        if not await self.db.is_channel_logged(guild_id, channel_id):
            return

        attachments = []
        for a in message.attachments:
            local_path = await download_attachment(
                url=a.url,
                channel_id=channel_id,
                message_id=str(message.id),
                filename=a.filename,
            )
            attachments.append({
                "url": a.url,
                "local_path": local_path,
                "filename": a.filename,
                "content_type": a.content_type or "",
                "size": a.size,
            })

        content = message.content
        if content:
            await download_emojis(content)

        if not content and attachments:
            content = " ".join(f"[{a['filename']}]" for a in attachments)
        elif content and attachments:
            content += " " + " ".join(f"[{a['filename']}]" for a in attachments)

        stickers = [s.name for s in message.stickers]
        if stickers:
            sticker_text = " ".join(f"[스티커: {name}]" for name in stickers)
            content = f"{content} {sticker_text}".strip() if content else sticker_text

        await self.db.save_message(
            message_id=str(message.id),
            guild_id=guild_id,
            channel_id=channel_id,
            channel_name=message.channel.name,
            author_id=str(message.author.id),
            author_name=str(message.author),
            content=content,
            attachments=attachments,
            created_at=message.created_at.replace(tzinfo=timezone.utc),
        )

    @commands.Cog.listener()
    async def on_message_edit(self, before: discord.Message, after: discord.Message):
        if before.author.bot:
            return
        if before.guild is None:
            return
        if before.content == after.content:
            return

        guild_id = str(before.guild.id)
        channel_id = str(before.channel.id)

        if not await self.db.is_channel_logged(guild_id, channel_id):
            return

        await self.db.save_edit(
            message_id=str(before.id),
            old_content=before.content,
            new_content=after.content,
        )

    @commands.Cog.listener()
    async def on_message_delete(self, message: discord.Message):
        if message.author.bot:
            return
        if message.guild is None:
            return

        guild_id = str(message.guild.id)
        channel_id = str(message.channel.id)

        if not await self.db.is_channel_logged(guild_id, channel_id):
            return

        await self.db.save_delete(message_id=str(message.id))

    @commands.Cog.listener()
    async def on_raw_message_delete(self, payload: discord.RawMessageDeleteEvent):
        # 캐시에 없는 메시지(봇 재시작 후, 오래된 메시지 등) 삭제도 기록
        if payload.cached_message is not None:
            return  # 캐시에 있으면 on_message_delete가 이미 처리

        if payload.guild_id is None:
            return

        guild_id = str(payload.guild_id)
        channel_id = str(payload.channel_id)

        if not await self.db.is_channel_logged(guild_id, channel_id):
            return

        await self.db.save_delete(message_id=str(payload.message_id))

    @commands.Cog.listener()
    async def on_bulk_message_delete(self, messages: list[discord.Message]):
        if not messages:
            return

        # 첫 메시지 기준으로 guild/channel 판별
        first = messages[0]
        if first.guild is None:
            return

        guild_id = str(first.guild.id)
        channel_id = str(first.channel.id)

        if not await self.db.is_channel_logged(guild_id, channel_id):
            return

        message_ids = []
        for msg in messages:
            if msg.author.bot:
                continue
            await self.db.save_delete(message_id=str(msg.id))
            message_ids.append(str(msg.id))

        await self.db.save_guild_event(
            event_type="bulk_message_delete",
            guild_id=guild_id,
            actor_id=None,
            actor_name=None,
            target_id=channel_id,
            target_name=first.channel.name,
            details={
                "channel_id": channel_id,
                "channel_name": first.channel.name,
                "count": len(message_ids),
                "message_ids": message_ids,
            },
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_raw_message_edit(self, payload: discord.RawMessageUpdateEvent):
        # 캐시에 없는 메시지 수정도 기록 (내용이 payload에 있을 때만)
        if payload.cached_message is not None:
            return  # 캐시에 있으면 on_message_edit가 이미 처리

        if payload.guild_id is None:
            return

        # 핀 변경만으로 발생한 MESSAGE_UPDATE는 무시
        if "pinned" in payload.data and "content" not in payload.data:
            return

        new_content = payload.data.get("content", "")
        if not new_content:
            return

        # DB에 저장된 최신 내용과 동일하면 스킵 (핀 변경 등 내용 무변경)
        info = await self.db.get_latest_message_info(str(payload.message_id))
        if info and info["content"] == new_content:
            return

        guild_id = str(payload.guild_id)
        channel_id = str(payload.channel_id)

        if not await self.db.is_channel_logged(guild_id, channel_id):
            return

        await self.db.save_edit(
            message_id=str(payload.message_id),
            old_content="(캐시 없음 — 수정 전 내용 불명)",
            new_content=new_content,
        )

    @commands.Cog.listener()
    async def on_raw_bulk_message_delete(
        self, payload: discord.RawBulkMessageDeleteEvent
    ):
        if payload.guild_id is None:
            return

        guild_id = str(payload.guild_id)
        channel_id = str(payload.channel_id)

        if not await self.db.is_channel_logged(guild_id, channel_id):
            return

        # 캐시에 있는 메시지는 on_bulk_message_delete가 이미 처리했으므로
        # 캐시에 없는 메시지만 처리
        cached_ids = {str(m.id) for m in payload.cached_messages}
        uncached_ids = [
            str(mid) for mid in payload.message_ids if str(mid) not in cached_ids
        ]

        if not uncached_ids:
            return

        for message_id in uncached_ids:
            await self.db.save_delete(message_id=message_id)

        # 채널 이름을 가져올 수 있으면 가져온다
        channel = self.bot.get_channel(payload.channel_id)
        channel_name = getattr(channel, "name", "(unknown)")

        await self.db.save_guild_event(
            event_type="bulk_message_delete",
            guild_id=guild_id,
            actor_id=None,
            actor_name=None,
            target_id=channel_id,
            target_name=channel_name,
            details={
                "channel_id": channel_id,
                "channel_name": channel_name,
                "count": len(uncached_ids),
                "message_ids": uncached_ids,
            },
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_guild_channel_pins_update(
        self,
        channel: discord.TextChannel,
        last_pin: Optional[datetime],
    ):
        if channel.guild is None:
            return

        guild_id = str(channel.guild.id)
        channel_id = str(channel.id)

        if not await self.db.is_channel_logged(guild_id, channel_id):
            return

        try:
            pinned = await channel.pins()
            pinned_ids = {str(m.id) for m in pinned}
        except discord.Forbidden:
            pinned_ids = set()
            pinned = []

        prev = self._pinned_cache.get(channel_id)
        self._pinned_cache[channel_id] = pinned_ids

        # 봇 시작 후 첫 이벤트면 캐시만 갱신하고 종료 (이전 상태를 모르므로)
        if prev is None:
            return

        pinned_map = {str(m.id): m for m in pinned}

        for mid in pinned_ids - prev:
            msg = pinned_map.get(mid)
            await self.db.save_guild_event(
                event_type="message_pin",
                guild_id=guild_id,
                actor_id=str(msg.author.id) if msg else None,
                actor_name=str(msg.author) if msg else None,
                target_id=mid,
                target_name=channel.name,
                details={
                    "content": msg.content if msg else "",
                    "author": str(msg.author) if msg else "",
                    "channel_id": channel_id,
                },
                occurred_at=datetime.now(timezone.utc),
            )

        for mid in prev - pinned_ids:
            content = ""
            author_name = None
            info = await self.db.get_latest_message_info(mid)
            if info:
                content = info["content"]
                author_name = info["author_name"]
            # DB에 없으면 Discord API에서 가져오기
            if not content:
                try:
                    msg = await channel.fetch_message(int(mid))
                    content = msg.content[:200] if msg.content else ""
                    author_name = str(msg.author)
                except Exception:
                    pass

            await self.db.save_guild_event(
                event_type="message_unpin",
                guild_id=guild_id,
                actor_id=None,
                actor_name=author_name,
                target_id=mid,
                target_name=channel.name,
                details={
                    "content": content,
                    "channel_id": channel_id,
                },
                occurred_at=datetime.now(timezone.utc),
            )


async def setup(bot: commands.Bot):
    await bot.add_cog(LoggingCog(bot, bot.db))
