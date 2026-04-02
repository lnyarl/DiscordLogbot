-- log_channels에 guild_name, channel_name 컬럼 추가
ALTER TABLE log_channels ADD COLUMN IF NOT EXISTS guild_name TEXT NOT NULL DEFAULT '';
ALTER TABLE log_channels ADD COLUMN IF NOT EXISTS channel_name TEXT NOT NULL DEFAULT '';
