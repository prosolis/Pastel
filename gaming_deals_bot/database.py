import logging
from datetime import datetime, timedelta, timezone

import aiosqlite

logger = logging.getLogger(__name__)

SCHEMA = """
CREATE TABLE IF NOT EXISTS posted_deals (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    title TEXT NOT NULL,
    posted_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS config (
    key TEXT PRIMARY KEY,
    value TEXT
);
"""


class Database:
    def __init__(self, path: str):
        self.path = path
        self._db: aiosqlite.Connection | None = None

    async def connect(self):
        self._db = await aiosqlite.connect(self.path)
        await self._db.executescript(SCHEMA)
        await self._db.commit()
        logger.info("Database initialized at %s", self.path)

    async def close(self):
        if self._db:
            await self._db.close()

    async def has_been_posted(self, deal_id: str) -> bool:
        assert self._db is not None
        cursor = await self._db.execute(
            "SELECT 1 FROM posted_deals WHERE id = ?", (deal_id,)
        )
        row = await cursor.fetchone()
        return row is not None

    async def mark_posted(self, deal_id: str, source: str, title: str):
        assert self._db is not None
        await self._db.execute(
            "INSERT OR IGNORE INTO posted_deals (id, source, title) VALUES (?, ?, ?)",
            (deal_id, source, title),
        )
        await self._db.commit()

    async def prune_old(self, days: int = 30):
        assert self._db is not None
        cutoff = datetime.now(timezone.utc) - timedelta(days=days)
        result = await self._db.execute(
            "DELETE FROM posted_deals WHERE posted_at < ?",
            (cutoff.isoformat(),),
        )
        await self._db.commit()
        if result.rowcount:
            logger.info("Pruned %d old deal records", result.rowcount)

    async def get_config(self, key: str) -> str | None:
        assert self._db is not None
        cursor = await self._db.execute(
            "SELECT value FROM config WHERE key = ?", (key,)
        )
        row = await cursor.fetchone()
        return row[0] if row else None

    async def set_config(self, key: str, value: str):
        assert self._db is not None
        await self._db.execute(
            "INSERT OR REPLACE INTO config (key, value) VALUES (?, ?)",
            (key, value),
        )
        await self._db.commit()
