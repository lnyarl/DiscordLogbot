package discord

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/lnyarl/discordlogbot/internal/db"
)

// ── Shadow caches for change diffs ───────────────────────────────────────
//
// discordgo doesn't ship BeforeUpdate for GuildScheduledEventUpdate or
// StageInstanceEventUpdate, and these don't live in discordgo's State, so
// we shadow them ourselves. Mirrors discord.py's State which feeds before
// to on_*_update.

type scheduledEventCache struct {
	mu     sync.RWMutex
	byID   map[string]*discordgo.GuildScheduledEvent // event ID → snapshot
}

func newScheduledEventCache() *scheduledEventCache {
	return &scheduledEventCache{byID: make(map[string]*discordgo.GuildScheduledEvent)}
}

func (c *scheduledEventCache) Get(id string) *discordgo.GuildScheduledEvent {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.byID[id]
}

func (c *scheduledEventCache) Set(e *discordgo.GuildScheduledEvent) {
	c.mu.Lock()
	c.byID[e.ID] = e
	c.mu.Unlock()
}

func (c *scheduledEventCache) Delete(id string) {
	c.mu.Lock()
	delete(c.byID, id)
	c.mu.Unlock()
}

type stageInstanceCache struct {
	mu   sync.RWMutex
	byID map[string]*discordgo.StageInstance
}

func newStageInstanceCache() *stageInstanceCache {
	return &stageInstanceCache{byID: make(map[string]*discordgo.StageInstance)}
}

func (c *stageInstanceCache) Get(id string) *discordgo.StageInstance {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.byID[id]
}

func (c *stageInstanceCache) Set(s *discordgo.StageInstance) {
	c.mu.Lock()
	c.byID[s.ID] = s
	c.mu.Unlock()
}

func (c *stageInstanceCache) Delete(id string) {
	c.mu.Lock()
	delete(c.byID, id)
	c.mu.Unlock()
}

// ── Scheduled events ─────────────────────────────────────────────────────

// scheduledEventLocation mirrors discord.py's ScheduledEvent.location:
// for EXTERNAL events it's entity_metadata.location; for stage/voice it's
// the channel name (or its ID if the channel isn't in State).
func (b *Bot) scheduledEventLocation(e *discordgo.GuildScheduledEvent) string {
	if e.EntityType == discordgo.GuildScheduledEventEntityTypeExternal {
		return e.EntityMetadata.Location
	}
	if e.ChannelID == "" {
		return ""
	}
	if name := b.channelName(e.ChannelID); name != "" {
		return name
	}
	return e.ChannelID
}

func scheduledEventCreator(e *discordgo.GuildScheduledEvent) (id, name string) {
	if e.CreatorID != "" {
		id = e.CreatorID
	}
	if e.Creator != nil {
		name = authorTag(e.Creator)
	}
	return
}

func (b *Bot) onScheduledEventCreate(_ *discordgo.Session, e *discordgo.GuildScheduledEventCreate) {
	if e.GuildID == "" {
		return
	}
	b.ScheduledEvents.Set(e.GuildScheduledEvent)
	actorID, actorName := scheduledEventCreator(e.GuildScheduledEvent)
	details := map[string]any{
		"event_name":  e.Name,
		"description": e.Description,
		"start_time":  isoformatPy(e.ScheduledStartTime),
		"end_time":    isoformatPyPtr(e.ScheduledEndTime),
		"location":    b.scheduledEventLocation(e.GuildScheduledEvent),
		"entity_type": entityTypeStr(e.EntityType),
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "scheduled_event_create",
		GuildID:    e.GuildID,
		ActorID:    actorID,
		ActorName:  actorName,
		TargetID:   e.ID,
		TargetName: e.Name,
		Details:    details,
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save scheduled_event_create", "err", err, "event_id", e.ID)
	}
}

