package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/lnyarl/discordlogbot/internal/db"
)

// ── AutoMod rules ────────────────────────────────────────────────────────

func (b *Bot) onAutoModerationRuleCreate(_ *discordgo.Session, e *discordgo.AutoModerationRuleCreate) {
	if e.GuildID == "" {
		return
	}
	actorID, actorName := b.creatorTag(e.GuildID, e.CreatorID)
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "automod_rule_create",
		GuildID:    e.GuildID,
		ActorID:    actorID,
		ActorName:  actorName,
		TargetID:   e.ID,
		TargetName: e.Name,
		Details: map[string]any{
			"rule_name":    e.Name,
			"trigger_type": autoModTriggerTypeStr(e.TriggerType),
			"actions":      autoModActions(e.Actions),
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save automod_rule_create", "err", err, "rule_id", e.ID)
	}
}

func (b *Bot) onAutoModerationRuleUpdate(_ *discordgo.Session, e *discordgo.AutoModerationRuleUpdate) {
	if e.GuildID == "" {
		return
	}
	actorID, actorName := b.creatorTag(e.GuildID, e.CreatorID)
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "automod_rule_update",
		GuildID:    e.GuildID,
		ActorID:    actorID,
		ActorName:  actorName,
		TargetID:   e.ID,
		TargetName: e.Name,
		Details: map[string]any{
			"rule_name": e.Name,
			"rule_id":   e.ID,
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save automod_rule_update", "err", err, "rule_id", e.ID)
	}
}

func (b *Bot) onAutoModerationRuleDelete(_ *discordgo.Session, e *discordgo.AutoModerationRuleDelete) {
	if e.GuildID == "" {
		return
	}
	actorID, actorName := b.creatorTag(e.GuildID, e.CreatorID)
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "automod_rule_delete",
		GuildID:    e.GuildID,
		ActorID:    actorID,
		ActorName:  actorName,
		TargetID:   e.ID,
		TargetName: e.Name,
		Details: map[string]any{
			"rule_name": e.Name,
			"rule_id":   e.ID,
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save automod_rule_delete", "err", err, "rule_id", e.ID)
	}
}

func autoModActions(actions []discordgo.AutoModerationAction) []map[string]any {
	out := make([]map[string]any, 0, len(actions))
	for _, a := range actions {
		out = append(out, map[string]any{"type": autoModActionTypeStr(a.Type)})
	}
	return out
}

// ── AutoMod execution ────────────────────────────────────────────────────

// autoModTriggerName mirrors discord.py's `execution.rule_trigger_type.name`
// (e.g. AutoModRuleTriggerType.keyword.name = "keyword"). discordgo
// only ships the int — we map back so payloads remain identical. Includes
// values 5 (mention_spam) and 6 (member_profile) added by Discord after
// discordgo v0.29.0's named constants were defined.
func autoModTriggerName(t discordgo.AutoModerationRuleTriggerType) string {
	switch t {
	case discordgo.AutoModerationEventTriggerKeyword:
		return "keyword"
	case discordgo.AutoModerationEventTriggerHarmfulLink:
		return "harmful_link"
	case discordgo.AutoModerationEventTriggerSpam:
		return "spam"
	case discordgo.AutoModerationEventTriggerKeywordPreset:
		return "keyword_preset"
	case 5:
		return "mention_spam"
	case 6:
		return "member_profile"
	default:
		return ""
	}
}

func (b *Bot) onAutoModerationActionExecution(_ *discordgo.Session, e *discordgo.AutoModerationActionExecution) {
	if e.GuildID == "" {
		return
	}
	content := e.Content
	if len(content) > 200 {
		content = firstRunes(content, 200)
	}
	var actorID, actorName string
	if e.UserID != "" {
		actorID = e.UserID
		actorName = b.userTagFromState(e.UserID)
	}
	var targetID string
	if e.RuleID != "" {
		targetID = e.RuleID
	}
	var channelID any = nil
	if e.ChannelID != "" {
		channelID = e.ChannelID
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType: "automod_action",
		GuildID:   e.GuildID,
		ActorID:   actorID,
		ActorName: actorName,
		TargetID:  targetID,
		Details: map[string]any{
			"rule_trigger_name": autoModTriggerName(e.RuleTriggerType),
			"action_type":       autoModActionTypeStr(e.Action.Type),
			"channel_id":        channelID,
			"content":           content,
			"matched_keyword":   e.MatchedKeyword,
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save automod_action", "err", err, "rule_id", e.RuleID)
	}
}

// ── Audit log ────────────────────────────────────────────────────────────

// auditLogChanges converts discordgo's structured AuditLogChange list into
// the Python-shaped {key: {before, after}} map. This is actually MORE
// faithful than the Python cog's `dir(entry.before)` introspection —
// Discord already supplies the keys it knows about.
func auditLogChanges(changes []*discordgo.AuditLogChange) map[string]any {
	out := map[string]any{}
	for _, c := range changes {
		if c == nil || c.Key == nil {
			continue
		}
		out[string(*c.Key)] = map[string]any{
			"before": stringifyAuditValue(c.OldValue),
			"after":  stringifyAuditValue(c.NewValue),
		}
	}
	return out
}

// stringifyAuditValue mirrors `str(value) if value is not None else None`
// from the Python cog. Discord's audit-log change values are JSON
// primitives (string, bool, number) and arrays/objects of those — fmt.Sprint
// renders all of these the same way Python's str() would for the scalar
// cases the cog actually compares.
func stringifyAuditValue(v any) any {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "True"
		}
		return "False"
	case json.Number:
		return x.String()
	}
	return fmt.Sprint(v)
}

func (b *Bot) onGuildAuditLogEntryCreate(_ *discordgo.Session, e *discordgo.GuildAuditLogEntryCreate) {
	if e.GuildID == "" {
		return
	}
	var actorID, actorName string
	if e.UserID != "" {
		actorID = e.UserID
		actorName = b.userTagFromState(e.UserID)
	}
	var actionStr string
	if e.ActionType != nil {
		actionStr = auditLogActionStr(*e.ActionType)
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType: "audit_log",
		GuildID:   e.GuildID,
		ActorID:   actorID,
		ActorName: actorName,
		TargetID:  e.TargetID,
		Details: map[string]any{
			"action":    actionStr,
			"target_id": e.TargetID,
			"reason":    e.Reason,
			"changes":   auditLogChanges(e.Changes),
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save audit_log", "err", err, "entry_id", e.ID)
	}
}

// creatorTag resolves a creator user ID to a display tag via State, mirroring
// `str(rule.creator) if rule.creator else None`.
func (b *Bot) creatorTag(guildID, creatorID string) (id, name string) {
	if creatorID == "" {
		return "", ""
	}
	id = creatorID
	if b.Session == nil || b.Session.State == nil {
		return
	}
	if m, err := b.Session.State.Member(guildID, creatorID); err == nil && m != nil && m.User != nil {
		name = authorTag(m.User)
	}
	return
}
