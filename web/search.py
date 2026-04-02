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
async def list_channels(request: Request, session: str | None = Cookie(default=None)):
    payload = _get_session(session)
    if not payload:
        return JSONResponse({"error": "Unauthorized"}, status_code=401)

    # JWT의 채널 목록으로 접근 가능한 guild 파악
    jwt_channels = payload.get("channels", [])
    accessible_guild_ids = {ch["guild_id"] for ch in jwt_channels}

    if not accessible_guild_ids:
        return {"channels": []}

    # DB에서 실제 로깅 대상 채널 조회
    pool = request.app.state.pool
    async with pool.acquire() as conn:
        rows = await conn.fetch(
            "SELECT guild_id, channel_id FROM log_channels WHERE guild_id = ANY($1::text[])",
            list(accessible_guild_ids),
        )

    # guild_name 매핑 (JWT에서)
    guild_names = {ch["guild_id"]: ch["guild_name"] for ch in jwt_channels}

    # channel_name은 messages 테이블에서 조회하거나, DB에 없으면 channel_id 표시
    channel_ids = [row["channel_id"] for row in rows]
    channel_names = {}
    if channel_ids:
        async with pool.acquire() as conn:
            name_rows = await conn.fetch(
                """
                SELECT DISTINCT ON (channel_id) channel_id, channel_name
                FROM messages WHERE channel_id = ANY($1::text[])
                ORDER BY channel_id, id DESC
                """,
                channel_ids,
            )
            channel_names = {r["channel_id"]: r["channel_name"] for r in name_rows}

    channels = []
    for row in rows:
        channels.append({
            "channel_id": row["channel_id"],
            "channel_name": channel_names.get(row["channel_id"], row["channel_id"]),
            "guild_id": row["guild_id"],
            "guild_name": guild_names.get(row["guild_id"], row["guild_id"]),
        })

    return {"channels": channels}


EVENT_LABELS = {
    "member_join": "멤버 입장",
    "member_leave": "멤버 퇴장",
    "member_ban": "멤버 차단",
    "member_unban": "멤버 차단 해제",
    "member_update": "멤버 변경",
    "channel_create": "채널 생성",
    "channel_delete": "채널 삭제",
    "channel_update": "채널 변경",
    "guild_update": "서버 설정 변경",
    "role_create": "역할 생성",
    "role_delete": "역할 삭제",
    "role_update": "역할 변경",
    "voice_join": "음성 입장",
    "voice_leave": "음성 퇴장",
    "voice_move": "음성 이동",
    "thread_create": "스레드 생성",
    "thread_delete": "스레드 삭제",
    "reaction_add": "반응 추가",
    "reaction_remove": "반응 제거",
    "invite_create": "초대 생성",
    "invite_delete": "초대 삭제",
    "emojis_update": "이모지 변경",
    "channel_pins_update": "메시지 고정",
    "message_pin": "메시지 고정",
    "message_unpin": "메시지 고정 해제",
    "bulk_message_delete": "메시지 일괄 삭제",
    "reaction_clear": "반응 전체 제거",
    "reaction_clear_emoji": "반응 이모지 제거",
    "thread_update": "스레드 변경",
    "thread_member_join": "스레드 멤버 입장",
    "thread_member_remove": "스레드 멤버 퇴장",
    "stickers_update": "스티커 변경",
    "user_update": "유저 프로필 변경",
    "guild_join": "봇 서버 추가",
    "guild_remove": "봇 서버 제거",
    "voice_state_change": "음성 상태 변경",
    "automod_rule_create": "AutoMod 규칙 생성",
    "automod_rule_update": "AutoMod 규칙 수정",
    "automod_rule_delete": "AutoMod 규칙 삭제",
    "automod_action": "AutoMod 실행",
    "audit_log": "감사 로그",
    "scheduled_event_create": "예약 이벤트 생성",
    "scheduled_event_update": "예약 이벤트 수정",
    "scheduled_event_delete": "예약 이벤트 삭제",
    "scheduled_event_user_add": "예약 이벤트 참가",
    "scheduled_event_user_remove": "예약 이벤트 참가 취소",
    "stage_instance_create": "스테이지 시작",
    "stage_instance_update": "스테이지 변경",
    "stage_instance_delete": "스테이지 종료",
    "integrations_update": "연동 변경",
    "integration_create": "연동 추가",
    "integration_update": "연동 수정",
    "integration_delete": "연동 삭제",
    "webhooks_update": "웹훅 변경",
}