// diffScheduledEvent compares two snapshots and returns the Python-shaped
// changes map. Pure function so tests can drive it directly.
func diffScheduledEvent(before, after *discordgo.GuildScheduledEvent, locFn func(*discordgo.GuildScheduledEvent) string) map[string]any {
	changes := map[string]any{}
	if before.Name != after.Name {
		changes["name"] = map[string]any{"before": before.Name, "after": after.Name}
	}
	if before.Description != after.Description {
		changes["description"] = map[string]any{"before": before.Description, "after": after.Description}
	}
	if !before.ScheduledStartTime.Equal(after.ScheduledStartTime) {
		changes["start_time"] = map[string]any{
			"before": isoformatPy(before.ScheduledStartTime),
			"after":  isoformatPy(after.ScheduledStartTime),
		}
	}
	if !timePtrEqual(before.ScheduledEndTime, after.ScheduledEndTime) {
		changes["end_time"] = map[string]any{
			"before": isoformatPyPtr(before.ScheduledEndTime),
			"after":  isoformatPyPtr(after.ScheduledEndTime),
		}
	}
	if before.Status != after.Status {
		changes["status"] = map[string]any{
			"before": eventStatusStr(before.Status),
			"after":  eventStatusStr(after.Status),
		}
	}
	bLoc := locFn(before)
	aLoc := locFn(after)
	if bLoc != aLoc {
		changes["location"] = map[string]any{"before": bLoc, "after": aLoc}
	}
	if before.EntityType != after.EntityType {
		changes["entity_type"] = map[string]any{
			"before": entityTypeStr(before.EntityType),
			"after":  entityTypeStr(after.EntityType),
		}
	}
	return changes
}

func timePtrEqual(a, b *time.Time) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(*b)
}

func (b *Bot) onScheduledEventUpdate(_ *discordgo.Session, e *discordgo.GuildScheduledEventUpdate) {
	if e.GuildID == "" {
		return
	}
	before := b.ScheduledEvents.Get(e.ID)
	b.ScheduledEvents.Set(e.GuildScheduledEvent)
	if before == nil {
		// First sighting (e.g. event existed before bot started and the
		// shadow seed missed it). Nothing meaningful to diff against —
		// match Python's "no changes → no event" behavior by skipping.
		return
	}
	changes := diffScheduledEvent(before, e.GuildScheduledEvent, b.scheduledEventLocation)
	if len(changes) == 0 {
		return
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "scheduled_event_update",
		GuildID:    e.GuildID,
		TargetID:   e.ID,
		TargetName: e.Name,
		Details:    map[string]any{"changes": changes},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save scheduled_event_update", "err", err, "event_id", e.ID)
	}
}

func (b *Bot) onScheduledEventDelete(_ *discordgo.Session, e *discordgo.GuildScheduledEventDelete) {
	if e.GuildID == "" {
		return
	}
	b.ScheduledEvents.Delete(e.ID)
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "scheduled_event_delete",
		GuildID:    e.GuildID,
		TargetID:   e.ID,
		TargetName: e.Name,
		Details:    map[string]any{"event_name": e.Name},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save scheduled_event_delete", "err", err, "event_id", e.ID)
	}
}

func (b *Bot) onScheduledEventUserAdd(_ *discordgo.Session, e *discordgo.GuildScheduledEventUserAdd) {
	if e.GuildID == "" {
		return
	}
	name := b.userTagFromState(e.UserID)
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "scheduled_event_user_add",
		GuildID:    e.GuildID,
		ActorID:    e.UserID,
		ActorName:  name,
		TargetID:   e.GuildScheduledEventID,
		TargetName: b.scheduledEventName(e.GuildScheduledEventID),
		Details: map[string]any{
			"event_name": b.scheduledEventName(e.GuildScheduledEventID),
			"event_id":   e.GuildScheduledEventID,
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save scheduled_event_user_add", "err", err, "event_id", e.GuildScheduledEventID)
	}
}

