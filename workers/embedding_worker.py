"""
임베딩 워커 — messages 테이블에서 embedding이 없는 행을 최신순으로 처리한다.

현재 메시지를 반드시 전부 포함하고, 남은 토큰 예산으로 같은 채널의
전후 메시지를 가까운 순서부터 채워 넣는다.
임베딩은 로컬 Ollama 서버에 위임한다.

실행:
    python -m workers.embedding_worker

환경변수:
    DATABASE_URL    PostgreSQL 연결 문자열
    OLLAMA_HOST     Ollama 서버 주소 (기본 http://localhost:11434)
    OLLAMA_MODEL    사용할 임베딩 모델 (기본 bge-m3)
    BATCH_SIZE      한 번에 처리할 메시지 수 (기본 32)
    CONCURRENCY     동시 Ollama 요청 수 (기본 4)
    POLL_INTERVAL   큐 비었을 때 재확인 간격(초) (기본 10)
    MAX_TOKENS      임베딩 입력 최대 토큰 수 (기본 8000)
    CONTEXT_BEFORE  이전 메시지 최대 수 (기본 10)
    CONTEXT_AFTER   이후 메시지 최대 수 (기본 5)
    MAX_GAP_HOURS   메시지 간 최대 허용 시간 간격 (기본 2)
"""

import asyncio
import logging
import os
import time
from datetime import datetime, timedelta, timezone

import asyncpg
from dotenv import load_dotenv
from ollama import AsyncClient
from pgvector.asyncpg import register_vector

load_dotenv()

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(message)s",
    datefmt="%H:%M:%S",
)
log = logging.getLogger("embedding_worker")

DATABASE_URL = os.environ["DATABASE_URL"]
OLLAMA_HOST = os.getenv("OLLAMA_HOST", "http://localhost:11434")
OLLAMA_MODEL = os.getenv("OLLAMA_MODEL", "bge-m3")
BATCH_SIZE = int(os.getenv("BATCH_SIZE", "32"))
CONCURRENCY = int(os.getenv("CONCURRENCY", "4"))
POLL_INTERVAL = int(os.getenv("POLL_INTERVAL", "10"))
MAX_TOKENS = int(os.getenv("MAX_TOKENS", "8000"))
CONTEXT_BEFORE = int(os.getenv("CONTEXT_BEFORE", "10"))
CONTEXT_AFTER = int(os.getenv("CONTEXT_AFTER", "5"))
MAX_GAP = timedelta(hours=float(os.getenv("MAX_GAP_HOURS", "2")))


def estimate_tokens(text: str) -> int:
    """한글/영문 혼용 텍스트의 토큰 수를 근사한다.
    한글 1자 ≈ 2토큰, ASCII 1자 ≈ 0.3토큰 기준으로 보수적으로 추정.
    """
    korean = sum(1 for c in text if "가" <= c <= "힣")
    other = len(text) - korean
    return int(korean * 2 + other * 0.4) + 1


def parse_dt(value: str) -> datetime:
    dt = datetime.fromisoformat(value)
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt


def trim_by_gap(
    messages: list[asyncpg.Record],
    anchor_dt: datetime,
) -> list[asyncpg.Record]:
    """메시지 간 시간 간격이 MAX_GAP 초과하는 지점에서 자른다."""
    result = []
    prev_dt = anchor_dt
    for msg in messages:
        msg_dt = parse_dt(msg["created_at"])
        if abs(msg_dt - prev_dt) > MAX_GAP:
            break
        result.append(msg)
        prev_dt = msg_dt
    return result


def build_context_text(
    current: asyncpg.Record,
    before: list[asyncpg.Record],
    after: list[asyncpg.Record],
) -> str:
    """현재 메시지를 우선 확보하고, 남은 토큰으로 전후 메시지를 가까운 것부터 채운다.

    before: 시간 역순 (직전 메시지가 index 0)
    after:  시간 순  (직후 메시지가 index 0)
    """
    def fmt(r: asyncpg.Record) -> str:
        return f"{r['author_name']}: {r['content']}"

    current_text = fmt(current)
    current_tokens = estimate_tokens(current_text)

    if current_tokens >= MAX_TOKENS:
        return current_text

    budget = MAX_TOKENS - current_tokens
    before_parts: list[str] = []
    after_parts: list[str] = []

    bi, ai = 0, 0
    while budget > 0 and (bi < len(before) or ai < len(after)):
        if bi < len(before):
            line = fmt(before[bi])
            cost = estimate_tokens(line) + 1  # +1 개행
            if cost <= budget:
                before_parts.append(line)
                budget -= cost
            bi += 1

        if ai < len(after) and budget > 0:
            line = fmt(after[ai])
            cost = estimate_tokens(line) + 1
            if cost <= budget:
                after_parts.append(line)
                budget -= cost
            ai += 1

    parts = list(reversed(before_parts)) + [current_text] + after_parts
    return "\n".join(parts)


