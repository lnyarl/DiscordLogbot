package discord

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/lnyarl/discordlogbot/internal/db"
)

// onMessageCreate / processCreate split: the discordgo callback adapts to
// the gateway's signature, while processCreate holds the testable logic.

func (b *Bot) onMessageCreate(_ *discordgo.Session, m *discordgo.MessageCreate) {
	if err := b.processCreate(context.Background(), m.Message); err != nil {
		slog.Error("processCreate failed", "err", err, "message_id", m.ID)
	}
}

func (b *Bot) processCreate(ctx context.Context, m *discordgo.Message) error {
	if m.Author == nil || m.Author.Bot {
		return nil
	}
	if m.GuildID == "" {
		return nil
	}
	// discord.py: type ∈ {default, reply}. Other types (channel pin notice,
	// member join, thread starter, etc.) are excluded.
	if m.Type != discordgo.MessageTypeDefault && m.Type != discordgo.MessageTypeReply {
		return nil
	}

	logged, err := b.Channels.IsLogged(ctx, b.Pool, m.GuildID, m.ChannelID)
	if err != nil {
		return err
	}
	if !logged {
		return nil
	}

	channelName := b.channelName(m.ChannelID)

	// Download attachments and accumulate metadata for the JSON column.
	var atts []db.Attachment
	for _, a := range m.Attachments {
		rel := b.Downloader.DownloadAttachment(ctx, a.URL, m.ChannelID, m.ID, a.Filename)
		var localPath *string
		if rel != "" {
			localPath = &rel
		}
		atts = append(atts, db.Attachment{
			URL:         a.URL,
			LocalPath:   localPath,
			Filename:    a.Filename,
			ContentType: a.ContentType,
			Size:        int(a.Size),
		})
	}

	if m.Content != "" {
		b.Downloader.DownloadEmojis(ctx, m.Content)
	}

	content := augmentContent(m.Content, atts, m.StickerItems)

	return db.SaveMessage(ctx, b.Pool, db.SaveMessageInput{
		MessageID:   m.ID,
		GuildID:     m.GuildID,
		ChannelID:   m.ChannelID,
		ChannelName: channelName,
		AuthorID:    m.Author.ID,
		AuthorName:  authorTag(m.Author),
		Content:     content,
		Attachments: atts,
		CreatedAt:   m.Timestamp,
	})
}

// augmentContent appends "[<filename>]" placeholders for every attachment
// and "[스티커: <name>]" for every sticker item, matching the
// LoggingCog.on_message text mutation. Empty content with attachments
// becomes the joined placeholder string; otherwise placeholders are
// appended after a single space.
func augmentContent(content string, atts []db.Attachment, stickers []*discordgo.StickerItem) string {
	if len(atts) > 0 {
		tags := make([]string, len(atts))
		for i, a := range atts {
			tags[i] = "[" + a.Filename + "]"
		}
		joined := strings.Join(tags, " ")
		if content == "" {
			content = joined
		} else {
			content = content + " " + joined
		}
	}
	if len(stickers) > 0 {
		names := make([]string, len(stickers))
		for i, s := range stickers {
			names[i] = "[스티커: " + s.Name + "]"
		}
		joined := strings.Join(names, " ")
		if content == "" {
			content = joined
		} else {
			// Python: f"{content} {sticker_text}".strip(). Strip trailing
			// whitespace from content first so trailing-space text + sticker
			// doesn't produce a double space.
			content = strings.TrimRight(content, " ") + " " + joined
		}
	}
	return content
}

// authorTag formats a Discord User the way Python's str(user) does:
// "Name#1234" historically, but Discord's username migration sets
// Discriminator to "0" for migrated users — discord.py renders those
// as just "Name" without the suffix.
func authorTag(u *discordgo.User) string {
	if u == nil {
		return ""
	}
	if u.Discriminator == "" || u.Discriminator == "0" {
		return u.Username
	}
	return u.Username + "#" + u.Discriminator
}

// ── MessageUpdate ──────────────────────────────────────────────────────

