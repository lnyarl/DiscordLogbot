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
