-- 사용자별 접근 가능 채널 캐시
--   봇이 Discord 게이트웨이 이벤트로 invalidate, 웹이 lazy fill 후 6h TTL
--   guild_ids 배열은 길드 단위 invalidate를 위한 GIN 인덱스용 비정규화 컬럼.
CREATE TABLE IF NOT EXISTS channel_access_cache (
    user_id     TEXT        PRIMARY KEY,
    channels    JSONB       NOT NULL,
    guild_ids   TEXT[]      NOT NULL,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_cac_expires ON channel_access_cache (expires_at);
CREATE INDEX IF NOT EXISTS idx_cac_guilds  ON channel_access_cache USING GIN (guild_ids);
