package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lnyarl/discordlogbot/internal/permissions"
)

// SQL query toolkit shared by the search_messages, get_messages, and
// get_guild_events tool handlers in server.go. Pulled out so the tool
// handlers themselves stay short and easy to scan.

// ── Access checks ────────────────────────────────────────────────────────

// checkAccess reports an empty string when the user is allowed to query
// guildID (with channelID, when channelRequired is true). channel-required
// tools (search_messages / get_messages) pass channelRequired=true; the
// guild-only tools (get_guild_events) pass false. Returns the user-facing
// rejection message on denial.
func checkAccess(channels []permissions.AccessibleChannel, guildID, channelID string, channelRequired bool) string {
	guildChannels := map[string]map[string]struct{}{}
	for _, c := range channels {
		m := guildChannels[c.GuildID]
		if m == nil {
			m = map[string]struct{}{}
			guildChannels[c.GuildID] = m
		}
		m[c.ChannelID] = struct{}{}
	}
	chans, ok := guildChannels[guildID]
	if !ok {
		return "접근 거부: 해당 서버에 접근 권한이 없습니다."
	}
	if channelRequired {
		if channelID == "" {
			return "접근 거부: 해당 채널에 접근 권한이 없습니다."
		}
		if _, ok := chans[channelID]; !ok {
			return "접근 거부: 해당 채널에 접근 권한이 없습니다."
		}
	}
	return ""
}

// ── Time filter ──────────────────────────────────────────────────────────

// parseISOToDBString mirrors web/mcp_router.py:_parse_iso_to_db_string.
// DB stores created_at/occurred_at as ISO 8601 text in UTC with +00:00
// offset; lexicographic comparison only matches chronological order if
// every value carries the same offset string. Naive datetimes are assumed
// UTC. Truncates to microseconds to match Python's isoformat() output.
func parseISOToDBString(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.000000Z",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05.000000",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	var t time.Time
	var err error
	for _, layout := range layouts {
		t, err = time.Parse(layout, s)
		if err == nil {
			break
		}
	}
	if err != nil {
		return "", fmt.Errorf("잘못된 ISO 8601 datetime: %q", s)
	}
	t = t.UTC()
	// Truncate to microseconds: Python's isoformat() prints exactly 6
	// fractional digits when non-zero, so any sub-microsecond precision
	// would diverge from a Python-written row. Truncation (not rounding)
	// keeps lexicographic comparisons monotonic.
	t = t.Truncate(time.Microsecond)
	if t.Nanosecond() == 0 {
		return t.Format("2006-01-02T15:04:05-07:00"), nil
	}
	return t.Format("2006-01-02T15:04:05.000000-07:00"), nil
}

// appendTimeFilter pushes since/until conditions and corresponding params
// onto the slice builder (migration plan §5 dynamic-numbering pattern).
func appendTimeFilter(conditions []string, params []any, column, since, until string) ([]string, []any) {
	if since != "" {
		params = append(params, since)
		conditions = append(conditions, fmt.Sprintf("%s >= $%d", column, len(params)))
	}
	if until != "" {
		params = append(params, until)
		conditions = append(conditions, fmt.Sprintf("%s <= $%d", column, len(params)))
	}
	return conditions, params
}

// ── LIKE escape ──────────────────────────────────────────────────────────

// likeEscapeMCP duplicates internal/web.likeEscape — the rule is identical
// but importing internal/web would couple the MCP server to the web HTTP
// layer. Both copies must stay in sync; if you change one, change the
// other (and pair with `ESCAPE '\'` at the call site, both packages do).
func likeEscapeMCP(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

// ── Misc helpers ─────────────────────────────────────────────────────────

func parseJSONDetails(raw string) any {
	if raw == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	return v
}

func clampInt(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}