func (b *Bot) onMessageUpdate(_ *discordgo.Session, m *discordgo.MessageUpdate) {
	if err := b.processUpdate(context.Background(), m); err != nil {
		slog.Error("processUpdate failed", "err", err, "message_id", m.ID)
	}
}

func (b *Bot) processUpdate(ctx context.Context, m *discordgo.MessageUpdate) error {
	// Author may be nil for pin-only updates; treat that the same as a
	// non-bot user (we still gate on content below).
	if m.Author != nil && m.Author.Bot {
		return nil
	}
	if m.GuildID == "" {
		return nil
	}
	// Pin-only / reaction-only updates carry no content. Mirrors Python's
	// `"pinned" in payload.data and "content" not in payload.data` skip.
	if m.Content == "" {
		return nil
	}
	logged, err := b.Channels.IsLogged(ctx, b.Pool, m.GuildID, m.ChannelID)
	if err != nil {
		return err
	}
	if !logged {
		return nil
	}
	// De-dup against the latest stored content for this message id —
	// Discord re-fires MESSAGE_UPDATE for non-content reasons (pins,
	// embeds resolving, etc.); skip if nothing actually changed.
	info, err := db.GetLatestMessageInfo(ctx, b.Pool, m.ID)
	if err != nil {
		return err
	}
	if info != nil && info.Content == m.Content {
		return nil
	}
	return db.SaveEdit(ctx, b.Pool, m.ID, m.Content)
}

// ── MessageDelete ──────────────────────────────────────────────────────

func (b *Bot) onMessageDelete(_ *discordgo.Session, m *discordgo.MessageDelete) {
	if err := b.processDelete(context.Background(), m); err != nil {
		slog.Error("processDelete failed", "err", err, "message_id", m.ID)
	}
}

func (b *Bot) processDelete(ctx context.Context, m *discordgo.MessageDelete) error {
	if m.GuildID == "" {
		return nil
	}
	logged, err := b.Channels.IsLogged(ctx, b.Pool, m.GuildID, m.ChannelID)
	if err != nil {
		return err
	}
	if !logged {
		return nil
	}
	_, err = db.SaveDelete(ctx, b.Pool, m.ID)
	return err
}

// ── MessageDeleteBulk ──────────────────────────────────────────────────

func (b *Bot) onMessageDeleteBulk(_ *discordgo.Session, m *discordgo.MessageDeleteBulk) {
	if err := b.processDeleteBulk(context.Background(), m); err != nil {
		slog.Error("processDeleteBulk failed", "err", err, "channel_id", m.ChannelID)
	}
}

func (b *Bot) processDeleteBulk(ctx context.Context, m *discordgo.MessageDeleteBulk) error {
	if m.GuildID == "" || len(m.Messages) == 0 {
		return nil
	}
	logged, err := b.Channels.IsLogged(ctx, b.Pool, m.GuildID, m.ChannelID)
	if err != nil {
		return err
	}
	if !logged {
		return nil
	}
	channelName := b.channelName(m.ChannelID)
	processed := make([]string, 0, len(m.Messages))
	for _, id := range m.Messages {
		// Only count IDs that actually had a prior row to mark as deleted.
		// Python filters bot messages explicitly via author.bot; discordgo
		// gives us only IDs, so we let SaveDelete signal whether a row
		// existed and skip the rest. This keeps count/message_ids honest.
		inserted, err := db.SaveDelete(ctx, b.Pool, id)
		if err != nil {
			slog.Error("save delete in bulk", "err", err, "message_id", id)
			continue
		}
		if !inserted {
			continue
		}
		processed = append(processed, id)
	}
	if len(processed) == 0 {
		return nil
	}
	return db.SaveGuildEvent(ctx, b.Pool, db.GuildEventInput{
		EventType:  "bulk_message_delete",
		GuildID:    m.GuildID,
		TargetID:   m.ChannelID,
		TargetName: channelName,
		Details: map[string]any{
			"channel_id":   m.ChannelID,
			"channel_name": channelName,
			"count":        len(processed),
			"message_ids":  processed,
		},
		OccurredAt: time.Now(),
	})
}
