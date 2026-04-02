import discord
from discord.ext import commands
from datetime import datetime, timezone

from db.base import AbstractDatabase


class ModerationCog(commands.Cog):
    def __init__(self, bot: commands.Bot, db: AbstractDatabase):
        self.bot = bot
        self.db = db

    # ── AutoMod 규칙 ──

    @commands.Cog.listener()
    async def on_automod_rule_create(self, rule: discord.AutoModRule):
        if rule.guild is None:
            return

        try:
            actions = [
                {"type": str(a.type)}
                for a in rule.actions
            ]
        except Exception:
            actions = []

        await self.db.save_guild_event(
            event_type="automod_rule_create",
            guild_id=str(rule.guild.id),
            actor_id=str(rule.creator_id) if rule.creator_id else None,
            actor_name=str(rule.creator) if rule.creator else None,
            target_id=str(rule.id),
            target_name=rule.name,
            details={
                "rule_name": rule.name,
                "trigger_type": str(rule.trigger.type),
                "actions": actions,
            },
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_automod_rule_update(self, rule: discord.AutoModRule):
        if rule.guild is None:
            return

        await self.db.save_guild_event(
            event_type="automod_rule_update",
            guild_id=str(rule.guild.id),
            actor_id=str(rule.creator_id) if rule.creator_id else None,
            actor_name=str(rule.creator) if rule.creator else None,
            target_id=str(rule.id),
            target_name=rule.name,
            details={
                "rule_name": rule.name,
                "rule_id": str(rule.id),
            },
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_automod_rule_delete(self, rule: discord.AutoModRule):
        if rule.guild is None:
            return

        await self.db.save_guild_event(
            event_type="automod_rule_delete",
            guild_id=str(rule.guild.id),
            actor_id=str(rule.creator_id) if rule.creator_id else None,
            actor_name=str(rule.creator) if rule.creator else None,
            target_id=str(rule.id),
            target_name=rule.name,
            details={
                "rule_name": rule.name,
                "rule_id": str(rule.id),
            },
            occurred_at=datetime.now(timezone.utc),
        )

    # ── AutoMod 실행 ──

    @commands.Cog.listener()
    async def on_automod_action(self, execution: discord.AutoModAction):
        if execution.guild is None:
            return

        rule_trigger_name = ""
        if hasattr(execution, "rule_trigger_type") and execution.rule_trigger_type:
            rule_trigger_name = execution.rule_trigger_type.name

        actor_id = None
        actor_name = None
        if execution.member is not None:
            actor_id = str(execution.member.id)
            actor_name = str(execution.member)
        elif execution.member_id is not None:
            actor_id = str(execution.member_id)

        await self.db.save_guild_event(
            event_type="automod_action",
            guild_id=str(execution.guild_id),
            actor_id=actor_id,
            actor_name=actor_name,
            target_id=str(execution.rule_id) if execution.rule_id else None,
            target_name=None,
            details={
                "rule_trigger_name": rule_trigger_name,
                "action_type": str(execution.action.type),
                "channel_id": str(execution.channel_id) if execution.channel_id else None,
                "content": execution.content[:200] if execution.content else "",
                "matched_keyword": execution.matched_keyword,
            },
            occurred_at=datetime.now(timezone.utc),
        )

    # ── 감사 로그 ──

    @commands.Cog.listener()
    async def on_audit_log_entry_create(self, entry: discord.AuditLogEntry):
        if entry.guild is None:
            return

        actor_id = None
        actor_name = None
        if entry.user is not None:
            actor_id = str(entry.user.id)
            actor_name = str(entry.user)

        target_id = None
        if entry.target is not None:
            target_id = str(entry.target.id) if hasattr(entry.target, "id") else str(entry.target)

        # 변경사항 요약
        changes: dict = {}
        if entry.before:
            for attr in dir(entry.before):
                if attr.startswith("_"):
                    continue
                before_val = getattr(entry.before, attr, None)
                after_val = getattr(entry.after, attr, None) if entry.after else None
                if before_val != after_val:
                    changes[attr] = {
                        "before": str(before_val) if before_val is not None else None,
                        "after": str(after_val) if after_val is not None else None,
                    }

        await self.db.save_guild_event(
            event_type="audit_log",
            guild_id=str(entry.guild.id),
            actor_id=actor_id,
            actor_name=actor_name,
            target_id=target_id,
            target_name=None,
            details={
                "action": str(entry.action),
                "target_id": target_id,
                "reason": entry.reason,
                "changes": changes,
            },
            occurred_at=datetime.now(timezone.utc),
        )


async def setup(bot: commands.Bot):
    await bot.add_cog(ModerationCog(bot, bot.db))
