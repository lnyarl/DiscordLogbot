import os
import sys
from contextlib import asynccontextmanager

import asyncpg
from dotenv import load_dotenv
from fastapi import FastAPI
from fastapi.responses import JSONResponse
from fastapi.staticfiles import StaticFiles
from slowapi import Limiter
from slowapi.util import get_remote_address
from slowapi.errors import RateLimitExceeded

load_dotenv()

DATABASE_URL = os.getenv("DATABASE_URL")
if not DATABASE_URL:
    sys.exit("오류: DATABASE_URL 환경변수가 설정되지 않았습니다.")

limiter = Limiter(key_func=get_remote_address)


@asynccontextmanager
async def lifespan(app: FastAPI):
    """앱 시작/종료 시점 리소스 관리.

    StreamableHTTPSessionManager가 내부 task group 활성화를 위해 .run() context를
    요구하므로, FastAPI의 lifespan 안에서 함께 진입한다.
    """
    app.state.pool = await asyncpg.create_pool(DATABASE_URL)
    async with app.state.pool.acquire() as conn:
        await conn.execute("CREATE EXTENSION IF NOT EXISTS pg_trgm")
        await conn.execute("""
            CREATE INDEX IF NOT EXISTS idx_messages_content_trgm
            ON messages USING GIN (content gin_trgm_ops)
        """)
    from db.migrate import run_migrations
    await run_migrations(app.state.pool)

    from web.mcp_router import streamable_manager
    async with streamable_manager.run():
        yield

    await app.state.pool.close()


app = FastAPI(
    title="DiscordLogbot Search",
    docs_url=None,
    redoc_url=None,
    openapi_url=None,
    lifespan=lifespan,
)
app.state.limiter = limiter
app.add_exception_handler(RateLimitExceeded, lambda req, exc: JSONResponse(
    {"error": "Too many requests"}, status_code=429,
))
app.mount("/static", StaticFiles(directory=os.path.join(os.path.dirname(__file__), "static")), name="static")

# 첨부파일·이모지 서빙 (bot 컨테이너가 디렉토리를 생성해야 함)
_data_base = os.path.join(os.path.dirname(__file__), "..", "data")
_attachments_dir = os.getenv("ATTACHMENTS_DIR", os.path.join(_data_base, "attachments"))
_emojis_dir = os.getenv("EMOJIS_DIR", os.path.join(_data_base, "emojis"))

for _dir in (_attachments_dir, _emojis_dir):
    if not os.path.isdir(_dir):
        sys.exit(f"오류: 디렉토리가 존재하지 않습니다: {_dir}\nbot 컨테이너가 먼저 기동되어야 합니다.")

app.mount("/attachments", StaticFiles(directory=_attachments_dir), name="attachments")
app.mount("/emojis", StaticFiles(directory=_emojis_dir), name="emojis")


from web.auth import router as auth_router, TEMPLATES as auth_templates      # noqa: E402
from web.search import router as search_router, TEMPLATES as search_templates  # noqa: E402
from web.oauth_server import router as oauth_router  # noqa: E402
from web.mcp_router import router as mcp_router      # noqa: E402

# 봇 초대 URL을 모든 템플릿에 글로벌로 주입
_client_id = os.getenv("DISCORD_CLIENT_ID", "")
_bot_invite_url = (
    f"https://discord.com/oauth2/authorize?client_id={_client_id}&permissions=66560&integration_type=0&scope=bot+applications.commands"
    if _client_id else ""
)
for _tpl in (auth_templates, search_templates):
    _tpl.env.globals["bot_invite_url"] = _bot_invite_url

app.include_router(auth_router)
app.include_router(search_router)
app.include_router(oauth_router)
app.include_router(mcp_router)
