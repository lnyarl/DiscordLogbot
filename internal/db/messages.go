package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Attachment matches the JSON shape Python stores in messages.attachments.
//
// LocalPath is *string to mirror Python's `None` on download failure — Python
// produces `"local_path": null`; an empty Go string with a regular string
// field would produce `""`, and `omitempty` would drop the key entirely.
// Both diverge from existing Python rows.
type Attachment struct {
	URL         string  `json:"url"`
	LocalPath   *string `json:"local_path"`
	Filename    string  `json:"filename"`
	ContentType string  `json:"content_type"`
	Size        int     `json:"size"`
}

// SaveMessageInput consolidates the args for SaveMessage to avoid a
// 10-positional call site (and to mirror Python's keyword arguments).
type SaveMessageInput struct {
	MessageID   string
	GuildID     string
	ChannelID   string
	ChannelName string
	AuthorID    string
	AuthorName  string
	Content     string
	Attachments []Attachment
	CreatedAt   time.Time
	Action      string // "" → "add"
}

// Python's datetime.isoformat() omits the fractional-second segment when
// microseconds are exactly zero, otherwise prints them with 6-digit
// zero-padding. We mirror that branch instead of using Go's "2006-...05.999999..."
// trim-trailing-zero format, because mixed Python+Go writers + lexicographic
// since/until filters would otherwise miss-match boundary rows
// (e.g. Go-saved ".5+00:00" vs Python filter ".500000+00:00" of the same instant).
func formatTime(t time.Time) string {
	t = t.UTC()
	if t.Nanosecond() == 0 {
		return t.Format("2006-01-02T15:04:05-07:00")
	}
	return t.Format("2006-01-02T15:04:05.000000-07:00")
}

// SaveMessage inserts an "add" (default) row.
func SaveMessage(ctx context.Context, pool *pgxpool.Pool, in SaveMessageInput) error {
	if in.Action == "" {
		in.Action = "add"
	}
	att, err := json.Marshal(in.Attachments)
	if err != nil {
		return fmt.Errorf("marshal attachments: %w", err)
	}
	slog.Info("save message",
		"action", in.Action, "channel", in.ChannelName,
		"author", in.AuthorName, "preview", preview(in.Content, 80))
	_, err = pool.Exec(ctx, `
		INSERT INTO messages
			(message_id, guild_id, channel_id, channel_name,
			 author_id, author_name, content, attachments, action, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`,
		in.MessageID, in.GuildID, in.ChannelID, in.ChannelName,
		in.AuthorID, in.AuthorName, in.Content,
		string(att), in.Action, formatTime(in.CreatedAt),
	)
	return err
}

// preview truncates s to maxLen runes (not bytes) for log output.
func preview(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen]) + "…"
}

// SaveEdit copies guild/channel/author/attachments from the latest existing
// row for messageID and inserts a new "update" row with newContent. If no
// prior row exists (cache miss / out of retention), it silently no-ops to
// match Python's behaviour — there's nothing to thread the edit to.
func SaveEdit(ctx context.Context, pool *pgxpool.Pool, messageID, newContent string) error {
	var guildID, channelID, channelName, authorID, authorName, attachments string
	err := pool.QueryRow(ctx, `
		SELECT guild_id, channel_id, channel_name, author_id, author_name, attachments
		FROM messages WHERE message_id = $1 ORDER BY id DESC LIMIT 1
	`, messageID).Scan(&guildID, &channelID, &channelName, &authorID, &authorName, &attachments)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	slog.Info("save edit", "message_id", messageID, "preview", preview(newContent, 80))
	_, err = pool.Exec(ctx, `
		INSERT INTO messages
			(message_id, guild_id, channel_id, channel_name,
			 author_id, author_name, content, attachments, action, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'update', $9)
	`,
		messageID, guildID, channelID, channelName, authorID, authorName,
		newContent, attachments, formatTime(time.Now()),
	)
	return err
}

// SaveDelete carries the latest content into the "delete" row so the
// audit trail preserves what was actually deleted. Returns inserted=false
// when no prior row exists (silently skipped, mirrors Python's "if row:"),
// so a bulk-delete caller can distinguish actually-logged deletions from
// no-ops (e.g. bot messages or messages from unlogged channels) and avoid
// inflating the count / message_ids in the bulk_message_delete event.
func SaveDelete(ctx context.Context, pool *pgxpool.Pool, messageID string) (inserted bool, err error) {
	var guildID, channelID, channelName, authorID, authorName, content, attachments string
	err = pool.QueryRow(ctx, `
		SELECT guild_id, channel_id, channel_name, author_id, author_name, content, attachments
		FROM messages WHERE message_id = $1 ORDER BY id DESC LIMIT 1
	`, messageID).Scan(&guildID, &channelID, &channelName, &authorID, &authorName, &content, &attachments)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	slog.Info("save delete", "message_id", messageID)
	_, err = pool.Exec(ctx, `
		INSERT INTO messages
			(message_id, guild_id, channel_id, channel_name,
			 author_id, author_name, content, attachments, action, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'delete', $9)
	`,
		messageID, guildID, channelID, channelName, authorID, authorName,
		content, attachments, formatTime(time.Now()),
	)
	if err != nil {
		return false, err
	}
	return true, nil
}

// LatestMessageInfo holds the fields the pin-update handler needs to
// describe an unpinned message even if it is no longer fetchable.
type LatestMessageInfo struct {
	Content    string
	AuthorName string
}

// GetLatestMessageInfo returns the most recent row for messageID, or nil
// if it has never been logged.
func GetLatestMessageInfo(ctx context.Context, pool *pgxpool.Pool, messageID string) (*LatestMessageInfo, error) {
	var info LatestMessageInfo
	err := pool.QueryRow(ctx, `
		SELECT content, author_name FROM messages WHERE message_id = $1 ORDER BY id DESC LIMIT 1
	`, messageID).Scan(&info.Content, &info.AuthorName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// GetMessageCount returns the row count for /logbot status.
func GetMessageCount(ctx context.Context, pool *pgxpool.Pool, guildID string) (int64, error) {
	var n int64
	err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM messages WHERE guild_id = $1", guildID).Scan(&n)
	return n, err
}
