import discord
from discord.ext import commands
from datetime import datetime, timezone

from db.base import AbstractDatabase


class ScheduledEventsCog(commands.Cog):
    def __init__(self, bot: commands.Bot, db: AbstractDatabase):
        self.bot = bot
        self.db = db

    # ── 예약 이벤트 ──

    @commands.Cog.listener()
    async def on_scheduled_event_create(self, event: discord.ScheduledEvent):
        if event.guild is None:
            return

        await self.db.save_guild_event(
            event_type="scheduled_event_create",
            guild_id=str(event.guild.id),
            actor_id=str(event.creator_id) if event.creator_id else None,
            actor_name=str(event.creator) if event.creator else None,
            target_id=str(event.id),
            target_name=event.name,
            details={
                "event_name": event.name,
                "description": event.description,
                "start_time": event.start_time.isoformat(),
                "end_time": event.end_time.isoformat() if event.end_time else None,
                "location": str(event.location) if event.location else None,
                "entity_type": str(event.entity_type),
            },
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_scheduled_event_update(
        self,
        before: discord.ScheduledEvent,
        after: discord.ScheduledEvent,
    ):
        if after.guild is None:
            return

        changes: dict = {}
        if before.name != after.name:
            changes["name"] = {"before": before.name, "after": after.name}
        if before.description != after.description:
            changes["description"] = {"before": before.description, "after": after.description}
        if before.start_time != after.start_time:
            changes["start_time"] = {
                "before": before.start_time.isoformat() if before.start_time else None,
                "after": after.start_time.isoformat() if after.start_time else None,
            }
        if before.end_time != after.end_time:
            changes["end_time"] = {
                "before": before.end_time.isoformat() if before.end_time else None,
                "after": after.end_time.isoformat() if after.end_time else None,
            }
        if before.status != after.status:
            changes["status"] = {
                "before": str(before.status),
                "after": str(after.status),
            }
        if before.location != after.location:
            changes["location"] = {
                "before": str(before.location) if before.location else None,
                "after": str(after.location) if after.location else None,
            }
        if before.entity_type != after.entity_type:
            changes["entity_type"] = {
                "before": str(before.entity_type),
                "after": str(after.entity_type),
            }

        if not changes:
            return

        await self.db.save_guild_event(
            event_type="scheduled_event_update",
            guild_id=str(after.guild.id),
            actor_id=None,
            actor_name=None,
            target_id=str(after.id),
            target_name=after.name,
            details={"changes": changes},
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_scheduled_event_delete(self, event: discord.ScheduledEvent):
        if event.guild is None:
            return

        await self.db.save_guild_event(
            event_type="scheduled_event_delete",
            guild_id=str(event.guild.id),
            actor_id=None,
            actor_name=None,
            target_id=str(event.id),
            target_name=event.name,
            details={"event_name": event.name},
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_scheduled_event_user_add(
        self,
        event: discord.ScheduledEvent,
        user: discord.User,
    ):
        if event.guild is None:
            return

        await self.db.save_guild_event(
            event_type="scheduled_event_user_add",
            guild_id=str(event.guild.id),
            actor_id=str(user.id),
            actor_name=str(user),
            target_id=str(event.id),
            target_name=event.name,
            details={"event_name": event.name, "event_id": str(event.id)},
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_scheduled_event_user_remove(
        self,
        event: discord.ScheduledEvent,
        user: discord.User,
    ):
        if event.guild is None:
            return

        await self.db.save_guild_event(
            event_type="scheduled_event_user_remove",
            guild_id=str(event.guild.id),
            actor_id=str(user.id),
            actor_name=str(user),
            target_id=str(event.id),
            target_name=event.name,
            details={"event_name": event.name, "event_id": str(event.id)},
            occurred_at=datetime.now(timezone.utc),
        )

    # ── 스테이지 ──

    @commands.Cog.listener()
    async def on_stage_instance_create(self, stage: discord.StageInstance):
        if stage.guild is None:
            return

        await self.db.save_guild_event(
            event_type="stage_instance_create",
            guild_id=str(stage.guild.id),
            actor_id=None,
            actor_name=None,
            target_id=str(stage.id),
            target_name=stage.topic,
            details={
                "topic": stage.topic,
                "channel_id": str(stage.channel_id),
                "privacy_level": str(stage.privacy_level),
            },
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_stage_instance_update(
        self,
        before: discord.StageInstance,
        after: discord.StageInstance,
    ):
        if after.guild is None:
            return

        changes: dict = {}
        if before.topic != after.topic:
            changes["topic"] = {"before": before.topic, "after": after.topic}
        if before.privacy_level != after.privacy_level:
            changes["privacy_level"] = {
                "before": str(before.privacy_level),
                "after": str(after.privacy_level),
            }

        if not changes:
            return

        await self.db.save_guild_event(
            event_type="stage_instance_update",
            guild_id=str(after.guild.id),
            actor_id=None,
            actor_name=None,
            target_id=str(after.id),
            target_name=after.topic,
            details={"changes": changes},
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_stage_instance_delete(self, stage: discord.StageInstance):
        if stage.guild is None:
            return

        await self.db.save_guild_event(
            event_type="stage_instance_delete",
            guild_id=str(stage.guild.id),
            actor_id=None,
            actor_name=None,
            target_id=str(stage.id),
            target_name=stage.topic,
            details={
                "topic": stage.topic,
                "channel_id": str(stage.channel_id),
            },
            occurred_at=datetime.now(timezone.utc),
        )


async def setup(bot: commands.Bot):
    await bot.add_cog(ScheduledEventsCog(bot, bot.db))