async def fetch_batch(conn: asyncpg.Connection, limit: int) -> list[asyncpg.Record]:
    return await conn.fetch(
        """
        SELECT id, channel_id, author_name, content, created_at
        FROM messages
        WHERE embedding IS NULL
          AND action != 'delete'
          AND length(content) >= 1
        ORDER BY id DESC
        LIMIT $1
        """,
        limit,
    )


async def fetch_context(
    conn: asyncpg.Connection,
    channel_id: str,
    created_at: str,
) -> tuple[list[asyncpg.Record], list[asyncpg.Record]]:
    before, after = await asyncio.gather(
        conn.fetch(
            """
            SELECT author_name, content, created_at
            FROM messages
            WHERE channel_id = $1 AND created_at < $2
              AND action != 'delete' AND length(content) >= 1
            ORDER BY created_at DESC LIMIT $3
            """,
            channel_id, created_at, CONTEXT_BEFORE,
        ),
        conn.fetch(
            """
            SELECT author_name, content, created_at
            FROM messages
            WHERE channel_id = $1 AND created_at > $2
              AND action != 'delete' AND length(content) >= 1
            ORDER BY created_at ASC LIMIT $3
            """,
            channel_id, created_at, CONTEXT_AFTER,
        ),
    )
    anchor_dt = parse_dt(created_at)
    return trim_by_gap(list(before), anchor_dt), trim_by_gap(list(after), anchor_dt)


async def embed_one(
    client: AsyncClient,
    sem: asyncio.Semaphore,
    text: str,
) -> list[float]:
    async with sem:
        response = await client.embeddings(model=OLLAMA_MODEL, prompt=text)
        return response.embedding


async def save_embeddings(
    conn: asyncpg.Connection,
    rows: list[asyncpg.Record],
    embeddings: list[list[float]],
) -> None:
    await conn.executemany(
        "UPDATE messages SET embedding = $1 WHERE id = $2",
        [(emb, row["id"]) for row, emb in zip(rows, embeddings)],
    )


async def count_remaining(conn: asyncpg.Connection) -> int:
    row = await conn.fetchrow(
        "SELECT COUNT(*) AS cnt FROM messages WHERE embedding IS NULL AND action != 'delete'"
    )
    return row["cnt"]


async def run() -> None:
    client = AsyncClient(host=OLLAMA_HOST)
    sem = asyncio.Semaphore(CONCURRENCY)

    pool = await asyncpg.create_pool(DATABASE_URL)
    async with pool.acquire() as conn:
        await register_vector(conn)
        remaining = await count_remaining(conn)
    log.info("모델: %s | 대기 메시지: %d건", OLLAMA_MODEL, remaining)

    total_processed = 0

    async with pool.acquire() as conn:
        await register_vector(conn)

        while True:
            rows = await fetch_batch(conn, BATCH_SIZE)

            if not rows:
                log.info("처리할 메시지 없음. %d초 후 재확인...", POLL_INTERVAL)
                await asyncio.sleep(POLL_INTERVAL)
                continue

            # 컨텍스트 조합
            texts: list[str] = []
            for row in rows:
                before, after = await fetch_context(conn, row["channel_id"], row["created_at"])
                texts.append(build_context_text(row, before, after))

            # Ollama 병렬 임베딩
            t0 = time.perf_counter()
            embeddings = await asyncio.gather(
                *[embed_one(client, sem, text) for text in texts]
            )
            elapsed = time.perf_counter() - t0

            await save_embeddings(conn, rows, list(embeddings))

            total_processed += len(rows)
            log.info(
                "%d건 완료 (누적 %d건) | %.1f건/초",
                len(rows),
                total_processed,
                len(rows) / elapsed,
            )


if __name__ == "__main__":
    asyncio.run(run())