RECENT_LIMIT = 1000

# 각 message_id의 최신 행만 가져오는 서브쿼리
LATEST_MESSAGES = """
    (SELECT DISTINCT ON (message_id)
        message_id, guild_id, channel_id, channel_name,
        author_id, author_name, content, attachments, action, created_at
    FROM messages
    ORDER BY message_id, id DESC) AS m
"""



@router.get("/api/search")
async def search(
    request: Request,
    q: str = Query(default="", max_length=200),
    channel_id: str | None = Query(default=None),
    guild_id: str | None = Query(default=None),
    page: int = Query(default=1, ge=1),
    include_events: bool = Query(default=False),
    session: str | None = Cookie(default=None),
):
    payload = _get_session(session)
    if not payload:
        return JSONResponse({"error": "Unauthorized"}, status_code=401)

    # JWT에서 접근 가능한 길드 확인
    jwt_channels = payload.get("channels", [])
    accessible_guild_ids = {ch["guild_id"] for ch in jwt_channels}

    if not accessible_guild_ids:
        return {"results": [], "total": 0, "page": page, "pages": 0}

    # DB에서 로깅 대상 채널 조회 (유저가 접근 가능한 길드 한정)
    pool = request.app.state.pool
    async with pool.acquire() as conn:
        log_rows = await conn.fetch(
            "SELECT guild_id, channel_id FROM log_channels WHERE guild_id = ANY($1::text[])",
            list(accessible_guild_ids),
        )
    accessible_ids = {row["channel_id"] for row in log_rows}
    guild_map = {row["channel_id"]: row["guild_id"] for row in log_rows}

    if not accessible_ids:
        return {"results": [], "total": 0, "page": page, "pages": 0}

    # 필터 적용
    if channel_id:
        if channel_id not in accessible_ids:
            return JSONResponse({"error": "Forbidden"}, status_code=403)
        target_ids = [channel_id]
    elif guild_id:
        target_ids = [cid for cid, gid in guild_map.items() if gid == guild_id]
        if not target_ids:
            return {"results": [], "total": 0, "page": page, "pages": 0}
    else:
        target_ids = list(accessible_ids)

    offset = (page - 1) * PAGE_SIZE
    ch_map = {ch["channel_id"]: ch for ch in jwt_channels}

    target_guild_ids = list({guild_map[cid] for cid in target_ids if cid in guild_map})

    ALL_LIMIT = 100
    event_rows = []

    async with pool.acquire() as conn:
        if not q:
            # 빈 검색어: 최신 메시지 (최대 100개)
            if include_events and channel_id:
                ch_info = ch_map.get(channel_id, {})
                ch_guild_id = ch_info.get("guild_id", "")
                total = await conn.fetchval(
                    f"""
                    SELECT LEAST(cnt, $3) FROM (
                        SELECT (
                            SELECT COUNT(*) FROM {LATEST_MESSAGES} WHERE channel_id = $1
                        ) + (
                            SELECT COUNT(*) FROM guild_events
                            WHERE guild_id = $2 AND details::jsonb->>'channel_id' = $1
                        ) AS cnt
                    ) sub
                    """,
                    channel_id, ch_guild_id, RECENT_LIMIT,
                )
                rows = await conn.fetch(
                    f"""
                    SELECT * FROM (
                        SELECT
                            'message' AS type,
                            message_id, guild_id, channel_id, channel_name,
                            author_name, content, created_at AS ts,
                            action, NULL AS event_type, NULL AS target_name, NULL AS details
                        FROM {LATEST_MESSAGES}
                        WHERE channel_id = $1
                        UNION ALL
                        SELECT
                            'event' AS type,
                            NULL, guild_id, NULL, NULL,
                            actor_name, NULL, occurred_at AS ts,
                            NULL, event_type, target_name, details
                        FROM guild_events
                        WHERE guild_id = $2
                          AND details::jsonb->>'channel_id' = $1
                    ) combined
                    ORDER BY ts DESC
                    LIMIT $3 OFFSET $4
                    """,
                    channel_id, ch_guild_id, PAGE_SIZE, offset,
                )
                # UNION 결과에서 분리
                results = []
                for row in rows:
                    if row["type"] == "event":
                        results.append({
                            "type": "event",
                            "event_type": row["event_type"],
                            "event_label": EVENT_LABELS.get(row["event_type"], row["event_type"]),
                            "actor_name": row["author_name"],
                            "target_name": row["target_name"],
                            "details": row["details"],
                            "occurred_at": row["ts"],
                        })
                    else:
                        results.append({
                            "type": "message",
                            "message_id": row["message_id"],
                            "guild_id": row["guild_id"],
                            "channel_id": row["channel_id"],
                            "action": row["action"],
                            "guild_name": ch_info.get("guild_name", ""),
                            "channel_name": row["channel_name"],
                            "author_name": row["author_name"],
                            "content": row["content"],
                            "created_at": row["ts"],
                            "score": None,
                        })
                return {
                    "results": results,
                    "total": int(total),
                    "page": page,
                    "pages": math.ceil(int(total) / PAGE_SIZE) if total else 0,
                }
            else:
                total = await conn.fetchval(
                    f"SELECT LEAST(COUNT(*), $2) FROM {LATEST_MESSAGES} WHERE channel_id = ANY($1::text[])",
                    target_ids, ALL_LIMIT,
                )
                rows = await conn.fetch(
                    f"""
                    SELECT message_id, guild_id, channel_id, channel_name,
                           author_name, content, action, created_at, 1.0::float AS score
                    FROM {LATEST_MESSAGES}
                    WHERE channel_id = ANY($1::text[])
                    ORDER BY created_at DESC
                    LIMIT $2 OFFSET $3
                    """,
                    target_ids, PAGE_SIZE, offset,
                )
        else:
            use_trgm = len(q) >= 3
            escaped = q.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")
            like_q = f"%{escaped}%"

            if use_trgm:
                total = await conn.fetchval(
                    f"""
                    SELECT COUNT(*) FROM {LATEST_MESSAGES}
                    WHERE channel_id = ANY($1::text[])
                      AND content % $2
                    """,
                    target_ids, q,
                )
                rows = await conn.fetch(
                    f"""
                    SELECT message_id, guild_id, channel_id, channel_name,
                           author_name, content, action, created_at,
                           similarity(content, $2) AS score
                    FROM {LATEST_MESSAGES}
                    WHERE channel_id = ANY($1::text[])
                      AND content % $2
                    ORDER BY score DESC, created_at DESC
                    LIMIT $3 OFFSET $4
                    """,
                    target_ids, q, PAGE_SIZE, offset,
                )
            else:
                total = await conn.fetchval(
                    f"""
                    SELECT COUNT(*) FROM {LATEST_MESSAGES}
                    WHERE channel_id = ANY($1::text[])
                      AND content ILIKE $2 ESCAPE '\\\\'
                    """,
                    target_ids, like_q,
                )
                rows = await conn.fetch(
                    f"""
                    SELECT message_id, guild_id, channel_id, channel_name,
                           author_name, content, action, created_at,
                           1.0::float AS score
                    FROM {LATEST_MESSAGES}
                    WHERE channel_id = ANY($1::text[])
                      AND content ILIKE $2 ESCAPE '\\\\'
                    ORDER BY created_at DESC
                    LIMIT $3 OFFSET $4
                    """,
                    target_ids, like_q, PAGE_SIZE, offset,
                )

            if include_events:
                event_rows = await conn.fetch(
                    """
                    SELECT event_type, guild_id, actor_name, target_name, details, occurred_at
                    FROM guild_events
                    WHERE guild_id = ANY($1::text[])
                      AND details ILIKE $2 ESCAPE '\\'
                    ORDER BY occurred_at DESC
                    LIMIT $3
                    """,
                    target_guild_ids, like_q, PAGE_SIZE,
                )

    results = []
    for row in rows:
        ch_info = ch_map.get(row["channel_id"], {})
        results.append({
            "type": "message",
            "message_id": row["message_id"],
            "guild_id": row["guild_id"],
            "channel_id": row["channel_id"],
            "action": row["action"],
            "guild_name": ch_info.get("guild_name", ""),
            "channel_name": row["channel_name"],
            "author_name": row["author_name"],
            "content": row["content"],
            "created_at": row["created_at"],
            "score": round(row["score"], 3),
        })

    for row in event_rows:
        results.append({
            "type": "event",
            "event_type": row["event_type"],
            "event_label": EVENT_LABELS.get(row["event_type"], row["event_type"]),
            "actor_name": row["actor_name"],
            "target_name": row["target_name"],
            "details": row["details"],
            "occurred_at": row["occurred_at"],
        })

    combined_total = total + len(event_rows)
    return {
        "results": results,
        "total": combined_total,
        "page": page,
        "pages": math.ceil(combined_total / PAGE_SIZE) if combined_total else 0,
    }
