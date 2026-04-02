import os
import sys

import asyncpg
from dotenv import load_dotenv
from fastapi import FastAPI
from fastapi.staticfiles import StaticFiles

load_dotenv()

DATABASE_URL = os.getenv("DATABASE_URL")
if not DATABASE_URL:
    sys.exit("오류: DATABASE_URL 환경변수가 설정되지 않았습니다.")

app = FastAPI(title="DiscordLogbot Search")
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
