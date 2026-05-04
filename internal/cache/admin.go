// Package cache provides channel_access_cache invalidation helpers shared by
// the bot's gateway handlers and the web/MCP layer. Mirrors web/cache_admin.py:
// PostgreSQL-only, no Discord API dependency.
//
// Pattern:
//   - InvalidateUser(userID)        — drop one user's cache rows
//                                      (member leave/ban/role change)
//   - InvalidateGuild(guildID)      — drop every user that contained guildID
//                                      (role permissions, channel overwrite,
//                                      channel add/remove)
//   - InvalidateGuilds(guildIDs)    — bulk variant for re-IDENTIFY recovery
//
// Lazy fill on the next request recomputes the cache.
package cache

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Channel mirrors a row in channel_access_cache.channels (jsonb array).
// Same shape Python's permissions.compute_accessible_channels emits, so
// a row written by either implementation is read back identically.
type Channel struct {
	ChannelID    string `json:"channel_id"`
	ChannelName  string `json:"channel_name"`
	CategoryID   string `json:"category_id,omitempty"`
	CategoryName string `json:"category_name,omitempty"`
	GuildID      string `json:"guild_id"`
	GuildName    string `json:"guild_name"`
}

// CacheTTL — 6h safety net for missed invalidation events. Mirrors
// CACHE_TTL in web/permissions.py.
const CacheTTL = 6 * time.Hour

// Read returns the cached channel list for userID, or nil on miss/expired
// (not an error — caller should compute fresh and Write).
func Read(ctx context.Context, pool *pgxpool.Pool, userID string) ([]Channel, error) {
	var raw []byte
	err := pool.QueryRow(ctx, `
		SELECT channels FROM channel_access_cache
		 WHERE user_id = $1 AND expires_at > now()
	`, userID).Scan(&raw)
	if err != nil {
		if errIsNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Channel
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Write upserts userID's cache rows with the supplied channel list.
// guild_ids is derived for the GIN-backed invalidate_guild query.
func Write(ctx context.Context, pool *pgxpool.Pool, userID string, channels []Channel) error {
	guildSet := map[string]struct{}{}
	for _, c := range channels {
		guildSet[c.GuildID] = struct{}{}
	}
	guildIDs := make([]string, 0, len(guildSet))
	for id := range guildSet {
		guildIDs = append(guildIDs, id)
	}
	// Sort for deterministic output (Python sorts; useful for diffs in tests).
	sortStrings(guildIDs)

	payload, err := json.Marshal(channels)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO channel_access_cache
			(user_id, channels, guild_ids, computed_at, expires_at)
		VALUES ($1, $2::jsonb, $3, now(), now() + $4)
		ON CONFLICT (user_id) DO UPDATE SET
			channels    = EXCLUDED.channels,
			guild_ids   = EXCLUDED.guild_ids,
			computed_at = EXCLUDED.computed_at,
			expires_at  = EXCLUDED.expires_at
	`, userID, payload, guildIDs, CacheTTL)
	if err != nil {
		return err
	}
	slog.Info("cache written", "user_id", userID, "channels", len(channels), "guilds", len(guildIDs))
	return nil
}

func errIsNoRows(err error) bool {
	return err != nil && err == pgx.ErrNoRows
}

func sortStrings(s []string) {
	// Tiny n; insertion sort keeps the package free of "sort" import bloat.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// InvalidateUser drops every cache row for userID.
func InvalidateUser(ctx context.Context, pool *pgxpool.Pool, userID string) error {
	tag, err := pool.Exec(ctx,
		"DELETE FROM channel_access_cache WHERE user_id = $1", userID)
	if err != nil {
		return err
	}
	slog.Info("cache invalidated", "user_id", userID, "rows", tag.RowsAffected())
	return nil
}

// InvalidateGuild drops every cache row whose guild_ids array contains
// guildID. The GIN index idx_cac_guilds keeps this off a sequential scan.
func InvalidateGuild(ctx context.Context, pool *pgxpool.Pool, guildID string) error {
	tag, err := pool.Exec(ctx,
		"DELETE FROM channel_access_cache WHERE $1 = ANY(guild_ids)", guildID)
	if err != nil {
		return err
	}
	slog.Info("cache invalidated", "guild_id", guildID, "rows", tag.RowsAffected())
	return nil
}

// InvalidateGuilds drops cache rows touching any of the supplied guildIDs.
// Empty input is a no-op (mirrors Python's early return).
func InvalidateGuilds(ctx context.Context, pool *pgxpool.Pool, guildIDs []string) error {
	if len(guildIDs) == 0 {
		return nil
	}
	tag, err := pool.Exec(ctx,
		"DELETE FROM channel_access_cache WHERE guild_ids && $1::text[]", guildIDs)
	if err != nil {
		return err
	}
	slog.Info("cache invalidated", "guilds", len(guildIDs), "rows", tag.RowsAffected())
	return nil
}
