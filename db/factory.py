import os

from db.base import AbstractDatabase


def create_database() -> AbstractDatabase:
    """Create a database instance based on the DB_BACKEND environment variable.

    Supported backends:
        - "sqlite" (default): Uses SQLite via aiosqlite. Reads DB_PATH env var.
        - "postgresql": Uses PostgreSQL via asyncpg. Reads DATABASE_URL env var.
    """
    backend = os.getenv("DB_BACKEND", "sqlite").lower()

    if backend == "sqlite":
        from db.sqlite_db import SQLiteDatabase

        db_path = os.getenv("DB_PATH", "./data/logbot.db")
        return SQLiteDatabase(db_path)

    if backend == "postgresql":
        from db.postgresql_db import PostgreSQLDatabase

        database_url = os.getenv("DATABASE_URL")
        if not database_url:
            raise ValueError("DATABASE_URL environment variable is required for PostgreSQL backend")
        return PostgreSQLDatabase(database_url)

    raise ValueError(f"Unsupported DB_BACKEND: {backend!r}. Use 'sqlite' or 'postgresql'.")
