package discord

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/bwmarrin/discordgo"

	"github.com/lnyarl/discordlogbot/internal/cache"
)

// readyTracker mirrors CacheInvalidationCog._session_started: the FIRST
// READY is the bot's normal startup (cache is fresh; nothing to do). A
// SECOND or later READY means a mid-session re-IDENTIFY — the gateway lost
// state and Discord is replaying from scratch, so events between the
// disconnect and the new READY may have been missed. Wipe every cached
// row for guilds we currently belong to so the next request lazily
// recomputes.
//
// on_resumed is the success case (no missed events) — it doesn't need a
// handler at all.
type readyTracker struct {
	started atomic.Bool
}

func newReadyTracker() *readyTracker { return &readyTracker{} }

// onReadyForInvalidation is registered alongside onReady so a re-IDENTIFY
// is detected without coupling pin seeding and cache invalidation.
func (b *Bot) onReadyForInvalidation(_ *discordgo.Session, r *discordgo.Ready) {
	if !b.ReadyOnce.started.CompareAndSwap(false, true) {
		// Subsequent READY — invalidate every guild the bot is in.
		ids := make([]string, 0, len(r.Guilds))
		for _, g := range r.Guilds {
			ids = append(ids, g.ID)
		}
		slog.Warn("gateway re-IDENTIFY detected; bulk-invalidating cache",
			"guilds", len(ids))
		if err := cache.InvalidateGuilds(context.Background(), b.Pool, ids); err != nil {
			slog.Error("re-IDENTIFY invalidation failed", "err", err)
		}
		return
	}
	slog.Info("CacheInvalidation: first READY — cache preserved")
}

// invalidateUser is the thin wrapper guild_events handlers call after a
// member-scope event (role change, leave, ban).
func (b *Bot) invalidateUser(userID string) {
	if err := cache.InvalidateUser(context.Background(), b.Pool, userID); err != nil {
		slog.Error("cache invalidate user", "err", err, "user_id", userID)
	}
}

// invalidateGuild is the wrapper for guild-scope events (role permission
// change, role delete, channel add/delete, channel overwrite change,
// bot leaving the guild).
func (b *Bot) invalidateGuild(guildID string) {
	if err := cache.InvalidateGuild(context.Background(), b.Pool, guildID); err != nil {
		slog.Error("cache invalidate guild", "err", err, "guild_id", guildID)
	}
}
