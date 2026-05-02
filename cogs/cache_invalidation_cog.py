"""채널 권한 캐시 무효화 cog.

Discord 게이트웨이 이벤트로 channel_access_cache를 능동적으로 청소한다.
누락 시에도 6h TTL이 안전망이지만, 실시간 정합성을 위해 가능한 범위에서
이벤트 기반 무효화를 시도.

전략:
  - 멤버 단위 변경 (역할/탈퇴/밴) → 그 사용자의 캐시 행만 DELETE
  - 길드 단위 변경 (역할 권한, 채널 overwrite, 채널 추가/삭제)
    → 그 길드를 포함한 모든 사용자 캐시 DELETE
  - 게이트웨이 mid-session re-IDENTIFY → 봇이 속한 모든 길드 일괄 청소
    (이벤트 누락 가능 구간이 발생했음을 감지)

PostgreSQL이 아니면 (SQLite 등) self.pool이 None — 모든 핸들러 no-op.
"""
import logging

import discord
from discord.ext import commands

from db.base import AbstractDatabase
from web.cache_admin import invalidate_guild, invalidate_guilds, invalidate_user

log = logging.getLogger(__name__)


class CacheInvalidationCog(commands.Cog):
    def __init__(self, bot: commands.Bot, db: AbstractDatabase):
        self.bot = bot
        self.db = db
        # 게이트웨이 mid-session re-IDENTIFY 감지용. 첫 on_ready는 정상 시작이므로
        # 무효화 안 하고, 두 번째 이상의 on_ready만 누락 발생 신호로 본다.
        # on_resumed는 RESUME 성공이라 누락 없음 → 플래그 영향 없음.
        self._session_started = False

    @property
    def pool(self):
        """PostgreSQL pool 또는 None."""
        return getattr(self.db, "_pool", None)

    # ── 멤버 스코프 ────────────────────────────────────────────────────────

    @commands.Cog.listener()
    async def on_member_update(
        self, before: discord.Member, after: discord.Member
    ) -> None:
        # 역할 변경만 권한에 영향. nickname 등 다른 변경은 무시.
        if not self.pool:
            return
        if {r.id for r in before.roles} != {r.id for r in after.roles}:
            await invalidate_user(self.pool, str(after.id))

    @commands.Cog.listener()
    async def on_member_remove(self, member: discord.Member) -> None:
        if not self.pool:
            return
        await invalidate_user(self.pool, str(member.id))

    @commands.Cog.listener()
    async def on_member_ban(
        self, guild: discord.Guild, user: discord.User
    ) -> None:
        if not self.pool:
            return
        await invalidate_user(self.pool, str(user.id))

    # ── 길드 스코프 ────────────────────────────────────────────────────────

    @commands.Cog.listener()
    async def on_guild_role_update(
        self, before: discord.Role, after: discord.Role
    ) -> None:
        # 권한 비트 또는 역할 자체가 바뀌면 그 길드 모든 사용자 캐시 영향
        if not self.pool:
            return
        if before.permissions != after.permissions:
            await invalidate_guild(self.pool, str(after.guild.id))

    @commands.Cog.listener()
    async def on_guild_role_delete(self, role: discord.Role) -> None:
        if not self.pool:
            return
        await invalidate_guild(self.pool, str(role.guild.id))

    @commands.Cog.listener()
    async def on_guild_channel_create(
        self, channel: discord.abc.GuildChannel
    ) -> None:
        # 새 채널이 생기면 카테고리/역할 권한이 바로 적용 — 사용자 캐시도 갱신 필요
        if not self.pool:
            return
        await invalidate_guild(self.pool, str(channel.guild.id))

    @commands.Cog.listener()
    async def on_guild_channel_delete(
        self, channel: discord.abc.GuildChannel
    ) -> None:
        if not self.pool:
            return
        await invalidate_guild(self.pool, str(channel.guild.id))

    @commands.Cog.listener()
    async def on_guild_channel_update(
        self,
        before: discord.abc.GuildChannel,
        after: discord.abc.GuildChannel,
    ) -> None:
        # overwrite 변경 또는 카테고리 이동 시 그 길드 사용자 캐시 무효화
        if not self.pool:
            return
        before_overwrites = getattr(before, "overwrites", {})
        after_overwrites = getattr(after, "overwrites", {})
        if before_overwrites != after_overwrites:
            await invalidate_guild(self.pool, str(after.guild.id))
            return
        before_cat = getattr(before, "category_id", None)
        after_cat = getattr(after, "category_id", None)
        if before_cat != after_cat:
            await invalidate_guild(self.pool, str(after.guild.id))

    @commands.Cog.listener()
    async def on_guild_remove(self, guild: discord.Guild) -> None:
        # 봇이 그 길드를 떠나면 캐시 의미 없음
        if not self.pool:
            return
        await invalidate_guild(self.pool, str(guild.id))

    # ── 게이트웨이 단절 ────────────────────────────────────────────────────

    @commands.Cog.listener()
    async def on_resumed(self) -> None:
        # RESUME 성공 = 끊긴 동안의 이벤트 replay됨 → 무효화 불필요
        log.debug("게이트웨이 RESUME 성공 — 누락 없음")

    @commands.Cog.listener()
    async def on_ready(self) -> None:
        # 첫 on_ready는 정상 시작이므로 무효화 스킵 (캐시는 아직 신선).
        # 두 번째 이상의 on_ready = mid-session re-IDENTIFY = 이벤트 누락 발생
        # → 봇이 속한 모든 길드 청소
        if not self._session_started:
            self._session_started = True
            log.info("CacheInvalidation: 봇 첫 시작 — 캐시 보존")
            return

        if not self.pool:
            return
        guild_ids = [str(g.id) for g in self.bot.guilds]
        log.warning(
            "게이트웨이 re-IDENTIFY 감지 — %d개 길드 캐시 무효화",
            len(guild_ids),
        )
        await invalidate_guilds(self.pool, guild_ids)


async def setup(bot: commands.Bot) -> None:
    await bot.add_cog(CacheInvalidationCog(bot, bot.db))
