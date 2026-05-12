"""MCP over HTTP+SSE 서버.

AI 클라이언트가 Bearer access token으로 인증 후 PostgreSQL 데이터에 접근한다.
권한은 token에 박지 않고 channel_access_cache 테이블에서 user_id로 조회한다 —
봇이 Discord 게이트웨이 이벤트로 무효화 시 즉시 반영, 누락 시 6h TTL이 자가 치료.
"""
import json
import re
from contextvars import ContextVar
from datetime import datetime, timezone

import asyncpg
from fastapi import APIRouter, Depends, HTTPException, Request
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer
from jose import JWTError, jwt
from mcp.server import Server
from mcp.server.sse import SseServerTransport
from mcp.server.streamable_http_manager import StreamableHTTPSessionManager
from mcp.types import TextContent, Tool
from starlette.responses import Response

from web.auth import JWT_ALGORITHM, JWT_SECRET
from web.permissions import get_or_compute_channels

router = APIRouter(prefix="/mcp")
security = HTTPBearer()

# 연결별 상태 — tool 핸들러는 mcp.run() 컨텍스트 안에서 실행되므로 contextvar 사용
_user_id_ctx: ContextVar[str] = ContextVar("mcp_user_id")
_pool_ctx: ContextVar[asyncpg.Pool] = ContextVar("mcp_pool")

mcp = Server("discord-logbot")
sse_transport = SseServerTransport("/mcp/messages")

# Streamable HTTP — 신규 transport. mcp-remote가 우선 시도하는 transport이며
# 단일 endpoint(/mcp)로 동작. main.py의 lifespan에서 .run() 컨텍스트로 진입해야
# 내부 task group이 활성화된다.
streamable_manager = StreamableHTTPSessionManager(mcp)

# session_id(UUID) → user_id 매핑: SSE 연결 수립 시 등록, 종료 시 정리
# POST /messages 요청이 해당 세션 소유자의 JWT인지 검증하는 데 사용
_session_owners: dict[str, str] = {}

# SSE endpoint 이벤트에서 session_id를 추출하기 위한 패턴
# MCP SDK가 보내는 형식: "event: endpoint\ndata: /mcp/messages?session_id=<UUID.hex>\n\n"
# UUID.hex = 32자 소문자 hex, 하이픈 없음 (str(uuid)의 36자 형식이 아님)
_SESSION_ID_RE = re.compile(rb"session_id=([a-f0-9]{32})")


# ── 인증 ────────────────────────────────────────────────────────────────────

def _validate_token(credentials: HTTPAuthorizationCredentials) -> dict:
    """JWT 검증 후 전체 페이로드 반환. 실패 시 401."""
    try:
        payload = jwt.decode(credentials.credentials, JWT_SECRET, algorithms=[JWT_ALGORITHM])
    except JWTError:
        raise HTTPException(401, "Invalid or expired token")
    if payload.get("type") != "mcp_access":
        raise HTTPException(401, "Invalid token type")
    return payload


def _check_access(
    channels: list[dict], guild_id: str, channel_id: str | None = None
) -> str | None:
    """접근 불가 시 오류 메시지 반환, 허용 시 None.

    - guild_id 체크: 해당 길드에 접근 가능한 채널이 하나 이상 있어야 통과.
      (길드 이벤트 조회 권한 = 그 길드 내 채널 접근 권한 보유로 정의)
    - channel_id가 None이면 채널 체크 스킵 (get_guild_events 등 채널 불필요 툴용).
    - channel_id가 빈 문자열이면 명시적 거부.
    - channel_id는 guild_id에 속한 채널인지도 교차 검증
      (guild_id=A, channel_id=B_from_guild_C 조합 차단).
    """
    # guild_id → 해당 길드에서 접근 가능한 channel_id 집합으로 매핑
    guild_channel_map: dict[str, set[str]] = {}
    for c in channels:
        guild_channel_map.setdefault(c["guild_id"], set()).add(c["channel_id"])

    if guild_id not in guild_channel_map:
        return "접근 거부: 해당 서버에 접근 권한이 없습니다."
    if channel_id is not None:
        if not channel_id or channel_id not in guild_channel_map[guild_id]:
            return "접근 거부: 해당 채널에 접근 권한이 없습니다."
    return None


# ── 시간 필터 유틸 ──────────────────────────────────────────────────────────

