import discord
from discord.ext import commands
from datetime import datetime, timezone

from db.base import AbstractDatabase


def _user_str(user: discord.abc.User | None) -> tuple[str | None, str | None]:
    if user is None:
        return None, None
    return str(user.id), str(user)


class GuildEventsCog(commands.Cog):
    def __init__(self, bot: commands.Bot, db: AbstractDatabase):
        self.bot = bot
        self.db = db

    # ── 멤버 ──

    @commands.Cog.listener()
    async def on_member_join(self, member: discord.Member):
        await self.db.save_guild_event(
            event_type="member_join",
            guild_id=str(member.guild.id),
            actor_id=str(member.id),
            actor_name=str(member),
            target_id=None,
            target_name=None,
            details={"account_created_at": member.created_at.isoformat()},
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_member_remove(self, member: discord.Member):
        await self.db.save_guild_event(
            event_type="member_leave",
            guild_id=str(member.guild.id),
            actor_id=str(member.id),
            actor_name=str(member),
            target_id=None,
            target_name=None,
            details={},
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_member_ban(self, guild: discord.Guild, user: discord.User):
        await self.db.save_guild_event(
            event_type="member_ban",
            guild_id=str(guild.id),
            actor_id=None,
            actor_name=None,
            target_id=str(user.id),
            target_name=str(user),
            details={},
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_member_unban(self, guild: discord.Guild, user: discord.User):
        await self.db.save_guild_event(
            event_type="member_unban",
            guild_id=str(guild.id),
            actor_id=None,
            actor_name=None,
            target_id=str(user.id),
            target_name=str(user),
            details={},
            occurred_at=datetime.now(timezone.utc),
        )

    # ── 채널 ──

    @commands.Cog.listener()
    async def on_guild_channel_create(self, channel: discord.abc.GuildChannel):
        await self.db.save_guild_event(
            event_type="channel_create",
            guild_id=str(channel.guild.id),
            actor_id=None,
            actor_name=None,
            target_id=str(channel.id),
            target_name=channel.name,
            details={"channel_type": str(channel.type)},
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_guild_channel_delete(self, channel: discord.abc.GuildChannel):
        await self.db.save_guild_event(
            event_type="channel_delete",
            guild_id=str(channel.guild.id),
            actor_id=None,
            actor_name=None,
            target_id=str(channel.id),
            target_name=channel.name,
            details={"channel_type": str(channel.type)},
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_guild_channel_update(
        self,
        before: discord.abc.GuildChannel,
        after: discord.abc.GuildChannel,
    ):
        changes: dict = {}
        if before.name != after.name:
            changes["name"] = {"before": before.name, "after": after.name}
        if isinstance(before, discord.TextChannel) and isinstance(after, discord.TextChannel):
            if before.topic != after.topic:
                changes["topic"] = {"before": before.topic, "after": after.topic}
            if before.slowmode_delay != after.slowmode_delay:
                changes["slowmode_delay"] = {
                    "before": before.slowmode_delay,
                    "after": after.slowmode_delay,
                }
            if before.nsfw != after.nsfw:
                changes["nsfw"] = {"before": before.nsfw, "after": after.nsfw}

        if not changes:
            return

        await self.db.save_guild_event(
            event_type="channel_update",
            guild_id=str(before.guild.id),
            actor_id=None,
            actor_name=None,
            target_id=str(before.id),
            target_name=after.name,
            details={"changes": changes},
            occurred_at=datetime.now(timezone.utc),
        )

    # ── 서버 설정 ──

    @commands.Cog.listener()
    async def on_guild_update(self, before: discord.Guild, after: discord.Guild):
        changes: dict = {}
        if before.name != after.name:
            changes["name"] = {"before": before.name, "after": after.name}
        if before.description != after.description:
            changes["description"] = {"before": before.description, "after": after.description}
        if before.verification_level != after.verification_level:
            changes["verification_level"] = {
                "before": str(before.verification_level),
                "after": str(after.verification_level),
            }
        if before.explicit_content_filter != after.explicit_content_filter:
            changes["explicit_content_filter"] = {
                "before": str(before.explicit_content_filter),
                "after": str(after.explicit_content_filter),
            }
        if before.default_notifications != after.default_notifications:
            changes["default_notifications"] = {
                "before": str(before.default_notifications),
                "after": str(after.default_notifications),
            }

        if not changes:
            return

        await self.db.save_guild_event(
            event_type="guild_update",
            guild_id=str(before.id),
            actor_id=None,
            actor_name=None,
            target_id=str(before.id),
            target_name=after.name,
            details={"changes": changes},
            occurred_at=datetime.now(timezone.utc),
        )

    # ── 역할 ──

    @commands.Cog.listener()
    async def on_guild_role_create(self, role: discord.Role):
        await self.db.save_guild_event(
            event_type="role_create",
            guild_id=str(role.guild.id),
            actor_id=None,
            actor_name=None,
            target_id=str(role.id),
            target_name=role.name,
            details={},
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_guild_role_delete(self, role: discord.Role):
        await self.db.save_guild_event(
            event_type="role_delete",
            guild_id=str(role.guild.id),
            actor_id=None,
            actor_name=None,
            target_id=str(role.id),
            target_name=role.name,
            details={},
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_guild_role_update(self, before: discord.Role, after: discord.Role):
        changes: dict = {}
        if before.name != after.name:
            changes["name"] = {"before": before.name, "after": after.name}
        if before.colour != after.colour:
            changes["colour"] = {"before": str(before.colour), "after": str(after.colour)}
        if before.hoist != after.hoist:
            changes["hoist"] = {"before": before.hoist, "after": after.hoist}
        if before.mentionable != after.mentionable:
            changes["mentionable"] = {"before": before.mentionable, "after": after.mentionable}
        if before.permissions != after.permissions:
            changes["permissions"] = {
                "before": before.permissions.value,
                "after": after.permissions.value,
            }

        if not changes:
            return

        await self.db.save_guild_event(
            event_type="role_update",
            guild_id=str(before.guild.id),
            actor_id=None,
            actor_name=None,
            target_id=str(before.id),
            target_name=after.name,
            details={"changes": changes},
            occurred_at=datetime.now(timezone.utc),
        )

    # ── 멤버 업데이트 ──

    @commands.Cog.listener()
    async def on_member_update(self, before: discord.Member, after: discord.Member):
        changes: dict = {}
        if before.nick != after.nick:
            changes["nick"] = {"before": before.nick, "after": after.nick}

        before_roles = {r.id for r in before.roles}
        after_roles = {r.id for r in after.roles}
        added_roles = after_roles - before_roles
        removed_roles = before_roles - after_roles
        if added_roles or removed_roles:
            changes["roles"] = {
                "added": [
                    {"id": str(r.id), "name": r.name}
                    for r in after.roles if r.id in added_roles
                ],
                "removed": [
                    {"id": str(r.id), "name": r.name}
                    for r in before.roles if r.id in removed_roles
                ],
            }

        if before.timed_out_until != after.timed_out_until:
            changes["timed_out_until"] = {
                "before": before.timed_out_until.isoformat() if before.timed_out_until else None,
                "after": after.timed_out_until.isoformat() if after.timed_out_until else None,
            }

        if not changes:
            return

        await self.db.save_guild_event(
            event_type="member_update",
            guild_id=str(before.guild.id),
            actor_id=str(before.id),
            actor_name=str(before),
            target_id=None,
            target_name=None,
            details={"changes": changes},
            occurred_at=datetime.now(timezone.utc),
        )

    # ── 음성 채널 ──

    @commands.Cog.listener()
    async def on_voice_state_update(
        self,
        member: discord.Member,
        before: discord.VoiceState,
        after: discord.VoiceState,
    ):
        # 채널 이동이 없으면 (음소거/카메라 등 상태만 변경) 무시
        if before.channel == after.channel:
            return

        if after.channel is not None and before.channel is None:
            event_type = "voice_join"
            target_id = str(after.channel.id)
            target_name = after.channel.name
            details: dict = {}
        elif before.channel is not None and after.channel is None:
            event_type = "voice_leave"
            target_id = str(before.channel.id)
            target_name = before.channel.name
            details = {}
        else:
            # 채널 간 이동
            event_type = "voice_move"
            target_id = str(after.channel.id)  # type: ignore[union-attr]
            target_name = after.channel.name  # type: ignore[union-attr]
            details = {
                "from_channel_id": str(before.channel.id),  # type: ignore[union-attr]
                "from_channel_name": before.channel.name,  # type: ignore[union-attr]
            }

        await self.db.save_guild_event(
            event_type=event_type,
            guild_id=str(member.guild.id),
            actor_id=str(member.id),
            actor_name=str(member),
            target_id=target_id,
            target_name=target_name,
            details=details,
            occurred_at=datetime.now(timezone.utc),
        )

    # ── 스레드 ──

    @commands.Cog.listener()
    async def on_thread_create(self, thread: discord.Thread):
        owner_id = str(thread.owner_id) if thread.owner_id else None
        owner_name = str(thread.owner) if thread.owner else None
        await self.db.save_guild_event(
            event_type="thread_create",
            guild_id=str(thread.guild.id),
            actor_id=owner_id,
            actor_name=owner_name,
            target_id=str(thread.id),
            target_name=thread.name,
            details={
                "parent_id": str(thread.parent_id),
                "type": str(thread.type),
            },
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_thread_delete(self, thread: discord.Thread):
        await self.db.save_guild_event(
            event_type="thread_delete",
            guild_id=str(thread.guild.id),
            actor_id=None,
            actor_name=None,
            target_id=str(thread.id),
            target_name=thread.name,
            details={"parent_id": str(thread.parent_id)},
            occurred_at=datetime.now(timezone.utc),
        )

    # ── 반응 ──

    @commands.Cog.listener()
    async def on_reaction_add(self, reaction: discord.Reaction, user: discord.Member | discord.User):
        if getattr(user, "bot", False):
            return
        guild = getattr(reaction.message, "guild", None)
        if guild is None:
            return

        emoji = str(reaction.emoji)
        await self.db.save_guild_event(
            event_type="reaction_add",
            guild_id=str(guild.id),
            actor_id=str(user.id),
            actor_name=str(user),
            target_id=str(reaction.message.id),
            target_name=emoji,
            details={
                "channel_id": str(reaction.message.channel.id),
                "emoji": emoji,
                "message_id": str(reaction.message.id),
            },
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_reaction_remove(self, reaction: discord.Reaction, user: discord.Member | discord.User):
        if getattr(user, "bot", False):
            return
        guild = getattr(reaction.message, "guild", None)
        if guild is None:
            return

        emoji = str(reaction.emoji)
        await self.db.save_guild_event(
            event_type="reaction_remove",
            guild_id=str(guild.id),
            actor_id=str(user.id),
            actor_name=str(user),
            target_id=str(reaction.message.id),
            target_name=emoji,
            details={
                "channel_id": str(reaction.message.channel.id),
                "emoji": emoji,
                "message_id": str(reaction.message.id),
            },
            occurred_at=datetime.now(timezone.utc),
        )

    # ── 초대 링크 ──

    @commands.Cog.listener()
    async def on_invite_create(self, invite: discord.Invite):
        if invite.guild is None:
            return
        inviter_id = str(invite.inviter.id) if invite.inviter else None
        inviter_name = str(invite.inviter) if invite.inviter else None
        await self.db.save_guild_event(
            event_type="invite_create",
            guild_id=str(invite.guild.id),
            actor_id=inviter_id,
            actor_name=inviter_name,
            target_id=invite.code,
            target_name=invite.code,
            details={
                "channel_id": str(invite.channel.id) if invite.channel else None,
                "channel_name": invite.channel.name if invite.channel else None,
                "max_uses": invite.max_uses,
                "max_age": invite.max_age,
                "temporary": invite.temporary,
            },
            occurred_at=datetime.now(timezone.utc),
        )

    @commands.Cog.listener()
    async def on_invite_delete(self, invite: discord.Invite):
        if invite.guild is None:
            return
        await self.db.save_guild_event(
            event_type="invite_delete",
            guild_id=str(invite.guild.id),
            actor_id=None,
            actor_name=None,
            target_id=invite.code,
            target_name=invite.code,
            details={
                "channel_id": str(invite.channel.id) if invite.channel else None,
                "channel_name": invite.channel.name if invite.channel else None,
            },
            occurred_at=datetime.now(timezone.utc),
        )

    # ── 이모지 ──

    @commands.Cog.listener()
    async def on_guild_emojis_update(
        self,
        guild: discord.Guild,
        before: list[discord.Emoji],
        after: list[discord.Emoji],
    ):
        before_ids = {e.id: e for e in before}
        after_ids = {e.id: e for e in after}

        added = [e for e in after if e.id not in before_ids]
        removed = [e for e in before if e.id not in after_ids]

        if not added and not removed:
            return

        await self.db.save_guild_event(
            event_type="emojis_update",
            guild_id=str(guild.id),
            actor_id=None,
            actor_name=None,
            target_id=None,
            target_name=None,
            details={
                "added": [{"id": str(e.id), "name": e.name} for e in added],
                "removed": [{"id": str(e.id), "name": e.name} for e in removed],
            },
            occurred_at=datetime.now(timezone.utc),
        )


async def setup(bot: commands.Bot):
    await bot.add_cog(GuildEventsCog(bot, bot.db))
