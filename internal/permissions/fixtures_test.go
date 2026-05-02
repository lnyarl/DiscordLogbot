package permissions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// rawFixture mirrors the JSON shape produced by tools/gen_permission_fixtures.py.
type rawFixture struct {
	Name  string `json:"name"`
	Input struct {
		Guild       *Guild              `json:"guild"`
		MemberRoles []string            `json:"member_roles"`
		Channel     *Channel            `json:"channel"`
		Categories  map[string]*Channel `json:"categories"`
		UserID      string              `json:"user_id"`
		GuildID     string              `json:"guild_id"`
	} `json:"input"`
	Expected bool `json:"expected"`
}

// TestFixturesAgainstPython loads the JSON expected from Python's
// can_view_channel and asserts the Go translation produces identical
// results across the entire fixture set.
func TestFixturesAgainstPython(t *testing.T) {
	path := filepath.Join("testdata", "fixtures.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fixtures missing — run `python tools/gen_permission_fixtures.py` first: %v", err)
	}

	var fixtures []rawFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatalf("decode fixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no fixtures loaded")
	}

	t.Logf("running %d fixtures", len(fixtures))

	mismatches := 0
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			roles := make(map[string]struct{}, len(f.Input.MemberRoles))
			for _, r := range f.Input.MemberRoles {
				roles[r] = struct{}{}
			}
			got := CanViewChannel(
				f.Input.Channel,
				roles,
				f.Input.Guild,
				f.Input.UserID,
				f.Input.GuildID,
				f.Input.Categories,
			)
			if got != f.Expected {
				mismatches++
				t.Errorf("got=%v want=%v (Python expected)", got, f.Expected)
			}
		})
	}
	if mismatches > 0 {
		t.Errorf("Phase 1 동등성 실패: %d/%d 케이스 불일치", mismatches, len(fixtures))
	}
}