def _parse_iso_to_db_string(s: str | None) -> str | None:
    """ISO 8601 입력을 DB 저장 형식(UTC, +00:00 offset)으로 정규화.

    DB는 created_at/occurred_at을 text 타입으로 ISO 8601 string으로 저장한다.
    Lexicographic 비교가 시간순과 일치하려면 모든 값이 같은 timezone offset
    문자열 ('+00:00')을 가져야 하므로, 'Z' 접미사나 다른 offset이 들어와도
    UTC로 변환해 통일한다.

    Naive datetime은 UTC로 가정 (Discord 메시지/이벤트는 모두 UTC 저장).
    """
    if not s:
        return None
    try:
        dt = datetime.fromisoformat(s)
    except ValueError as e:
        raise ValueError(f"잘못된 ISO 8601 datetime: {s!r}") from e
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    else:
        dt = dt.astimezone(timezone.utc)
    return dt.isoformat()


_TIME_FILTER_SCHEMA = {
    "since": {
        "type": "string",
        "description": "이 시각 이후 (ISO 8601, 예: 2026-04-25T00:00:00Z). 생략 시 제한 없음.",
    },
    "until": {
        "type": "string",
        "description": "이 시각 이전 (ISO 8601, 예: 2026-05-01T00:00:00Z). 생략 시 제한 없음.",
    },
}


def _append_time_filter(
    conditions: list[str],
    params: list,
    column: str,
    since: str | None,
    until: str | None,
) -> None:
    """conditions/params에 since/until SQL 절을 인덱스 순서대로 추가."""
    if since is not None:
        params.append(since)
        conditions.append(f"{column} >= ${len(params)}")
    if until is not None:
        params.append(until)
        conditions.append(f"{column} <= ${len(params)}")


# ── MCP 툴 정의 ─────────────────────────────────────────────────────────────

@mcp.list_tools()
async def list_tools() -> list[Tool]:
    return [
        Tool(
            name="list_channels",
            description="접근 가능한 Discord 서버/채널 목록 반환",
            inputSchema={"type": "object", "properties": {}},
        ),
        Tool(
            name="search_messages",
            description="채널에서 키워드로 메시지 검색 (부분 일치). since/until로 기간 제한 가능.",
            inputSchema={
                "type": "object",
                "properties": {
                    "guild_id": {"type": "string", "description": "Discord 서버 ID"},
                    "channel_id": {"type": "string", "description": "Discord 채널 ID"},
                    "keyword": {"type": "string", "description": "검색할 키워드"},
                    "limit": {"type": "integer", "default": 100, "maximum": 500},
                    **_TIME_FILTER_SCHEMA,
                },
                "required": ["guild_id", "channel_id", "keyword"],
            },
        ),
        Tool(
            name="get_messages",
            description="채널의 메시지 조회. since/until 미지정 시 최신순.",
            inputSchema={
                "type": "object",
                "properties": {
                    "guild_id": {"type": "string"},
                    "channel_id": {"type": "string"},
                    "limit": {"type": "integer", "default": 100, "maximum": 500},
                    **_TIME_FILTER_SCHEMA,
                },
                "required": ["guild_id", "channel_id"],
            },
        ),
        Tool(
            name="get_guild_events",
            description="서버 이벤트(입퇴장, 밴, 역할 변경 등) 조회. since/until로 기간 제한 가능.",
            inputSchema={
                "type": "object",
                "properties": {
                    "guild_id": {"type": "string"},
                    "event_type": {
                        "type": "string",
                        "description": "필터할 이벤트 타입 (생략 시 전체)",
                    },
                    "limit": {"type": "integer", "default": 50, "maximum": 200},
                    **_TIME_FILTER_SCHEMA,
                },
                "required": ["guild_id"],
            },
        ),
    ]


# ── MCP 툴 핸들러 ────────────────────────────────────────────────────────────

