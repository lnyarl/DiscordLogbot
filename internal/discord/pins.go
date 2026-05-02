package discord

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/lnyarl/discordlogbot/internal/db"
)

// PinCache mirrors LoggingCog._pinned_cache: per-channel set of currently
// pinned message IDs. Required because ChannelPinsUpdate only signals
// "something changed" — the diff (which message was pinned/unpinned) is
// computed locally from cached previous state vs. a fresh REST fetch.
//
// Sync.RWMutex (not Python's GIL) because gateway events fan out to
// goroutines.
type PinCache struct {
	mu      sync.RWMutex
	byChan  map[string]map[string]struct{} // channelID → set of message IDs
}

func newPinCache() *PinCache {
	return &PinCache{byChan: make(map[string]map[string]struct{})}
}

// Get returns the cached pin set for channelID, and whether one exists.
// Mirrors Python's `prev = self._pinned_cache.get(channel_id)` semantics:
// the second return is false on first observation, which the diff logic
// uses to suppress events for the initial seed (we don't have a prior
// state to compare against).
func (p *PinCache) Get(channelID string) (map[string]struct{}, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.byChan[channelID]
	if !ok {
		return nil, false
	}
	// Defensive copy so callers iterating concurrently with another Set
	// don't race on the underlying map.
	out := make(map[string]struct{}, len(v))
	for k := range v {
		out[k] = struct{}{}
	}
	return out, true
}

// Set replaces (or installs) the pin set for channelID.
func (p *PinCache) Set(channelID string, ids map[string]struct{}) {
	p.mu.Lock()
	p.byChan[channelID] = ids
	p.mu.Unlock()
}

// pinDiff returns the IDs newly added to current vs. prev, and the IDs
// removed from prev vs. current. Pure function — easy to unit test.
type pinDiff struct {
	Added   []string
	Removed []string
}

func diffPins(prev, current map[string]struct{}) pinDiff {
	var d pinDiff
	for id := range current {
		if _, was := prev[id]; !was {
			d.Added = append(d.Added, id)
		}
	}
	for id := range prev {
		if _, still := current[id]; !still {
			d.Removed = append(d.Removed, id)
		}
	}
	return d
}

// firstRunes returns the first n runes of s, multibyte-safe. Used for
// logging/event payloads where Python's content[:200] would slice mid-glyph.
func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// onChannelPinsUpdate is the discordgo callback wrapper.
func (b *Bot) onChannelPinsUpdate(s *discordgo.Session, e *discordgo.ChannelPinsUpdate) {
	if err := b.processPinsUpdate(context.Background(), e); err != nil {
		slog.Error("processPinsUpdate failed", "err", err, "channel_id", e.ChannelID)
	}
}

