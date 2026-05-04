// Package worker is the Phase 7 embedding worker. It pulls messages with
// no embedding from Postgres, builds a context window of nearby messages
// in the same channel, embeds the result via Ollama, and writes the
// resulting vector back via pgvector.
package worker

import (
	"fmt"
	"strings"
	"time"
)

// EstimateTokens approximates the bge-m3 input length for a Korean/English
// mixed string. Mirrors workers/embedding_worker.py:estimate_tokens —
// Korean 1 char ≈ 2 tokens, ASCII 1 char ≈ 0.4 tokens, +1 for safety.
//
// Pure function; tested directly.
func EstimateTokens(s string) int {
	korean := 0
	other := 0
	for _, r := range s {
		if r >= '가' && r <= '힣' {
			korean++
		} else {
			other++
		}
	}
	return int(float64(korean)*2.0+float64(other)*0.4) + 1
}

// ParseDT parses an ISO 8601 string the same way Python
// `datetime.fromisoformat` does — accepts Z and explicit offsets, treats
// naive datetimes as UTC. Returns time.Time.
func ParseDT(s string) (time.Time, error) {
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
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid ISO datetime %q: %w", s, err)
}

// ContextMessage is the minimal shape buildContextText / trimByGap need —
// the worker queries strip everything except author_name / content /
// created_at, so this matches the SELECT column set.
type ContextMessage struct {
	AuthorName string
	Content    string
	CreatedAt  string // raw ISO string from DB (text column)
}

// TrimByGap drops messages once the gap between adjacent messages exceeds
// maxGap. Anchored at anchorDT — the first iteration compares against
// the anchor, then walks forward through the slice. Mirrors Python's
// trim_by_gap exactly.
//
// `messages` should be ordered such that index 0 is the closest to the
// anchor (Python feeds it before/after lists pre-ordered that way).
func TrimByGap(messages []ContextMessage, anchorDT time.Time, maxGap time.Duration) []ContextMessage {
	out := make([]ContextMessage, 0, len(messages))
	prev := anchorDT
	for _, m := range messages {
		dt, err := ParseDT(m.CreatedAt)
		if err != nil {
			break
		}
		if absDuration(dt.Sub(prev)) > maxGap {
			break
		}
		out = append(out, m)
		prev = dt
	}
	return out
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// BuildContextText takes the current message and pre-ordered before/after
// neighbour lists and returns the concatenated text used as the embedding
// input. Always emits the current message. Then alternates picking from
// before / after (closest-first) while the token budget allows. Mirrors
// the Python build_context_text logic exactly, including the +1 newline
// cost per added line and the "skip-but-advance" behavior when a single
// neighbour exceeds the remaining budget.
func BuildContextText(current ContextMessage, before, after []ContextMessage, maxTokens int) string {
	fmtMsg := func(m ContextMessage) string {
		return m.AuthorName + ": " + m.Content
	}
	currentText := fmtMsg(current)
	currentTokens := EstimateTokens(currentText)
	if currentTokens >= maxTokens {
		return currentText
	}
	budget := maxTokens - currentTokens

	beforeParts := make([]string, 0, len(before))
	afterParts := make([]string, 0, len(after))
	bi, ai := 0, 0
	for budget > 0 && (bi < len(before) || ai < len(after)) {
		if bi < len(before) {
			line := fmtMsg(before[bi])
			cost := EstimateTokens(line) + 1 // +1 newline
			if cost <= budget {
				beforeParts = append(beforeParts, line)
				budget -= cost
			}
			bi++
		}
		if ai < len(after) && budget > 0 {
			line := fmtMsg(after[ai])
			cost := EstimateTokens(line) + 1
			if cost <= budget {
				afterParts = append(afterParts, line)
				budget -= cost
			}
			ai++
		}
	}

	// before is closest-first; reverse so older lines appear at the top.
	parts := make([]string, 0, len(beforeParts)+1+len(afterParts))
	for i := len(beforeParts) - 1; i >= 0; i-- {
		parts = append(parts, beforeParts[i])
	}
	parts = append(parts, currentText)
	parts = append(parts, afterParts...)
	return strings.Join(parts, "\n")
}