@mcp.call_tool()
async def call_tool(name: str, arguments: dict) -> list[TextContent]:
    user_id = _user_id_ctx.get()
    pool = _pool_ctx.get()

    # 매 도구 호출마다 캐시 조회 — SSE 세션이 길어도 권한 변경 즉시 반영.
    # miss/expired 시 Discord API로 lazy fill (TTL 6h).
    channels = await get_or_compute_channels(pool, user_id)

    if name == "list_channels":
        return [TextContent(type="text", text=json.dumps(channels, ensure_ascii=False, indent=2))]

    guild_id = arguments.get("guild_id", "")
    # channel_id는 None(미전달)과 ""(빈 문자열 공격)을 구분해서 전달
    channel_id: str | None = arguments.get("channel_id")

    # search_messages / get_messages 는 channel_id 필수 — None이면 명시적 거부
    _CHANNEL_REQUIRED = {"search_messages", "get_messages"}
    if name in _CHANNEL_REQUIRED and channel_id is None:
        return [TextContent(type="text", text="channel_id가 필요합니다.")]

    err = _check_access(channels, guild_id, channel_id)
    if err:
        return [TextContent(type="text", text=err)]

    try:
        since = _parse_iso_to_db_string(arguments.get("since"))
        until = _parse_iso_to_db_string(arguments.get("until"))
    except ValueError as e:
        return [TextContent(type="text", text=str(e))]

    rows: list = []
    async with pool.acquire() as conn:
        if name == "search_messages":
            keyword = arguments["keyword"]
            limit = min(int(arguments.get("limit", 100)), 500)
            escaped = keyword.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")
            params: list = [guild_id, channel_id, f"%{escaped}%"]
            conditions = [
                "guild_id = $1",
                "channel_id = $2",
                "lower(content) LIKE lower($3) ESCAPE '\\'",
            ]
            _append_time_filter(conditions, params, "created_at", since, until)
            params.append(limit)
            rows = await conn.fetch(
                f"""
                SELECT channel_name, author_name, content, created_at
                FROM messages
                WHERE {' AND '.join(conditions)}
                ORDER BY created_at DESC LIMIT ${len(params)}
                """,
                *params,
            )

        elif name == "get_messages":
            limit = min(int(arguments.get("limit", 100)), 500)
            params = [guild_id, channel_id]
            conditions = ["guild_id = $1", "channel_id = $2"]
            _append_time_filter(conditions, params, "created_at", since, until)
            params.append(limit)
            rows = await conn.fetch(
                f"""
                SELECT channel_name, author_name, content, created_at
                FROM messages
                WHERE {' AND '.join(conditions)}
                ORDER BY created_at DESC LIMIT ${len(params)}
                """,
                *params,
            )

        elif name == "get_guild_events":
            limit = min(int(arguments.get("limit", 50)), 200)
            event_type = arguments.get("event_type")
            params = [guild_id]
            conditions = ["guild_id = $1"]
            if event_type:
                params.append(event_type)
                conditions.append(f"event_type = ${len(params)}")
            _append_time_filter(conditions, params, "occurred_at", since, until)
            params.append(limit)
            rows = await conn.fetch(
                f"""
                SELECT event_type, actor_name, target_name, details, occurred_at
                FROM guild_events
                WHERE {' AND '.join(conditions)}
                ORDER BY occurred_at DESC LIMIT ${len(params)}
                """,
                *params,
            )

        else:
            return [TextContent(type="text", text=f"알 수 없는 툴: {name}")]

    result = [dict(r) for r in rows]
    return [TextContent(type="text", text=json.dumps(result, ensure_ascii=False, indent=2))]


# ── FastAPI 엔드포인트 ───────────────────────────────────────────────────────

class _AlreadySentResponse(Response):
    """SSE transport / handle_post_message가 ASGI send에 직접 응답을 쓴 뒤,
    FastAPI 라우터가 두 번째 http.response.start를 보내려다
    'Unexpected ASGI message' RuntimeError를 내는 것을 막기 위한 sentinel.

    이 응답을 반환하면 FastAPI가 Response.__call__ 을 부르지만 no-op이라
    추가로 송출되는 ASGI 메시지가 없다.
    """

    def __init__(self) -> None:
        super().__init__(content=b"", status_code=200)

    async def __call__(self, scope, receive, send) -> None:  # type: ignore[override]
        return


