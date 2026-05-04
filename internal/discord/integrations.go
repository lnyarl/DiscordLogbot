package discord

import (
	"context"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/lnyarl/discordlogbot/internal/db"
)

// onGuildIntegrationsUpdate mirrors integration_cog.on_guild_integrations_update.
func (b *Bot) onGuildIntegrationsUpdate(_ *discordgo.Session, e *discordgo.GuildIntegrationsUpdate) {
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "integrations_update",
		GuildID:    e.GuildID,
		Details:    map[string]any{},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save integrations_update", "err", err, "guild_id", e.GuildID)
	}
}

func integrationDetails(i *discordgo.Integration) map[string]any {
	return map[string]any{
		"name":    i.Name,
		"type":    i.Type,
		"account": i.Account.Name,
	}
}

func (b *Bot) onIntegrationCreate(_ *discordgo.Session, e *discordgo.IntegrationCreate) {
	if e.GuildID == "" {
		return
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "integration_create",
		GuildID:    e.GuildID,
		TargetID:   e.ID,
		TargetName: e.Name,
		Details:    integrationDetails(e.Integration),
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save integration_create", "err", err, "integration_id", e.ID)
	}
}

func (b *Bot) onIntegrationUpdate(_ *discordgo.Session, e *discordgo.IntegrationUpdate) {
	if e.GuildID == "" {
		return
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "integration_update",
		GuildID:    e.GuildID,
		TargetID:   e.ID,
		TargetName: e.Name,
		Details:    integrationDetails(e.Integration),
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save integration_update", "err", err, "integration_id", e.ID)
	}
}

// onIntegrationDelete mirrors on_integration_delete with the discord.py
// RawIntegrationDeleteEvent — discordgo's IntegrationDelete carries only
// id/guild_id/application_id, no name/type/account, so those fields are
// absent from details (matching Python's getattr fallback to None).
func (b *Bot) onIntegrationDelete(_ *discordgo.Session, e *discordgo.IntegrationDelete) {
	if e.GuildID == "" {
		return
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType: "integration_delete",
		GuildID:   e.GuildID,
		TargetID:  e.ID,
		Details: map[string]any{
			"name":    nil,
			"type":    nil,
			"account": nil,
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save integration_delete", "err", err, "integration_id", e.ID)
	}
}

// onWebhooksUpdate mirrors integration_cog.on_webhooks_update.
func (b *Bot) onWebhooksUpdate(_ *discordgo.Session, e *discordgo.WebhooksUpdate) {
	if e.GuildID == "" {
		return
	}
	chName := b.channelName(e.ChannelID)
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "webhooks_update",
		GuildID:    e.GuildID,
		TargetID:   e.ChannelID,
		TargetName: chName,
		Details: map[string]any{
			"channel_id":   e.ChannelID,
			"channel_name": chName,
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save webhooks_update", "err", err, "channel_id", e.ChannelID)
	}
}
