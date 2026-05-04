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
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

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
