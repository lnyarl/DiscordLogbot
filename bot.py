import logging
import os
import sys

import discord
from discord.ext import commands
from dotenv import load_dotenv

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(name)s] %(message)s",
    datefmt="%H:%M:%S",
)

from db.factory import create_database

load_dotenv()

DISCORD_TOKEN = os.getenv("DISCORD_TOKEN")
if not DISCORD_TOKEN:
    sys.exit("오류: DISCORD_TOKEN 환경변수가 설정되지 않았습니다. .env 파일을 확인하세요.")

intents = discord.Intents.default()
intents.message_content = True
intents.members = True
intents.reactions = True
intents.invites = True
intents.auto_moderation_configuration = True
intents.auto_moderation_execution = True
intents.guild_scheduled_events = True
intents.moderation = True


class LogBot(commands.Bot):
    def __init__(self):
        super().__init__(command_prefix="!", intents=intents)
        self.db = create_database()

    async def setup_hook(self):
        await self.db.connect()
        await self.load_extension("cogs.logging_cog")
        await self.load_extension("cogs.admin_cog")
        await self.load_extension("cogs.guild_events_cog")
        await self.load_extension("cogs.moderation_cog")
        await self.load_extension("cogs.scheduled_events_cog")
        await self.load_extension("cogs.integration_cog")
        await self.load_extension("cogs.cache_invalidation_cog")
        self.tree.on_error = self._on_app_command_error
        try:
            synced = await self.tree.sync()
            print(f"Synced {len(synced)} command(s)")
        except Exception as e:
            print(f"Failed to sync commands: {e}")

    async def _on_app_command_error(self, interaction, error):
        logging.getLogger("logbot").exception("App command error", exc_info=error)
        if not interaction.response.is_done():
            await interaction.response.send_message("오류가 발생했습니다.", ephemeral=True)
        else:
            await interaction.followup.send("오류가 발생했습니다.", ephemeral=True)

    async def on_ready(self):
        print(f"Logged in as {self.user} (ID: {self.user.id})")
        print(f"Database backend: {os.getenv('DB_BACKEND', 'sqlite')}")

    async def close(self):
        await self.db.close()
        await super().close()


bot = LogBot()

if __name__ == "__main__":
    bot.run(DISCORD_TOKEN)