// processPinsUpdate is the testable core. It mirrors LoggingCog
// .on_guild_channel_pins_update:
//
//	1) gate on channel-logged
//	2) fetch the current pinned set via REST (Forbidden → empty)
//	3) compare against the previous cached set
//	4) for the first observation: just seed the cache, emit nothing
//	5) for subsequent updates: emit message_pin / message_unpin events
//	   with content/author resolved from the fetched messages, the DB,
//	   or REST as fallback.
func (b *Bot) processPinsUpdate(ctx context.Context, e *discordgo.ChannelPinsUpdate) error {
	if e.GuildID == "" {
		return nil
	}
	logged, err := b.Channels.IsLogged(ctx, b.Pool, e.GuildID, e.ChannelID)
	if err != nil {
		return err
	}
	if !logged {
		return nil
	}

	pinned, err := b.Session.ChannelMessagesPinned(e.ChannelID)
	if err != nil {
		// Mirrors Python's discord.Forbidden branch: treat as "no pins
		// visible" rather than aborting; we still want to compute removals
		// against the previous state.
		slog.Warn("fetch pinned messages", "err", err, "channel_id", e.ChannelID)
		pinned = nil
	}
	currentIDs := make(map[string]struct{}, len(pinned))
	currentByID := make(map[string]*discordgo.Message, len(pinned))
	for _, m := range pinned {
		currentIDs[m.ID] = struct{}{}
		currentByID[m.ID] = m
	}

	prev, hadPrev := b.Pins.Get(e.ChannelID)
	b.Pins.Set(e.ChannelID, currentIDs)

	// First observation: nothing to compare against, just seed.
	if !hadPrev {
		return nil
	}

	channelName := b.channelName(e.ChannelID)
	now := time.Now()
	d := diffPins(prev, currentIDs)

	for _, id := range d.Added {
		var actorID, actorName, content string
		if m := currentByID[id]; m != nil {
			if m.Author != nil {
				actorID = m.Author.ID
				actorName = authorTag(m.Author)
			}
			content = m.Content
		}
		if err := db.SaveGuildEvent(ctx, b.Pool, db.GuildEventInput{
			EventType:  "message_pin",
			GuildID:    e.GuildID,
			ActorID:    actorID,
			ActorName:  actorName,
			TargetID:   id,
			TargetName: channelName,
			Details: map[string]any{
				"content":    content,
				"author":     actorName,
				"channel_id": e.ChannelID,
			},
			OccurredAt: now,
		}); err != nil {
			slog.Error("save message_pin", "err", err, "message_id", id)
		}
	}

	for _, id := range d.Removed {
		// 1. DB latest (cheapest, works for messages we logged previously)
		var content, authorName string
		if info, infoErr := db.GetLatestMessageInfo(ctx, b.Pool, id); infoErr == nil && info != nil {
			content = info.Content
			authorName = info.AuthorName
		}
		// 2. REST fallback if DB had nothing (very old / never-logged pins)
		if content == "" {
			if msg, fetchErr := b.Session.ChannelMessage(e.ChannelID, id); fetchErr == nil && msg != nil {
				content = firstRunes(msg.Content, 200)
				if msg.Author != nil {
					authorName = authorTag(msg.Author)
				}
			}
		}
		if err := db.SaveGuildEvent(ctx, b.Pool, db.GuildEventInput{
			EventType:  "message_unpin",
			GuildID:    e.GuildID,
			ActorName:  authorName,
			TargetID:   id,
			TargetName: channelName,
			Details: map[string]any{
				"content":    content,
				"channel_id": e.ChannelID,
			},
			OccurredAt: now,
		}); err != nil {
			slog.Error("save message_unpin", "err", err, "message_id", id)
		}
	}
	return nil
}

// seedChannelPins fetches the current pin list for one channel and stores
// it in the cache. Used by /logbot add and /logbot add_all so the FIRST
// pin update after registration emits real diffs (without this, the first
// real update would be swallowed as "no prior cache").
func (b *Bot) seedChannelPins(channelID string) {
	pinned, err := b.Session.ChannelMessagesPinned(channelID)
	if err != nil {
		// Forbidden / not-yet-readable: seed an empty set so subsequent
		// pins still produce diffs.
		b.Pins.Set(channelID, map[string]struct{}{})
		return
	}
	set := make(map[string]struct{}, len(pinned))
	for _, m := range pinned {
		set[m.ID] = struct{}{}
	}
	b.Pins.Set(channelID, set)
}

// seedAllPinsAt initializes the pin cache for every logged channel of every
// guild the bot is currently in. Called from onReady. Sequential to stay
// well under Discord's global REST limits (50 req/s); a heavy bot would
// want batching, but this matches the Python sequential `await
// channel.pins()` pattern.
func (b *Bot) seedAllPinsAt(ctx context.Context) {
	for _, g := range b.Session.State.Guilds {
		ids, err := db.GetLogChannels(ctx, b.Pool, g.ID)
		if err != nil {
			slog.Error("seed pins: GetLogChannels", "err", err, "guild_id", g.ID)
			continue
		}
		for _, chID := range ids {
			b.seedChannelPins(chID)
		}
	}
}