@router.get("/sse")
async def mcp_sse(
    request: Request,
    credentials: HTTPAuthorizationCredentials = Depends(security),
):
    """SSE 연결 엔드포인트. JWT 검증 후 MCP 세션 시작.

    ASGI send를 가로채 MCP SDK가 전송하는 endpoint 이벤트에서
    session_id를 추출하여 _session_owners에 등록한다.
    """
    payload = _validate_token(credentials)
    user_id: str = payload["sub"]
    pool: asyncpg.Pool = request.app.state.pool

    captured_sids: list[str] = []
    original_send = request._send
    # session_id 탐색용 누적 버퍼: endpoint 이벤트가 여러 청크로 분할될 경우에 대비
    # session_id 발견 후 또는 2 KB 초과 시 탐색 중단
    _chunk_buf = bytearray()
    _sid_found = False

    async def intercepting_send(message: dict) -> None:
        """endpoint SSE 이벤트에서 session_id를 캡처해 소유자 매핑에 등록."""
        nonlocal _sid_found
        if not _sid_found and message.get("type") == "http.response.body":
            _chunk_buf.extend(message.get("body", b""))
            m = _SESSION_ID_RE.search(_chunk_buf)
            if m:
                sid = m.group(1).decode()
                captured_sids.append(sid)
                _session_owners[sid] = user_id
                _sid_found = True
            elif len(_chunk_buf) > 2048:
                # 2 KB 이내에 session_id가 없으면 탐색 포기 (정상 이벤트가 아님)
                _sid_found = True
        await original_send(message)

    token_u = _user_id_ctx.set(user_id)
    token_p = _pool_ctx.set(pool)
    try:
        async with sse_transport.connect_sse(
            request.scope, request.receive, intercepting_send
        ) as streams:
            await mcp.run(streams[0], streams[1], mcp.create_initialization_options())
    finally:
        for sid in captured_sids:
            _session_owners.pop(sid, None)
        _user_id_ctx.reset(token_u)
        _pool_ctx.reset(token_p)
    return _AlreadySentResponse()


@router.post("/messages")
async def mcp_messages(
    request: Request,
    credentials: HTTPAuthorizationCredentials = Depends(security),
):
    """MCP 클라이언트 → 서버 메시지 수신 엔드포인트.

    JWT 검증 + 세션 소유자 일치 검증으로 타인의 세션에 메시지 주입을 차단한다.
    """
    payload = _validate_token(credentials)
    user_id: str = payload["sub"]

    session_id = request.query_params.get("session_id", "")
    expected_owner = _session_owners.get(session_id)
    if expected_owner is None:
        raise HTTPException(404, "Session not found")
    if expected_owner != user_id:
        raise HTTPException(403, "Forbidden: session belongs to a different user")

    await sse_transport.handle_post_message(request.scope, request.receive, request._send)
    return _AlreadySentResponse()


# ── Streamable HTTP ─────────────────────────────────────────────────────────
# 신규 transport(/mcp 단일 endpoint). mcp-remote가 우선 시도하므로 추가만 해도
# 자동으로 활성화. SSE endpoint(/mcp/sse, /mcp/messages)는 호환성을 위해 그대로 유지.

async def _handle_streamable(request: Request) -> Response:
    """공통 핸들러: 토큰 검증 → 컨텍스트 세팅 → manager에 위임.

    StreamableHTTPSessionManager가 응답을 직접 ASGI send에 쓰므로 _AlreadySentResponse
    sentinel 반환.
    """
    auth = request.headers.get("authorization", "")
    if not auth.startswith("Bearer "):
        raise HTTPException(401, "Missing Bearer token")
    try:
        payload = jwt.decode(auth[7:], JWT_SECRET, algorithms=[JWT_ALGORITHM])
    except JWTError:
        raise HTTPException(401, "Invalid or expired token")
    if payload.get("type") != "mcp_access":
        raise HTTPException(401, "Invalid token type")

    user_id: str = payload["sub"]
    pool: asyncpg.Pool = request.app.state.pool

    token_u = _user_id_ctx.set(user_id)
    token_p = _pool_ctx.set(pool)
    try:
        await streamable_manager.handle_request(
            request.scope, request.receive, request._send,
        )
    finally:
        _user_id_ctx.reset(token_u)
        _pool_ctx.reset(token_p)
    return _AlreadySentResponse()


@router.post("")
async def mcp_streamable_post(request: Request) -> Response:
    return await _handle_streamable(request)


@router.get("")
async def mcp_streamable_get(request: Request) -> Response:
    # GET은 server-initiated 이벤트 스트림 수신용 (선택적, MCP 명세)
    return await _handle_streamable(request)


@router.delete("")
async def mcp_streamable_delete(request: Request) -> Response:
    # DELETE는 세션 종료 신호 (MCP 명세)
    return await _handle_streamable(request)
