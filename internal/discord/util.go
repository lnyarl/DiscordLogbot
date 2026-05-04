package discord

import (
	"time"

	"github.com/bwmarrin/discordgo"
)

// isoformatPy formats t the way Python's datetime.isoformat() does so JSON
// payloads in guild_events.details remain string-comparable across
// Python+Go writers (same parity rule as db.formatTime).
func isoformatPy(t time.Time) string {
	t = t.UTC()
	if t.Nanosecond() == 0 {
		return t.Format("2006-01-02T15:04:05-07:00")
	}
	return t.Format("2006-01-02T15:04:05.000000-07:00")
}

// isoformatPyPtr returns nil for a nil time so JSON encodes `null`, matching
// Python's `t.isoformat() if t else None`.
func isoformatPyPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return isoformatPy(*t)
}

// emojiString mirrors discord.py's str(emoji): "<:name:id>" / "<a:name:id>"
// for custom emojis, and the unicode glyph alone for stock emojis (where
// ID is empty).
func emojiString(e *discordgo.Emoji) string {
	if e == nil {
		return ""
	}
	if e.ID == "" {
		return e.Name
	}
	if e.Animated {
		return "<a:" + e.Name + ":" + e.ID + ">"
	}
	return "<:" + e.Name + ":" + e.ID + ">"
}

// memberTag formats a Member the way Python's str(Member) does: it falls
// through to str(User), which authorTag already implements.
func memberTag(m *discordgo.Member) string {
	if m == nil || m.User == nil {
		return ""
	}
	return authorTag(m.User)
}