func (b *Bot) onScheduledEventUserRemove(_ *discordgo.Session, e *discordgo.GuildScheduledEventUserRemove) {
	if e.GuildID == "" {
		return
	}
	name := b.userTagFromState(e.UserID)
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "scheduled_event_user_remove",
		GuildID:    e.GuildID,
		ActorID:    e.UserID,
		ActorName:  name,
		TargetID:   e.GuildScheduledEventID,
		TargetName: b.scheduledEventName(e.GuildScheduledEventID),
		Details: map[string]any{
			"event_name": b.scheduledEventName(e.GuildScheduledEventID),
			"event_id":   e.GuildScheduledEventID,
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save scheduled_event_user_remove", "err", err, "event_id", e.GuildScheduledEventID)
	}
}

func (b *Bot) scheduledEventName(id string) string {
	if e := b.ScheduledEvents.Get(id); e != nil {
		return e.Name
	}
	return ""
}

// userTagFromState resolves a user ID to "Name#1234" via discordgo State.
// State has no top-level User-by-ID map, so we scan our guilds until a
// Member matches — the same fallback discord.py uses when only the ID is
// known. Returns "" if the user isn't cached anywhere (row stores ID
// alone, mirroring Python's behavior on cache miss).
func (b *Bot) userTagFromState(userID string) string {
	if b.Session == nil || b.Session.State == nil || userID == "" {
		return ""
	}
	for _, g := range b.Session.State.Guilds {
		if m, err := b.Session.State.Member(g.ID, userID); err == nil && m != nil && m.User != nil {
			return authorTag(m.User)
		}
	}
	return ""
}

// ── Stage instances ──────────────────────────────────────────────────────

func (b *Bot) onStageInstanceCreate(_ *discordgo.Session, e *discordgo.StageInstanceEventCreate) {
	if e.GuildID == "" {
		return
	}
	b.StageInstances.Set(e.StageInstance)
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "stage_instance_create",
		GuildID:    e.GuildID,
		TargetID:   e.ID,
		TargetName: e.Topic,
		Details: map[string]any{
			"topic":         e.Topic,
			"channel_id":    e.ChannelID,
			"privacy_level": stagePrivacyLevelStr(e.PrivacyLevel),
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save stage_instance_create", "err", err, "stage_id", e.ID)
	}
}

func diffStageInstance(before, after *discordgo.StageInstance) map[string]any {
	changes := map[string]any{}
	if before.Topic != after.Topic {
		changes["topic"] = map[string]any{"before": before.Topic, "after": after.Topic}
	}
	if before.PrivacyLevel != after.PrivacyLevel {
		changes["privacy_level"] = map[string]any{
			"before": stagePrivacyLevelStr(before.PrivacyLevel),
			"after":  stagePrivacyLevelStr(after.PrivacyLevel),
		}
	}
	return changes
}

func (b *Bot) onStageInstanceUpdate(_ *discordgo.Session, e *discordgo.StageInstanceEventUpdate) {
	if e.GuildID == "" {
		return
	}
	before := b.StageInstances.Get(e.ID)
	b.StageInstances.Set(e.StageInstance)
	if before == nil {
		return
	}
	changes := diffStageInstance(before, e.StageInstance)
	if len(changes) == 0 {
		return
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "stage_instance_update",
		GuildID:    e.GuildID,
		TargetID:   e.ID,
		TargetName: e.Topic,
		Details:    map[string]any{"changes": changes},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save stage_instance_update", "err", err, "stage_id", e.ID)
	}
}

func (b *Bot) onStageInstanceDelete(_ *discordgo.Session, e *discordgo.StageInstanceEventDelete) {
	if e.GuildID == "" {
		return
	}
	b.StageInstances.Delete(e.ID)
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "stage_instance_delete",
		GuildID:    e.GuildID,
		TargetID:   e.ID,
		TargetName: e.Topic,
		Details: map[string]any{
			"topic":      e.Topic,
			"channel_id": e.ChannelID,
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save stage_instance_delete", "err", err, "stage_id", e.ID)
	}
}
