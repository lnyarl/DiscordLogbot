package mcp

import (
	"testing"

	"github.com/lnyarl/discordlogbot/internal/permissions"
)

// ── parseISOToDBString ───────────────────────────────────────────────────

func TestParseISOToDBString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"Z suffix", "2026-04-25T00:00:00Z", "2026-04-25T00:00:00+00:00"},
		{"explicit +00:00", "2026-04-25T00:00:00+00:00", "2026-04-25T00:00:00+00:00"},
		{"non-UTC offset", "2026-04-25T09:00:00+09:00", "2026-04-25T00:00:00+00:00"},
		{"naive UTC assumed", "2026-04-25T00:00:00", "2026-04-25T00:00:00+00:00"},
		{"date only", "2026-04-25", "2026-04-25T00:00:00+00:00"},
		{"with microseconds", "2026-04-25T00:00:00.500000Z", "2026-04-25T00:00:00.500000+00:00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseISOToDBString(tt.in)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tt.want {
				t.Errorf("got=%q want=%q", got, tt.want)
			}
		})
	}
}

func TestParseISOToDBString_RejectsGarbage(t *testing.T) {
	if _, err := parseISOToDBString("not-a-date"); err == nil {
		t.Error("expected parse error")
	}
}

// ── appendTimeFilter ─────────────────────────────────────────────────────

func TestAppendTimeFilter(t *testing.T) {
	conds := []string{"guild_id = $1"}
	params := []any{"G1"}
	conds, params = appendTimeFilter(conds, params, "created_at", "2026-04-01T00:00:00+00:00", "2026-05-01T00:00:00+00:00")
	if len(conds) != 3 {
		t.Fatalf("conds=%d", len(conds))
	}
	if conds[1] != "created_at >= $2" {
		t.Errorf("since cond=%q", conds[1])
	}
	if conds[2] != "created_at <= $3" {
		t.Errorf("until cond=%q", conds[2])
	}
	if len(params) != 3 {
		t.Errorf("params=%v", params)
	}
}

func TestAppendTimeFilter_NoOpsOnEmpty(t *testing.T) {
	conds := []string{"guild_id = $1"}
	params := []any{"G1"}
	conds2, params2 := appendTimeFilter(conds, params, "created_at", "", "")
	if len(conds2) != 1 || len(params2) != 1 {
		t.Errorf("empty since/until must not append; got conds=%v params=%v", conds2, params2)
	}
}

// ── checkAccess ──────────────────────────────────────────────────────────

func TestCheckAccess_GuildOnly(t *testing.T) {
	channels := []permissions.AccessibleChannel{
		{GuildID: "G1", ChannelID: "C1"},
		{GuildID: "G1", ChannelID: "C2"},
	}
	if msg := checkAccess(channels, "G1", "", false); msg != "" {
		t.Errorf("guild-only access should pass; msg=%q", msg)
	}
	if msg := checkAccess(channels, "G2", "", false); !contains(msg, "서버") {
		t.Errorf("missing guild rejection; msg=%q", msg)
	}
}

func TestCheckAccess_ChannelRequired(t *testing.T) {
	channels := []permissions.AccessibleChannel{
		{GuildID: "G1", ChannelID: "C1"},
	}
	tests := []struct {
		name           string
		guildID, chID  string
		wantAcceptable bool
	}{
		{"matching channel", "G1", "C1", true},
		{"empty channel ID", "G1", "", false},
		{"unknown channel", "G1", "C-other", false},
		{"channel from different guild", "G1", "C2", false},
		{"unknown guild", "G2", "C1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := checkAccess(channels, tt.guildID, tt.chID, true)
			gotAcceptable := msg == ""
			if gotAcceptable != tt.wantAcceptable {
				t.Errorf("got msg=%q wantAcceptable=%v", msg, tt.wantAcceptable)
			}
		})
	}
}

// ── likeEscapeMCP ────────────────────────────────────────────────────────

func TestLikeEscapeMCP(t *testing.T) {
	if got := likeEscapeMCP("50% off"); got != `50\% off` {
		t.Errorf("got=%q", got)
	}
	if got := likeEscapeMCP("a_b"); got != `a\_b` {
		t.Errorf("got=%q", got)
	}
	if got := likeEscapeMCP(`back\slash`); got != `back\\slash` {
		t.Errorf("got=%q", got)
	}
}

// ── clampInt ─────────────────────────────────────────────────────────────

func TestClampInt(t *testing.T) {
	tests := []struct {
		v, def, max, want int
	}{
		{0, 100, 500, 100},   // unset → default
		{-1, 100, 500, 100},  // negative → default
		{50, 100, 500, 50},   // within range
		{1000, 100, 500, 500}, // capped at max
	}
	for _, tt := range tests {
		if got := clampInt(tt.v, tt.def, tt.max); got != tt.want {
			t.Errorf("clampInt(%d, %d, %d) = %d, want %d", tt.v, tt.def, tt.max, got, tt.want)
		}
	}
}

// ── parseJSONDetails ─────────────────────────────────────────────────────

func TestParseJSONDetails(t *testing.T) {
	if d := parseJSONDetails(""); d != nil {
		t.Errorf("empty should be nil; got %v", d)
	}
	d := parseJSONDetails(`{"k":"v"}`)
	m, ok := d.(map[string]any)
	if !ok || m["k"] != "v" {
		t.Errorf("got=%v", d)
	}
	if d := parseJSONDetails("garbage"); d != "garbage" {
		t.Errorf("garbage should pass through; got %v", d)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
