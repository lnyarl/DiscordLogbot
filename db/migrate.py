"""자동 DB 마이그레이션 러너.

db/migrations/ 디렉토리의 .sql 파일을 순번대로 실행한다.
이미 실행된 마이그레이션은 _migrations 테이블에 기록되어 중복 실행되지 않는다.
"""

import os
import logging

log = logging.getLogger("logbot.migrate")

MIGRATIONS_DIR = os.path.join(os.path.dirname(__file__), "migrations")


async def run_migrations(pool) -> None:
    log.info("Checking migrations in %s", MIGRATIONS_DIR)

    async with pool.acquire() as conn:
        await conn.execute("""
            CREATE TABLE IF NOT EXISTS _migrations (
                name TEXT PRIMARY KEY,
                applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
            )
        """)

        applied = {
            row["name"]
            for row in await conn.fetch("SELECT name FROM _migrations")
        }

    if not os.path.isdir(MIGRATIONS_DIR):
        log.warning("Migrations directory not found: %s", MIGRATIONS_DIR)
        return

    sql_files = sorted(
        f for f in os.listdir(MIGRATIONS_DIR)
        if f.endswith(".sql")
    )

    log.info("Found %d migration(s), %d already applied", len(sql_files), len(applied))

    for filename in sql_files:
        if filename in applied:
            continue

        filepath = os.path.join(MIGRATIONS_DIR, filename)
        with open(filepath) as f:
            sql = f.read()

        async with pool.acquire() as conn:
            async with conn.transaction():
                # 주석 행 제거 후 statement 분리
                lines = [
                    line for line in sql.splitlines()
                    if line.strip() and not line.strip().startswith("--")
                ]
                clean_sql = "\n".join(lines)
                for statement in clean_sql.split(";"):
                    statement = statement.strip()
                    if not statement:
                        continue
                    log.info("Executing: %s", statement[:80])
                    await conn.execute(statement)
                await conn.execute(
                    "INSERT INTO _migrations (name) VALUES ($1)", filename
                )

        log.info("Migration applied: %s", filename)
