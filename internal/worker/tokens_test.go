package worker

import (
	"strings"
	"testing"
	"time"
)

// ── EstimateTokens ───────────────────────────────────────────────────────

func TestEstimateTokens_Korean(t *testing.T) {
	// 한글 5자 = 10 tokens + 1 = 11
	if got := EstimateTokens("안녕하세요"); got != 11 {
		t.Errorf("got=%d want 11", got)
	}
}

func TestEstimateTokens_ASCII(t *testing.T) {
	// 10 ASCII chars = 4.0 → int = 4 + 1 = 5
	if got := EstimateTokens("hello world"); got != 5 {
		t.Errorf("got=%d want 5", got)
	}
}

func TestEstimateTokens_Mixed(t *testing.T) {
	// "Hello 안녕" → "H","e","l","l","o"," ", + 2 Korean chars
	// other = 6 (with space), korean = 2 → 6*0.4 + 2*2 = 2.4+4 = 6.4 → 6 + 1 = 7
	if got := EstimateTokens("Hello 안녕"); got != 7 {
		t.Errorf("got=%d want 7", got)
	}
}

func TestEstimateTokens_Empty(t *testing.T) {
	if got := EstimateTokens(""); got != 1 {
		t.Errorf("empty string should be 1 (the +1 floor); got %d", got)
	}
}

// ── ParseDT ──────────────────────────────────────────────────────────────

func TestParseDT(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want time.Time
	}{
		{"Z suffix", "2026-04-25T00:00:00Z", time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)},
		{"explicit offset", "2026-04-25T09:00:00+09:00", time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)},
		{"naive UTC assumed", "2026-04-25T00:00:00", time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)},
		{"with microseconds", "2026-04-25T00:00:00.500000Z", time.Date(2026, 4, 25, 0, 0, 0, 500_000_000, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDT(tt.in)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("got=%s want=%s", got, tt.want)
			}
		})
	}
}

func TestParseDT_RejectsGarbage(t *testing.T) {
	if _, err := ParseDT("not-a-date"); err == nil {
		t.Error("expected parse error")
	}
}

func TestParseDT_NaiveWithMicroseconds(t *testing.T) {
	// asyncpg often returns naive (no tz) timestamps with microsecond
	// precision; the layout "2006-01-02T15:04:05.000000" must catch
	// these before falling through to the date-only fallback.
	got, err := ParseDT("2026-04-25T00:00:00.500000")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := time.Date(2026, 4, 25, 0, 0, 0, 500_000_000, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got=%s want=%s", got, want)
	}
}

// ── TrimByGap ────────────────────────────────────────────────────────────

func TestTrimByGap_StopsOnLargeGap(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	messages := []ContextMessage{
		{CreatedAt: "2026-01-01T11:30:00+00:00"},  // 30min before — kept
		{CreatedAt: "2026-01-01T11:00:00+00:00"},  // 30min before that — kept
		{CreatedAt: "2026-01-01T05:00:00+00:00"},  // 6h gap → cut here
		{CreatedAt: "2026-01-01T04:30:00+00:00"},  // discarded
	}
	got := TrimByGap(messages, anchor, 2*time.Hour)
	if len(got) != 2 {
		t.Errorf("got %d messages, want 2", len(got))
	}
}

func TestTrimByGap_KeepsAllWithinGap(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	messages := []ContextMessage{
		{CreatedAt: "2026-01-01T11:30:00+00:00"},
		{CreatedAt: "2026-01-01T11:00:00+00:00"},
		{CreatedAt: "2026-01-01T10:30:00+00:00"},
	}
	got := TrimByGap(messages, anchor, 2*time.Hour)
	if len(got) != 3 {
		t.Errorf("got %d messages, want 3", len(got))
	}
}

func TestTrimByGap_EmptySlice(t *testing.T) {
	got := TrimByGap(nil, time.Now(), time.Hour)
	if len(got) != 0 {
		t.Errorf("nil input should produce empty, got %v", got)
	}
}

// ── BuildContextText ─────────────────────────────────────────────────────

func TestBuildContextText_OnlyCurrentWhenBudgetExhausted(t *testing.T) {
	current := ContextMessage{AuthorName: "alice", Content: strings.Repeat("x", 100)}
	got := BuildContextText(current, nil, nil, 5)
	if !strings.HasPrefix(got, "alice: x") {
		t.Errorf("got %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Error("no neighbours expected")
	}
}

func TestBuildContextText_AlternatesBeforeAfter(t *testing.T) {
	current := ContextMessage{AuthorName: "C", Content: "now"}
	before := []ContextMessage{
		{AuthorName: "B0", Content: "bef0"}, // closest before
		{AuthorName: "B1", Content: "bef1"},
	}
	after := []ContextMessage{
		{AuthorName: "A0", Content: "aft0"}, // closest after
		{AuthorName: "A1", Content: "aft1"},
	}
	got := BuildContextText(current, before, after, 1000)

	// Expected order: B1, B0, C, A0, A1 (before reversed for chronological)
	expected := "B1: bef1\nB0: bef0\nC: now\nA0: aft0\nA1: aft1"
	if got != expected {
		t.Errorf("got=%q\nwant=%q", got, expected)
	}
}

func TestBuildContextText_SkipsTooBigButAdvances(t *testing.T) {
	// Mirror Python's "skip-but-advance" — if a single line exceeds the
	// remaining budget, that index is consumed (not retried) and the
	// loop moves to the next neighbour.
	current := ContextMessage{AuthorName: "C", Content: "."}
	before := []ContextMessage{
		{AuthorName: "TooBig", Content: strings.Repeat("x", 1000)}, // > budget
		{AuthorName: "small", Content: "ok"},                        // fits
	}
	got := BuildContextText(current, before, nil, 30)

	if !strings.Contains(got, "small: ok") {
		t.Errorf("expected small line to be picked, got %q", got)
	}
	if strings.Contains(got, "TooBig") {
		t.Errorf("expected TooBig to be skipped, got %q", got)
	}
}

func TestBuildContextText_CurrentExactlyOverBudget(t *testing.T) {
	current := ContextMessage{AuthorName: "X", Content: strings.Repeat("a", 50)}
	if t1 := EstimateTokens("X: " + strings.Repeat("a", 50)); t1 < 10 {
		t.Skipf("token estimate too low for this guard: %d", t1)
	}
	got := BuildContextText(current, []ContextMessage{{AuthorName: "B", Content: "x"}}, nil, 5)
	// Current alone exceeds budget → return only current.
	if strings.Contains(got, "B:") {
		t.Errorf("expected only current, got %q", got)
	}
}
