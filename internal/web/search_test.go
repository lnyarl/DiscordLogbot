package web

import (
	"errors"
	"strings"
	"testing"

	"github.com/lnyarl/discordlogbot/internal/cache"
)

func TestDedupChannelIDs(t *testing.T) {
	in := []cache.Channel{
		{ChannelID: "c1"}, {ChannelID: "c2"}, {ChannelID: "c1"}, {ChannelID: "c3"},
	}
	out := dedupChannelIDs(in)
	if len(out) != 3 {
		t.Fatalf("len=%d want 3", len(out))
	}
	want := map[string]bool{"c1": true, "c2": true, "c3": true}
	for _, id := range out {
		if !want[id] {
			t.Errorf("unexpected id %q", id)
		}
	}
	if dedup := dedupChannelIDs(nil); len(dedup) != 0 {
		t.Errorf("nil input should produce empty slice, got %v", dedup)
	}
}

// LikeEscape: characters Postgres treats as ILIKE wildcards must be
// backslash-escaped before being concatenated into a `%pattern%`. Mirrors
// search.py exactly.
func TestLikeEscape(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"hello", "hello"},
		{`he\lo`, `he\\lo`},
		{"50% off", `50\% off`},
		{"under_score", `under\_score`},
		{`mix \ % _`, `mix \\ \% \_`},
	}
	for _, tt := range tests {
		if got := likeEscape(tt.in); got != tt.want {
			t.Errorf("likeEscape(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParsePositiveInt(t *testing.T) {
	tests := []struct {
		in   string
		def  int
		want int
	}{
		{"", 1, 1},
		{"5", 1, 5},
		{"abc", 7, 7},
		{"-3", 1, 1}, // negative falls back to default
		{"0", 1, 1},
	}
	for _, tt := range tests {
		if got := parsePositiveInt(tt.in, tt.def); got != tt.want {
			t.Errorf("parsePositiveInt(%q, %d) = %d, want %d", tt.in, tt.def, got, tt.want)
		}
	}
}

func TestLabelFor(t *testing.T) {
	if got := labelFor("member_join"); got != "멤버 입장" {
		t.Errorf("got=%q", got)
	}
	// Unknown event types fall back to the raw type.
	if got := labelFor("totally_unknown"); got != "totally_unknown" {
		t.Errorf("unknown should pass through, got=%q", got)
	}
}

func TestParseAttachments(t *testing.T) {
	if got := parseAttachments(""); len(got) != 0 {
		t.Errorf("empty should be empty list, got %v", got)
	}
	got := parseAttachments(`[{"url":"https://x","filename":"f.png"}]`)
	if len(got) != 1 || got[0]["url"] != "https://x" {
		t.Errorf("got=%v", got)
	}
	if g := parseAttachments("not json"); len(g) != 0 {
		t.Errorf("invalid JSON should fall back to []; got %v", g)
	}
}

func TestParseDetails(t *testing.T) {
	// Object form
	d := parseDetails(`{"channel_id":"C1","content":"hi"}`)
	m, ok := d.(map[string]any)
	if !ok || m["channel_id"] != "C1" {
		t.Errorf("got=%v", d)
	}
	// String pass-through on parse failure.
	if d := parseDetails("plain text"); d != "plain text" {
		t.Errorf("invalid JSON expected raw passthrough, got %v", d)
	}
	// Empty → nil.
	if d := parseDetails(""); d != nil {
		t.Errorf("empty expected nil, got %v", d)
	}
}

func TestRound3(t *testing.T) {
	tests := []struct {
		in, want float64
	}{
		{0.123456, 0.123},
		{1.0, 1.0},
		{0.999999, 1.0},
		{0.4999, 0.5},
	}
	for _, tt := range tests {
		if got := round3(tt.in); got != tt.want {
			t.Errorf("round3(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// scanMessageRows walks any pgx.Rows-like and short-circuits on Scan
// errors. Verify with a stub.
type stubRows struct {
	rows int
	err  error
	pos  int
}

func (s *stubRows) Next() bool { return s.pos < s.rows }
func (s *stubRows) Scan(dest ...any) error {
	if s.err != nil {
		return s.err
	}
	if len(dest) != 10 {
		return errors.New("wrong scan arity")
	}
	*dest[0].(*string) = "M1"
	*dest[1].(*string) = "G1"
	*dest[2].(*string) = "C1"
	*dest[3].(*string) = "general"
	*dest[4].(*string) = "alice"
	*dest[5].(*string) = "hi"
	*dest[6].(*string) = "[]"
	*dest[7].(*string) = "add"
	*dest[8].(*string) = "2026-01-01T00:00:00+00:00"
	*dest[9].(*float64) = 0.7
	s.pos++
	return nil
}

func TestScanMessageRows(t *testing.T) {
	rs := &stubRows{rows: 2}
	got := scanMessageRows(rs)
	if len(got) != 2 {
		t.Fatalf("rows=%d", len(got))
	}
	if got[0].MessageID != "M1" {
		t.Errorf("messageID=%q", got[0].MessageID)
	}
	if got[0].Score == nil || *got[0].Score != 0.7 {
		t.Errorf("score=%v", got[0].Score)
	}
}

func TestEventLabels_Coverage(t *testing.T) {
	// Spot-check a handful — full table of 56 entries.
	wants := map[string]string{
		"member_join":        "멤버 입장",
		"channel_create":     "채널 생성",
		"automod_action":     "AutoMod 실행",
		"webhooks_update":    "웹훅 변경",
		"audit_log":          "감사 로그",
	}
	for k, want := range wants {
		if got := EventLabels[k]; got != want {
			t.Errorf("EventLabels[%q] = %q, want %q", k, got, want)
		}
	}
	// Sanity: total count matches Python's dictionary length.
	if got, want := len(EventLabels), 54; got != want {
		t.Errorf("EventLabels size = %d, want %d", got, want)
	}
}

func TestStripPath(t *testing.T) {
	tests := map[string]string{
		"https://historian.stashy.in/auth/callback": "https://historian.stashy.in",
		"http://localhost:8080/auth/callback":       "http://localhost:8080",
		"https://example.com":                       "https://example.com",
		"not-a-url":                                 "",
	}
	for in, want := range tests {
		if got := stripPath(in); got != want {
			t.Errorf("stripPath(%q) = %q, want %q", in, got, want)
		}
	}
}

var _ = strings.HasPrefix
