import discord
from discord import app_commands
from discord.ext import commands

from db.base import AbstractDatabase


class AdminCog(commands.Cog):
    def __init__(self, bot: commands.Bot, db: AbstractDatabase):
        self.bot = bot
        self.db = db

    logbot_group = app_commands.Group(
        name="logbot",
        description="로깅 봇 관리 커맨드",
        default_permissions=discord.Permissions(manage_guild=True),
        guild_only=True,
    )

    @logbot_group.command(name="add", description="로깅 대상 채널을 추가합니다")
    @app_commands.describe(channel="로깅할 텍스트 채널")
    async def logbot_add(
        self, interaction: discord.Interaction, channel: discord.TextChannel
    ):
        guild_id = str(interaction.guild_id)
        channel_id = str(channel.id)

        await self.db.add_log_channel(guild_id, channel_id)
        await interaction.response.send_message(
            f"{channel.mention} 채널을 로깅 대상에 추가했습니다.", ephemeral=True
        )

    @logbot_group.command(name="remove", description="로깅 대상 채널을 제거합니다")
    @app_commands.describe(channel="제거할 텍스트 채널")
    async def logbot_remove(
        self, interaction: discord.Interaction, channel: discord.TextChannel
    ):
        guild_id = str(interaction.guild_id)
        channel_id = str(channel.id)

        removed = await self.db.remove_log_channel(guild_id, channel_id)
        if removed:
            await interaction.response.send_message(
                f"{channel.mention} 채널을 로깅 대상에서 제거했습니다.", ephemeral=True
            )
        else:
            await interaction.response.send_message(
                f"{channel.mention} 채널은 로깅 대상에 없습니다.", ephemeral=True
            )

    @logbot_group.command(name="list", description="현재 로깅 대상 채널 목록을 조회합니다")
    async def logbot_list(self, interaction: discord.Interaction):
        guild_id = str(interaction.guild_id)
        channel_ids = await self.db.get_log_channels(guild_id)

        if not channel_ids:
            await interaction.response.send_message(
                "로깅 대상 채널이 없습니다. `/logbot add #채널`로 채널을 추가하세요.",
                ephemeral=True,
            )
            return

        lines = []
        for cid in channel_ids:
            ch = self.bot.get_channel(int(cid))
            if ch:
                lines.append(f"- {ch.mention}")
            else:
                lines.append(f"- (알 수 없는 채널: {cid})")

        await interaction.response.send_message(
            f"**로깅 대상 채널 ({len(lines)}개):**\n" + "\n".join(lines),
            ephemeral=True,
        )

    @logbot_group.command(name="status", description="봇 상태 및 총 로그 수를 조회합니다")
    async def logbot_status(self, interaction: discord.Interaction):
        guild_id = str(interaction.guild_id)
        count = await self.db.get_message_count(guild_id)
        channel_ids = await self.db.get_log_channels(guild_id)

        if channel_ids:
            target = f"{len(channel_ids)}개 지정 채널"
        else:
            target = "없음 (로깅 중단 상태)"

        embed = discord.Embed(title="Logbot 상태", color=discord.Color.blue())
        embed.add_field(name="총 로그 메시지 수", value=f"{count:,}개")
        embed.add_field(name="로깅 대상", value=target)
        embed.add_field(
            name="지연시간", value=f"{round(self.bot.latency * 1000)}ms"
        )

        await interaction.response.send_message(embed=embed, ephemeral=True)


async def setup(bot: commands.Bot):
    await bot.add_cog(AdminCog(bot, bot.db))
