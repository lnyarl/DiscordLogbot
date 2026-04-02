---
name: discord-bot-coder
description: "디스코드 봇 코드를 구현한다. 커맨드 추가, 이벤트 핸들러 작성, 로깅 시스템 구현, 봇 설정 파일 생성 등 실제 코딩 작업에 반드시 이 스킬을 사용할 것."
---

# Discord Bot Coder

스펙 문서를 읽고 실제 동작하는 Discord 봇 코드를 작성한다.

## 시작 전 체크

1. `_workspace/01_plan_spec.md` 읽기
2. 프로젝트 루트의 기존 파일 확인 (Glob으로 탐색)
3. 기존 코드가 있으면 Read로 내용 파악 후 통합

## discord.py 기본 구조

```python
# bot.py
import discord
from discord.ext import commands
import os
from dotenv import load_dotenv

load_dotenv()

intents = discord.Intents.default()
intents.message_content = True  # Privileged Intent
intents.members = True           # Privileged Intent

bot = commands.Bot(command_prefix="!", intents=intents)

@bot.event
async def on_ready():
    print(f"Logged in as {bot.user}")
    try:
        synced = await bot.tree.sync()
        print(f"Synced {len(synced)} command(s)")
    except Exception as e:
        print(f"Failed to sync commands: {e}")

if __name__ == "__main__":
    bot.run(os.getenv("DISCORD_TOKEN"))
```

## Cog 구조 (기능 모듈화)

각 기능은 Cog으로 분리한다. 이유: Cog은 재로드 가능하고 코드 관리가 쉽다.

```python
# cogs/logging.py
import discord
from discord.ext import commands
from discord import app_commands
import json, os
from datetime import datetime, timezone

class LoggingCog(commands.Cog):
    def __init__(self, bot):
        self.bot = bot
        self.log_channels = {}  # {guild_id: channel_id}
        self._load_config()

    def _load_config(self):
        if os.path.exists("config.json"):
            with open("config.json") as f:
                data = json.load(f)
                self.log_channels = data.get("log_channels", {})

    def _save_config(self):
        with open("config.json", "w") as f:
            json.dump({"log_channels": self.log_channels}, f, indent=2)

    def _get_log_channel(self, guild_id: int):
        channel_id = self.log_channels.get(str(guild_id))
        if channel_id:
            return self.bot.get_channel(channel_id)
        return None

async def setup(bot):
    await bot.add_cog(LoggingCog(bot))
```

## 이벤트 핸들러 패턴

```python
@commands.Cog.listener()
async def on_message_delete(self, message: discord.Message):
    # 봇 메시지 제외
    if message.author.bot:
        return

    log_channel = self._get_log_channel(message.guild.id)
    if not log_channel:
        return

    embed = discord.Embed(
        title="메시지 삭제",
        color=discord.Color.red(),
        timestamp=datetime.now(timezone.utc)
    )
    embed.add_field(name="작성자", value=message.author.mention)
    embed.add_field(name="채널", value=message.channel.mention)
    embed.add_field(name="내용", value=message.content[:1024] or "내용 없음", inline=False)

    await log_channel.send(embed=embed)
```

## 슬래시 커맨드 패턴

```python
@app_commands.command(name="setlog", description="로그 채널을 설정합니다")
@app_commands.default_permissions(manage_guild=True)
async def set_log_channel(self, interaction: discord.Interaction, channel: discord.TextChannel):
    self.log_channels[str(interaction.guild_id)] = channel.id
    self._save_config()
    await interaction.response.send_message(f"로그 채널을 {channel.mention}으로 설정했습니다.", ephemeral=True)
```

## 환경변수 (.env)

```
DISCORD_TOKEN=your_bot_token_here
```

`.gitignore`에 `.env`를 반드시 포함한다.

## requirements.txt (discord.py)

```
discord.py>=2.3.0
python-dotenv>=1.0.0
```

## 구현 완료 후

`_workspace/02_code_summary.md`에 다음 내용 저장:
- 생성/수정된 파일 목록
- 각 파일의 주요 기능
- 미구현 항목 (있을 경우 이유 포함)
- 실행 방법 (`python bot.py` 등)

완료 후 discord-reviewer에게 SendMessage로 알린다.
