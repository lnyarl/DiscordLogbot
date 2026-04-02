import discord
from discord.ext import commands
from datetime import datetime, timezone
from typing import Optional

from db.base import AbstractDatabase


class LoggingCog(commands.Cog):
    def __init__(self, bot: commands.Bot, db: AbstractDatabase):
        self.bot = bot
        self.db = db

    @commands.Cog.listener()
    async def on_message(self, message: discord.Message):
        if message.author.bot:
            return
        if message.guild is None:
            return

        guild_id = str(message.guild.id)
        channel_id = str(message.channel.id)

        if not await self.db.is_channel_logged(guild_id, channel_id):
            return

        attachments = [
            {
                "url": a.url,
                "filename": a.filename,
                "content_type": a.content_type or "",
                "size": a.size,
            }
            for a in message.attachments
        ]

        await self.db.save_message(
            message_id=str(message.id),
            guild_id=guild_id,
            channel_id=channel_id,
            channel_name=message.channel.name,
            author_id=str(message.author.id),
            author_name=str(message.author),
            content=message.content,
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
    async def on_raw_message_edit(self, payload: discord.RawMessageUpdateEvent):
        # 캐시에 없는 메시지 수정도 기록 (내용이 payload에 있을 때만)
        if payload.cached_message is not None:
            return  # 캐시에 있으면 on_message_edit가 이미 처리

        if payload.guild_id is None:
            return

        new_content = payload.data.get("content", "")
        if not new_content:
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

        await self.db.save_guild_event(
            event_type="channel_pins_update",
            guild_id=guild_id,
            actor_id=None,
            actor_name=None,
            target_id=channel_id,
            target_name=channel.name,
            details={"last_pin": last_pin.isoformat() if last_pin else None},
            occurred_at=datetime.now(timezone.utc),
        )


async def setup(bot: commands.Bot):
    await bot.add_cog(LoggingCog(bot, bot.db))
