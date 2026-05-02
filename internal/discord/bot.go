// Package discord wires the discordgo gateway client to our DB layer
// and registers every event handler the Python cogs originally owned.
package discord

import (
	"context"
	"log/slog"

	"github.com/bwmarrin/discordgo"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lnyarl/discordlogbot/internal/attachments"
	"github.com/lnyarl/discordlogbot/internal/db"
)

// Intents is the union of every gateway intent any of our handlers
// (Phases 3 + 4) needs. Privileged intents (GuildMembers, MessageContent)
// must also be enabled in the Discord Developer Portal for the bot.
//
// Mirrors bot.py's intents = default() + members + message_content +
// reactions + invites + auto_moderation_* + guild_scheduled_events +
// moderation, all on top of discord.py's default set.
const Intents = discordgo.IntentGuilds |
	discordgo.IntentGuildMembers |
	discordgo.IntentGuildModeration |
	discordgo.IntentGuildEmojis |
	discordgo.IntentGuildIntegrations |
	discordgo.IntentGuildWebhooks |
	discordgo.IntentGuildInvites |
	discordgo.IntentGuildVoiceStates |
	discordgo.IntentGuildMessages |
	discordgo.IntentGuildMessageReactions |
	discordgo.IntentGuildMessageTyping |
	discordgo.IntentDirectMessages |
	discordgo.IntentDirectMessageReactions |
	discordgo.IntentDirectMessageTyping |
	discordgo.IntentMessageContent |
	discordgo.IntentGuildScheduledEvents |
	discordgo.IntentAutoModerationConfiguration |
	discordgo.IntentAutoModerationExecution

// Bot bundles the discordgo session with the side-car services every
// handler needs (DB pool, channel cache, pin cache, attachment downloader).
type Bot struct {
	Session    *discordgo.Session
	Pool       *pgxpool.Pool
	Channels   *db.LogChannelCache
	Pins       *PinCache
	Downloader *attachments.Downloader
}

// NewBot constructs a session with the production intent set and
// registers every Phase 3b handler. Call Open() to start the gateway.
func NewBot(token string, pool *pgxpool.Pool, dl *attachments.Downloader) (*Bot, error) {
	s, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}
	s.Identify.Intents = Intents
	s.StateEnabled = true

	b := &Bot{
		Session:    s,
		Pool:       pool,
		Channels:   db.NewLogChannelCache(),
		Pins:       newPinCache(),
		Downloader: dl,
	}
	b.registerHandlers()
	return b, nil
}

func (b *Bot) registerHandlers() {
	b.Session.AddHandler(b.onReady)
	b.Session.AddHandler(b.onMessageCreate)
	b.Session.AddHandler(b.onMessageUpdate)
	b.Session.AddHandler(b.onMessageDelete)
	b.Session.AddHandler(b.onMessageDeleteBulk)
	b.Session.AddHandler(b.onChannelPinsUpdate)
	b.Session.AddHandler(b.onInteractionCreate)
}

// Open opens the gateway connection. Returns the discordgo error verbatim.
func (b *Bot) Open() error  { return b.Session.Open() }
func (b *Bot) Close() error { return b.Session.Close() }

func (b *Bot) onReady(_ *discordgo.Session, r *discordgo.Ready) {
	slog.Info("bot connected",
		"user", r.User.Username, "id", r.User.ID, "guilds", len(r.Guilds))
	// Seed the pin cache off-thread so READY isn't blocked on N REST
	// calls. Each subsequent ChannelPinsUpdate then has a baseline to
	// diff against and won't be silently swallowed as "first observation".
	go b.seedAllPinsAt(context.Background())
}

// channelName looks up the channel name from the discordgo State; returns
// "" if the channel is unknown (e.g. event arrives before READY).
func (b *Bot) channelName(channelID string) string {
	if b.Session == nil || b.Session.State == nil {
		return ""
	}
	ch, err := b.Session.State.Channel(channelID)
	if err != nil || ch == nil {
		return ""
	}
	return ch.Name
}
