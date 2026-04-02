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

        # LoggingCog의 핀 캐시 초기화
        logging_cog = self.bot.get_cog("LoggingCog")
        if logging_cog:
            try:
                pinned = await channel.pins()
                logging_cog._pinned_cache[channel_id] = {str(m.id) for m in pinned}
            except Exception:
                logging_cog._pinned_cache[channel_id] = set()

        await interaction.response.send_message(
            f"{channel.mention} 채널을 로깅 대상에 추가했습니다.", ephemeral=True
        )

    @logbot_group.command(name="add_all", description="모든 공개 텍스트 채널을 로깅 대상에 추가합니다")
    async def logbot_add_all(self, interaction: discord.Interaction):
        guild = interaction.guild
        guild_id = str(guild.id)
        logging_cog = self.bot.get_cog("LoggingCog")
        count = 0

        for channel in guild.text_channels:
            # 비공개 채널(@everyone이 볼 수 없는 채널) 제외
            overwrites = channel.overwrites_for(guild.default_role)
            if overwrites.view_channel is False:
                continue

            channel_id = str(channel.id)
            await self.db.add_log_channel(guild_id, channel_id)

            if logging_cog:
                try:
                    pinned = await channel.pins()
                    logging_cog._pinned_cache[channel_id] = {str(m.id) for m in pinned}
                except Exception:
                    logging_cog._pinned_cache[channel_id] = set()
            count += 1

        await interaction.response.send_message(
            f"공개 텍스트 채널 {count}개를 로깅 대상에 추가했습니다.", ephemeral=True
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

    @logbot_group.command(name="search", description="로그에서 키워드를 검색합니다")
    @app_commands.describe(keyword="검색할 키워드")
    async def logbot_search(
        self, interaction: discord.Interaction, keyword: str
    ):
        guild_id = str(interaction.guild_id)
        results = await self.db.search_messages(guild_id, keyword, limit=20)

        if not results:
            await interaction.response.send_message(
                f"`{keyword}`에 대한 검색 결과가 없습니다.", ephemeral=True
            )
            return

        lines = []
        for r in results:
            content = r["content"]
            if len(content) > 80:
                content = content[:80] + "..."
            lines.append(
                f"**#{r['channel_name']}** @{r['author_name']} ({r['created_at'][:10]})\n> {content}"
            )

        # Discord 메시지 2000자 제한
        text = f"**`{keyword}` 검색 결과 ({len(results)}건):**\n\n" + "\n\n".join(lines)
        if len(text) > 2000:
            text = text[:1997] + "..."

        await interaction.response.send_message(text, ephemeral=True)

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
