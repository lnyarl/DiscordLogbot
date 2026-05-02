package db

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// LogChannelCache mirrors PostgreSQLDatabase._logged_channels — a per-guild
// set of logged channel ids loaded lazily and invalidated on any mutation.
//
// Backed by sync.RWMutex (not Python's GIL) because the bot may serve
// concurrent gateway events on multiple goroutines.
type LogChannelCache struct {
	mu    sync.RWMutex
	cache map[string]map[string]struct{}
}

func NewLogChannelCache() *LogChannelCache {
	return &LogChannelCache{cache: make(map[string]map[string]struct{})}
}

// IsLogged returns whether (guildID, channelID) is registered. On cache miss
// it fetches the guild's full list and stores it for subsequent calls.
func (c *LogChannelCache) IsLogged(
	ctx context.Context, pool *pgxpool.Pool, guildID, channelID string,
) (bool, error) {
	c.mu.RLock()
	if set, ok := c.cache[guildID]; ok {
		_, in := set[channelID]
		c.mu.RUnlock()
		return in, nil
	}
	c.mu.RUnlock()

	ids, err := GetLogChannels(ctx, pool, guildID)
	if err != nil {
		return false, err
	}
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	c.mu.Lock()
	c.cache[guildID] = set
	c.mu.Unlock()
	_, in := set[channelID]
	return in, nil
}

// Invalidate drops the cached set for guildID so the next IsLogged refetches.
func (c *LogChannelCache) Invalidate(guildID string) {
	c.mu.Lock()
	delete(c.cache, guildID)
	c.mu.Unlock()
}

// AddLogChannel upserts a (guild, channel) entry with display names.
func AddLogChannel(
	ctx context.Context, pool *pgxpool.Pool,
	guildID, channelID, guildName, channelName string,
) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO log_channels (guild_id, channel_id, guild_name, channel_name)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (guild_id, channel_id) DO UPDATE
		   SET guild_name = $3, channel_name = $4
	`, guildID, channelID, guildName, channelName)
	return err
}

// RemoveLogChannel returns whether a row was actually removed (false = no-op).
func RemoveLogChannel(ctx context.Context, pool *pgxpool.Pool, guildID, channelID string) (bool, error) {
	tag, err := pool.Exec(ctx,
		"DELETE FROM log_channels WHERE guild_id = $1 AND channel_id = $2",
		guildID, channelID,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// GetLogChannels returns every channel id registered for the guild.
func GetLogChannels(ctx context.Context, pool *pgxpool.Pool, guildID string) ([]string, error) {
	rows, err := pool.Query(ctx, "SELECT channel_id FROM log_channels WHERE guild_id = $1", guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
