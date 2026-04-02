"""검색 API 및 UI."""
import math
import os

from fastapi import APIRouter, Cookie, Query, Request
from fastapi.responses import HTMLResponse, JSONResponse, RedirectResponse
from fastapi.templating import Jinja2Templates

from web.auth import decode_jwt

TEMPLATES = Jinja2Templates(directory=os.path.join(os.path.dirname(__file__), "templates"))

router = APIRouter()

PAGE_SIZE = 20


def _get_session(session: str | None) -> dict | None:
    if not session:
        return None
    return decode_jwt(session)


@router.get("/search", response_class=HTMLResponse)
async def search_page(request: Request, session: str | None = Cookie(default=None)):
    payload = _get_session(session)
    if not payload:
        return RedirectResponse("/")
    return TEMPLATES.TemplateResponse(
        request=request,
        name="search.html",
        context={"username": payload.get("username")},
    )


@router.get("/api/channels")
async def list_channels(session: str | None = Cookie(default=None)):
    payload = _get_session(session)
    if not payload:
        return JSONResponse({"error": "Unauthorized"}, status_code=401)

    channels = payload.get("channels", [])
    return {"channels": channels}


RECENT_LIMIT = 1000


@router.get("/api/recent")
async def recent_messages(
    request: Request,
    channel_id: str = Query(...),
    page: int = Query(default=1, ge=1),
    session: str | None = Cookie(default=None),
):
    payload = _get_session(session)
    if not payload:
        return JSONResponse({"error": "Unauthorized"}, status_code=401)

    accessible = payload.get("channels", [])
    accessible_ids = {ch["channel_id"] for ch in accessible}

    if channel_id not in accessible_ids:
        return JSONResponse({"error": "Forbidden"}, status_code=403)

    pool = request.app.state.pool
    offset = (page - 1) * PAGE_SIZE

    ch_map = {ch["channel_id"]: ch for ch in accessible}

    async with pool.acquire() as conn:
        total = await conn.fetchval(
            """
            SELECT LEAST(COUNT(*), $2) FROM messages
            WHERE channel_id = $1
            """,
            channel_id,
            RECENT_LIMIT,
        )

        rows = await conn.fetch(
            """
            SELECT message_id, guild_id, channel_id, channel_name,
                   author_name, content, created_at
            FROM messages
            WHERE channel_id = $1
            ORDER BY created_at DESC
            LIMIT $2 OFFSET $3
            """,
            channel_id,
            PAGE_SIZE,
            offset,
        )

    results = []
    for row in rows:
        ch_info = ch_map.get(row["channel_id"], {})
        results.append({
            "message_id": row["message_id"],
            "guild_name": ch_info.get("guild_name", ""),
            "channel_name": row["channel_name"],
            "author_name": row["author_name"],
            "content": row["content"],
            "created_at": row["created_at"],
            "score": None,
        })

    total = int(total)
    return {
        "results": results,
        "total": total,
        "page": page,
        "pages": math.ceil(total / PAGE_SIZE) if total else 0,
    }


@router.get("/api/search")
async def search(
    request: Request,
    q: str = Query(..., min_length=1, max_length=200),
    channel_id: str | None = Query(default=None),
    guild_id: str | None = Query(default=None),
    page: int = Query(default=1, ge=1),
    session: str | None = Cookie(default=None),
):
    payload = _get_session(session)
    if not payload:
        return JSONResponse({"error": "Unauthorized"}, status_code=401)

    # 접근 가능한 채널 ID 목록
    accessible = payload.get("channels", [])
    accessible_ids = {ch["channel_id"] for ch in accessible}

    if not accessible_ids:
        return {"results": [], "total": 0, "page": page, "pages": 0}

    # 필터 적용
    if channel_id:
        if channel_id not in accessible_ids:
            return JSONResponse({"error": "Forbidden"}, status_code=403)
        target_ids = [channel_id]
    elif guild_id:
        target_ids = [
            ch["channel_id"] for ch in accessible
            if ch["guild_id"] == guild_id
        ]
        if not target_ids:
            return {"results": [], "total": 0, "page": page, "pages": 0}
    else:
        target_ids = list(accessible_ids)

    pool = request.app.state.pool
    offset = (page - 1) * PAGE_SIZE

    # 짧은 검색어(1-2자)는 trigram이 생성되지 않으므로 ILIKE 폴백
    use_trgm = len(q) >= 3

    async with pool.acquire() as conn:
        if use_trgm:
            total = await conn.fetchval(
                """
                SELECT COUNT(*) FROM messages
                WHERE channel_id = ANY($1::text[])
                  AND content % $2
                """,
                target_ids,
                q,
            )
            rows = await conn.fetch(
                """
                SELECT
                    message_id, guild_id, channel_id, channel_name,
                    author_name, content, created_at,
                    similarity(content, $2) AS score
                FROM messages
                WHERE channel_id = ANY($1::text[])
                  AND content % $2
                ORDER BY score DESC, created_at DESC
                LIMIT $3 OFFSET $4
                """,
                target_ids,
                q,
                PAGE_SIZE,
                offset,
            )
        else:
            like_pattern = f"%{q}%"
            total = await conn.fetchval(
                """
                SELECT COUNT(*) FROM messages
                WHERE channel_id = ANY($1::text[])
                  AND content ILIKE $2
                """,
                target_ids,
                like_pattern,
            )
            rows = await conn.fetch(
                """
                SELECT
                    message_id, guild_id, channel_id, channel_name,
                    author_name, content, created_at,
                    1.0::float AS score
                FROM messages
                WHERE channel_id = ANY($1::text[])
                  AND content ILIKE $2
                ORDER BY created_at DESC
                LIMIT $3 OFFSET $4
                """,
                target_ids,
                like_pattern,
                PAGE_SIZE,
                offset,
            )

    # channel_name 매핑 (접근 가능 채널 정보로 guild_name 보강)
    ch_map = {ch["channel_id"]: ch for ch in accessible}

    results = []
    for row in rows:
        ch_info = ch_map.get(row["channel_id"], {})
        results.append({
            "message_id": row["message_id"],
            "guild_name": ch_info.get("guild_name", ""),
            "channel_name": row["channel_name"],
            "author_name": row["author_name"],
            "content": row["content"],
            "created_at": row["created_at"],
            "score": round(row["score"], 3),
        })

    return {
        "results": results,
        "total": total,
        "page": page,
        "pages": math.ceil(total / PAGE_SIZE) if total else 0,
    }
