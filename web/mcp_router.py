"""MCP over HTTP+SSE 서버.

AI 클라이언트가 Bearer access token으로 인증 후 PostgreSQL 데이터에 접근한다.
모든 쿼리는 JWT에 포함된 허용 채널 목록으로 필터링된다.
"""
import json
import re
from contextvars import ContextVar

import asyncpg
from fastapi import APIRouter, Depends, HTTPException, Request
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer
from jose import JWTError, jwt
from mcp.server import Server
from mcp.server.sse import SseServerTransport
from mcp.types import TextContent, Tool

from web.auth import JWT_ALGORITHM, JWT_SECRET

router = APIRouter(prefix="/mcp")
security = HTTPBearer()

# 연결별 상태 — tool 핸들러는 mcp.run() 컨텍스트 안에서 실행되므로 contextvar 사용
_channels_ctx: ContextVar[list[dict]] = ContextVar("mcp_channels")
_pool_ctx: ContextVar[asyncpg.Pool] = ContextVar("mcp_pool")

mcp = Server("discord-logbot")
sse_transport = SseServerTransport("/mcp/messages")

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


def _extract_channels(credentials: HTTPAuthorizationCredentials) -> list[dict]:
    return _validate_token(credentials).get("channels", [])


def _check_access(guild_id: str, channel_id: str | None = None) -> str | None:
    """접근 불가 시 오류 메시지 반환, 허용 시 None.

    - guild_id 체크: 해당 길드에 접근 가능한 채널이 하나 이상 있어야 통과.
      (길드 이벤트 조회 권한 = 그 길드 내 채널 접근 권한 보유로 정의)
    - channel_id가 None이면 채널 체크 스킵 (get_guild_events 등 채널 불필요 툴용).
    - channel_id가 빈 문자열이면 명시적 거부.
    - channel_id는 guild_id에 속한 채널인지도 교차 검증
      (guild_id=A, channel_id=B_from_guild_C 조합 차단).
    """
    channels = _channels_ctx.get()
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
            description="채널에서 키워드로 메시지 검색 (부분 일치)",
            inputSchema={
                "type": "object",
                "properties": {
                    "guild_id": {"type": "string", "description": "Discord 서버 ID"},
                    "channel_id": {"type": "string", "description": "Discord 채널 ID"},
                    "keyword": {"type": "string", "description": "검색할 키워드"},
                    "limit": {"type": "integer", "default": 100, "maximum": 500},
                },
                "required": ["guild_id", "channel_id", "keyword"],
            },
        ),
        Tool(
            name="get_messages",
            description="채널의 최근 메시지 조회",
            inputSchema={
                "type": "object",
                "properties": {
                    "guild_id": {"type": "string"},
                    "channel_id": {"type": "string"},
                    "limit": {"type": "integer", "default": 100, "maximum": 500},
                },
                "required": ["guild_id", "channel_id"],
            },
        ),
        Tool(
            name="get_guild_events",
            description="서버 이벤트(입퇴장, 밴, 역할 변경 등) 조회",
            inputSchema={
                "type": "object",
                "properties": {
                    "guild_id": {"type": "string"},
                    "event_type": {
                        "type": "string",
                        "description": "필터할 이벤트 타입 (생략 시 전체)",
                    },
                    "limit": {"type": "integer", "default": 50, "maximum": 200},
                },
                "required": ["guild_id"],
            },
        ),
    ]


# ── MCP 툴 핸들러 ────────────────────────────────────────────────────────────

@mcp.call_tool()
async def call_tool(name: str, arguments: dict) -> list[TextContent]:
    channels = _channels_ctx.get()
    pool = _pool_ctx.get()

    if name == "list_channels":
        return [TextContent(type="text", text=json.dumps(channels, ensure_ascii=False, indent=2))]

    guild_id = arguments.get("guild_id", "")
    # channel_id는 None(미전달)과 ""(빈 문자열 공격)을 구분해서 전달
    channel_id: str | None = arguments.get("channel_id")

    # search_messages / get_messages 는 channel_id 필수 — None이면 명시적 거부
    _CHANNEL_REQUIRED = {"search_messages", "get_messages"}
    if name in _CHANNEL_REQUIRED and channel_id is None:
        return [TextContent(type="text", text="channel_id가 필요합니다.")]

    err = _check_access(guild_id, channel_id)
    if err:
        return [TextContent(type="text", text=err)]

    rows: list = []
    async with pool.acquire() as conn:
        if name == "search_messages":
            keyword = arguments["keyword"]
            limit = min(int(arguments.get("limit", 100)), 500)
            escaped = keyword.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")
            rows = await conn.fetch(
                """
                SELECT channel_name, author_name, content, created_at
                FROM messages
                WHERE guild_id = $1 AND channel_id = $2
                  AND lower(content) LIKE lower($3) ESCAPE '\\'
                ORDER BY created_at DESC LIMIT $4
                """,
                guild_id, channel_id, f"%{escaped}%", limit,
            )

        elif name == "get_messages":
            limit = min(int(arguments.get("limit", 100)), 500)
            rows = await conn.fetch(
                """
                SELECT channel_name, author_name, content, created_at
                FROM messages
                WHERE guild_id = $1 AND channel_id = $2
                ORDER BY created_at DESC LIMIT $3
                """,
                guild_id, channel_id, limit,
            )

        elif name == "get_guild_events":
            limit = min(int(arguments.get("limit", 50)), 200)
            event_type = arguments.get("event_type")
            if event_type:
                rows = await conn.fetch(
                    """
                    SELECT event_type, actor_name, target_name, details, occurred_at
                    FROM guild_events
                    WHERE guild_id = $1 AND event_type = $2
                    ORDER BY occurred_at DESC LIMIT $3
                    """,
                    guild_id, event_type, limit,
                )
            else:
                rows = await conn.fetch(
                    """
                    SELECT event_type, actor_name, target_name, details, occurred_at
                    FROM guild_events
                    WHERE guild_id = $1
                    ORDER BY occurred_at DESC LIMIT $2
                    """,
                    guild_id, limit,
                )

        else:
            return [TextContent(type="text", text=f"알 수 없는 툴: {name}")]

    result = [dict(r) for r in rows]
    return [TextContent(type="text", text=json.dumps(result, ensure_ascii=False, indent=2))]


# ── FastAPI 엔드포인트 ───────────────────────────────────────────────────────

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
    channels: list[dict] = payload.get("channels", [])
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

    token_c = _channels_ctx.set(channels)
    token_p = _pool_ctx.set(pool)
    try:
        async with sse_transport.connect_sse(
            request.scope, request.receive, intercepting_send
        ) as streams:
            await mcp.run(streams[0], streams[1], mcp.create_initialization_options())
    finally:
        for sid in captured_sids:
            _session_owners.pop(sid, None)
        _channels_ctx.reset(token_c)
        _pool_ctx.reset(token_p)


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
