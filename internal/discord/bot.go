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
// handler needs (DB pool, channel cache, pin cache, attachment downloader,
// per-entity shadow caches for diff-based update events, and re-IDENTIFY
// detection).
type Bot struct {
	Session         *discordgo.Session
	Pool            *pgxpool.Pool
	Channels        *db.LogChannelCache
	Pins            *PinCache
	Downloader      *attachments.Downloader
	Guilds          *guildShadow
	Roles           *roleShadow
	Emojis          *emojiShadow
	Stickers        *stickerShadow
	Users           *userShadow
	ScheduledEvents *scheduledEventCache
	StageInstances  *stageInstanceCache
	InitialGuilds   *initialGuildSet
	ReadyOnce       *readyTracker
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
		Session:         s,
		Pool:            pool,
		Channels:        db.NewLogChannelCache(),
		Pins:            newPinCache(),
		Downloader:      dl,
		Guilds:          newGuildShadow(),
		Roles:           newRoleShadow(),
		Emojis:          newEmojiShadow(),
		Stickers:        newStickerShadow(),
		Users:           newUserShadow(),
		ScheduledEvents: newScheduledEventCache(),
		StageInstances:  newStageInstanceCache(),
		InitialGuilds:   newInitialGuildSet(),
		ReadyOnce:       newReadyTracker(),
	}
	b.registerHandlers()
	return b, nil
}

func (b *Bot) registerHandlers() {
	// Phase 3 — messages, pins, slash commands.
	b.Session.AddHandler(b.onReady)
	b.Session.AddHandler(b.onReadyForInvalidation)
	b.Session.AddHandler(b.onMessageCreate)
	b.Session.AddHandler(b.onMessageUpdate)
	b.Session.AddHandler(b.onMessageDelete)
	b.Session.AddHandler(b.onMessageDeleteBulk)
	b.Session.AddHandler(b.onChannelPinsUpdate)
	b.Session.AddHandler(b.onInteractionCreate)

	// Phase 4 — guild events (members, channels, guild, roles, voice,
	// threads, reactions, invites, emojis, stickers, user, guild join/remove).
	b.Session.AddHandler(b.onGuildCreate)
	b.Session.AddHandler(b.onGuildDelete)
	b.Session.AddHandler(b.onGuildUpdate)
	b.Session.AddHandler(b.onGuildMemberAdd)
	b.Session.AddHandler(b.onGuildMemberUpdate)
	b.Session.AddHandler(b.onGuildMemberRemove)
	b.Session.AddHandler(b.onGuildBanAdd)
	b.Session.AddHandler(b.onGuildBanRemove)
	b.Session.AddHandler(b.onGuildRoleCreate)
	b.Session.AddHandler(b.onGuildRoleUpdate)
	b.Session.AddHandler(b.onGuildRoleDelete)
	b.Session.AddHandler(b.onChannelCreate)
	b.Session.AddHandler(b.onChannelUpdate)
	b.Session.AddHandler(b.onChannelDelete)
	b.Session.AddHandler(b.onThreadCreate)
	b.Session.AddHandler(b.onThreadUpdate)
	b.Session.AddHandler(b.onThreadDelete)
	b.Session.AddHandler(b.onThreadMembersUpdate)
	b.Session.AddHandler(b.onVoiceStateUpdate)
	b.Session.AddHandler(b.onMessageReactionAdd)
	b.Session.AddHandler(b.onMessageReactionRemove)
	b.Session.AddHandler(b.onMessageReactionRemoveAll)
	b.Session.AddHandler(b.onInviteCreate)
	b.Session.AddHandler(b.onInviteDelete)
	b.Session.AddHandler(b.onGuildEmojisUpdate)
	b.Session.AddHandler(b.onGuildStickersUpdate)
	b.Session.AddHandler(b.onUserUpdate)

	// Phase 4 — moderation.
	b.Session.AddHandler(b.onAutoModerationRuleCreate)
	b.Session.AddHandler(b.onAutoModerationRuleUpdate)
	b.Session.AddHandler(b.onAutoModerationRuleDelete)
	b.Session.AddHandler(b.onAutoModerationActionExecution)
	b.Session.AddHandler(b.onGuildAuditLogEntryCreate)

	// Phase 4 — scheduled events / stage instances.
	b.Session.AddHandler(b.onScheduledEventCreate)
	b.Session.AddHandler(b.onScheduledEventUpdate)
	b.Session.AddHandler(b.onScheduledEventDelete)
	b.Session.AddHandler(b.onScheduledEventUserAdd)
	b.Session.AddHandler(b.onScheduledEventUserRemove)
	b.Session.AddHandler(b.onStageInstanceCreate)
	b.Session.AddHandler(b.onStageInstanceUpdate)
	b.Session.AddHandler(b.onStageInstanceDelete)

	// Phase 4 — integrations.
	b.Session.AddHandler(b.onGuildIntegrationsUpdate)
	b.Session.AddHandler(b.onIntegrationCreate)
	b.Session.AddHandler(b.onIntegrationUpdate)
	b.Session.AddHandler(b.onIntegrationDelete)
	b.Session.AddHandler(b.onWebhooksUpdate)
}

// Open opens the gateway connection. Returns the discordgo error verbatim.
func (b *Bot) Open() error  { return b.Session.Open() }
func (b *Bot) Close() error { return b.Session.Close() }

func (b *Bot) onReady(_ *discordgo.Session, r *discordgo.Ready) {
	slog.Info("bot connected",
		"user", r.User.Username, "id", r.User.ID, "guilds", len(r.Guilds))

	// Capture the initial guild ID set so subsequent GuildCreate events
	// for those IDs are recognized as availability sync, not real joins.
	ids := make([]string, 0, len(r.Guilds))
	for _, g := range r.Guilds {
		ids = append(ids, g.ID)
	}
	b.InitialGuilds.MarkReady(ids)

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
