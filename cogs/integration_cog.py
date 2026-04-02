import discord
from discord.ext import commands
from datetime import datetime, timezone

from db.base import AbstractDatabase


class IntegrationCog(commands.Cog):
    def __init__(self, bot: commands.Bot, db: AbstractDatabase):
        self.bot = bot
        self.db = db

    # ── 연동 ──

    @commands.Cog.listener()
    async def on_guild_integrations_update(self, guild: discord.Guild):
        if guild is None:
            return

        await self.db.save_guild_event(
            event_type="integrations_update",
            guild_id=str(guild.id),
            actor_id=None,
            actor_name=None,
            target_id=None,
            target_name=None,
            details={},
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_integration_create(self, integration: discord.Integration):
        if integration.guild is None:
            return

        await self.db.save_guild_event(
            event_type="integration_create",
            guild_id=str(integration.guild.id),
            actor_id=None,
            actor_name=None,
            target_id=str(integration.id),
            target_name=integration.name,
            details={
                "name": integration.name,
                "type": integration.type,
                "account": str(integration.account) if hasattr(integration, "account") else None,
            },
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_integration_update(self, integration: discord.Integration):
        if integration.guild is None:
            return

        await self.db.save_guild_event(
            event_type="integration_update",
            guild_id=str(integration.guild.id),
            actor_id=None,
            actor_name=None,
            target_id=str(integration.id),
            target_name=integration.name,
            details={
                "name": integration.name,
                "type": integration.type,
                "account": str(integration.account) if hasattr(integration, "account") else None,
            },
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_integration_delete(self, integration: discord.RawIntegrationDeleteEvent):
        if integration.guild_id is None:
            return

        await self.db.save_guild_event(
            event_type="integration_delete",
            guild_id=str(integration.guild_id),
            actor_id=None,
            actor_name=None,
            target_id=str(integration.id),
            target_name=None,
            details={
                "name": integration.name if hasattr(integration, "name") else None,
                "type": integration.type if hasattr(integration, "type") else None,
                "account": str(integration.account) if hasattr(integration, "account") else None,
            },
            occurred_at=datetime.now(timezone.utc),
        )

    # ── 웹훅 ──

    @commands.Cog.listener()
    async def on_webhooks_update(self, channel: discord.abc.GuildChannel):
        if channel.guild is None:
            return

        await self.db.save_guild_event(
            event_type="webhooks_update",
            guild_id=str(channel.guild.id),
            actor_id=None,
            actor_name=None,
            target_id=str(channel.id),
            target_name=channel.name,
            details={
                "channel_id": str(channel.id),
                "channel_name": channel.name,
            },
            occurred_at=datetime.now(timezone.utc),
        )


async def setup(bot: commands.Bot):
    await bot.add_cog(IntegrationCog(bot, bot.db))
