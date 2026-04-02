import os
import sys

import asyncpg
from dotenv import load_dotenv
from fastapi import FastAPI, Request
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

app = FastAPI(
    title="DiscordLogbot Search",
    docs_url=None,
    redoc_url=None,
    openapi_url=None,
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


@app.on_event("startup")
async def startup():
    app.state.pool = await asyncpg.create_pool(DATABASE_URL)
    async with app.state.pool.acquire() as conn:
        await conn.execute("CREATE EXTENSION IF NOT EXISTS pg_trgm")
        await conn.execute("""
            CREATE INDEX IF NOT EXISTS idx_messages_content_trgm
            ON messages USING GIN (content gin_trgm_ops)
        """)
    from db.migrate import run_migrations
    await run_migrations(app.state.pool)


@app.on_event("shutdown")
async def shutdown():
    await app.state.pool.close()


from web.auth import router as auth_router      # noqa: E402
from web.search import router as search_router  # noqa: E402

app.include_router(auth_router)
app.include_router(search_router)
