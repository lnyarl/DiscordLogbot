package permissions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"testing"
)

// mockGuild defines a single guild's state in the mock Discord.
type mockGuild struct {
	Info       Guild
	Member     Member
	NotInGuild bool // true → member endpoint returns 404
	Channels   []Channel
}

type mockState struct {
	UserID    string
	BotGuilds []BotGuild
	Guilds    map[string]*mockGuild
}

// newMockDiscord returns an httptest server that serves the four endpoints
// ComputeAccessibleChannels uses. Path patterns rely on Go 1.22+ ServeMux.
func newMockDiscord(t *testing.T, s *mockState) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("GET /users/@me/guilds", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.BotGuilds)
	})
	mux.HandleFunc("GET /guilds/{guild}/members/{user}", func(w http.ResponseWriter, r *http.Request) {
		gid := r.PathValue("guild")
		uid := r.PathValue("user")
		g, ok := s.Guilds[gid]
		if !ok || uid != s.UserID || g.NotInGuild {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, g.Member)
	})
	mux.HandleFunc("GET /guilds/{guild}/channels", func(w http.ResponseWriter, r *http.Request) {
		g, ok := s.Guilds[r.PathValue("guild")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, g.Channels)
	})
	mux.HandleFunc("GET /guilds/{guild}", func(w http.ResponseWriter, r *http.Request) {
		g, ok := s.Guilds[r.PathValue("guild")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, g.Info)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// ── 시나리오별 통합 테스트 ──────────────────────────────────────────────────

func TestComputeAccessibleChannels(t *testing.T) {
	view := strconv.FormatUint(PermViewChannel, 10)
	admin := strconv.FormatUint(PermAdmin, 10)
	const (
		userID  = "user-1"
		guildA  = "guild-a"
		guildB  = "guild-b"
		ownerID = "owner-1"
	)

	type tc struct {
		name      string
		state     *mockState
		wantIDs   []string // expected accessible channel IDs (sorted)
		wantNames []string // expected channel names (parallel to wantIDs)
	}

	tests := []tc{
		{
			name: "owner sees every text and announcement channel; voice/category filtered",
			state: &mockState{
				UserID:    userID,
				BotGuilds: []BotGuild{{ID: guildA, Name: "Alpha"}},
				Guilds: map[string]*mockGuild{
					guildA: {
						Info: Guild{
							ID: guildA, Name: "Alpha", OwnerID: userID,
							Roles: []Role{{ID: guildA, Permissions: "0"}},
						},
						Member: Member{Roles: []string{}},
						Channels: []Channel{
							{ID: "c1", Name: "general", Type: channelTypeText},
							{ID: "c2", Name: "news", Type: channelTypeAnnouncement},
							{ID: "v1", Name: "voice", Type: 2},
							{ID: "cat1", Name: "Cat", Type: channelTypeCategory},
						},
					},
				},
			},
			wantIDs:   []string{"c1", "c2"},
			wantNames: []string{"general", "news"},
		},
		{
			name: "regular user with @everyone VIEW sees most; one channel deny excludes",
			state: &mockState{
				UserID:    userID,
				BotGuilds: []BotGuild{{ID: guildA, Name: "Alpha"}},
				Guilds: map[string]*mockGuild{
					guildA: {
						Info: Guild{
							ID: guildA, Name: "Alpha", OwnerID: ownerID,
							Roles: []Role{{ID: guildA, Permissions: view}},
						},
						Member: Member{Roles: []string{}},
						Channels: []Channel{
							{ID: "c1", Name: "general", Type: channelTypeText},
							{ID: "c2", Name: "secret", Type: channelTypeText, PermissionOverwrites: []Overwrite{
								{ID: guildA, Deny: view},
							}},
							{ID: "c3", Name: "lobby", Type: channelTypeText},
						},
					},
				},
			},
			wantIDs:   []string{"c1", "c3"},
			wantNames: []string{"general", "lobby"},
		},
		{
			name: "ADMIN role bypasses every channel deny",
			state: &mockState{
				UserID:    userID,
				BotGuilds: []BotGuild{{ID: guildA, Name: "Alpha"}},
				Guilds: map[string]*mockGuild{
					guildA: {
						Info: Guild{
							ID: guildA, Name: "Alpha", OwnerID: ownerID,
							Roles: []Role{
								{ID: guildA, Permissions: "0"},
								{ID: "role-admin", Permissions: admin},
							},
						},
						Member: Member{Roles: []string{"role-admin"}},
						Channels: []Channel{
							{ID: "c1", Name: "general", Type: channelTypeText, PermissionOverwrites: []Overwrite{
								{ID: guildA, Deny: view},
							}},
							{ID: "c2", Name: "private", Type: channelTypeText, PermissionOverwrites: []Overwrite{
								{ID: guildA, Deny: view},
							}},
						},
					},
				},
			},
			wantIDs:   []string{"c1", "c2"},
			wantNames: []string{"general", "private"},
		},
		{
			name: "user not in guild → empty result for that guild",
			state: &mockState{
				UserID:    userID,
				BotGuilds: []BotGuild{{ID: guildA, Name: "Alpha"}},
				Guilds: map[string]*mockGuild{
					guildA: {
						NotInGuild: true,
						Info: Guild{
							ID: guildA, Name: "Alpha", OwnerID: ownerID,
							Roles: []Role{{ID: guildA, Permissions: view}},
						},
						Channels: []Channel{
							{ID: "c1", Name: "general", Type: channelTypeText},
						},
					},
				},
			},
			wantIDs:   nil,
			wantNames: nil,
		},
		{
			name: "multi-guild: bot in two, user in one",
			state: &mockState{
				UserID: userID,
				BotGuilds: []BotGuild{
					{ID: guildA, Name: "Alpha"},
					{ID: guildB, Name: "Beta"},
				},
				Guilds: map[string]*mockGuild{
					guildA: {
						Info: Guild{
							ID: guildA, Name: "Alpha", OwnerID: ownerID,
							Roles: []Role{{ID: guildA, Permissions: view}},
						},
						Member: Member{Roles: []string{}},
						Channels: []Channel{
							{ID: "a1", Name: "alpha-general", Type: channelTypeText},
						},
					},
					guildB: {
						NotInGuild: true,
						Info: Guild{
							ID: guildB, Name: "Beta", OwnerID: ownerID,
							Roles: []Role{{ID: guildB, Permissions: view}},
						},
						Channels: []Channel{
							{ID: "b1", Name: "beta-general", Type: channelTypeText},
						},
					},
				},
			},
			wantIDs:   []string{"a1"},
			wantNames: []string{"alpha-general"},
		},
		{
			name: "category deny inherits → channel allow on member rescues",
			state: &mockState{
				UserID:    userID,
				BotGuilds: []BotGuild{{ID: guildA, Name: "Alpha"}},
				Guilds: map[string]*mockGuild{
					guildA: {
						Info: Guild{
							ID: guildA, Name: "Alpha", OwnerID: ownerID,
							Roles: []Role{{ID: guildA, Permissions: view}},
						},
						Member: Member{Roles: []string{}},
						Channels: []Channel{
							{ID: "cat1", Name: "Locked", Type: channelTypeCategory, PermissionOverwrites: []Overwrite{
								{ID: guildA, Deny: view},
							}},
							{ID: "c1", Name: "rescued", Type: channelTypeText, ParentID: "cat1", PermissionOverwrites: []Overwrite{
								{ID: userID, Type: 1, Allow: view},
							}},
							{ID: "c2", Name: "stays-hidden", Type: channelTypeText, ParentID: "cat1"},
						},
					},
				},
			},
			wantIDs:   []string{"c1"},
			wantNames: []string{"rescued"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newMockDiscord(t, tt.state)
			c := NewClient(ts.URL, "fake-token")
			got, err := ComputeAccessibleChannels(context.Background(), c, tt.state.UserID)
			if err != nil {
				t.Fatalf("ComputeAccessibleChannels: %v", err)
			}

			gotIDs := make([]string, len(got))
			gotNames := make([]string, len(got))
			for i, ch := range got {
				gotIDs[i] = ch.ChannelID
				gotNames[i] = ch.ChannelName
			}
			sortPair(gotIDs, gotNames)

			wantIDs := append([]string(nil), tt.wantIDs...)
			wantNames := append([]string(nil), tt.wantNames...)
			sortPair(wantIDs, wantNames)

			if !equalStr(gotIDs, wantIDs) {
				t.Errorf("channel ids: got %v want %v", gotIDs, wantIDs)
			}
			if !equalStr(gotNames, wantNames) {
				t.Errorf("channel names: got %v want %v", gotNames, wantNames)
			}
		})
	}
}

func sortPair(ids, names []string) {
	idx := make([]int, len(ids))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return ids[idx[a]] < ids[idx[b]] })
	sortedIDs := make([]string, len(ids))
	sortedNames := make([]string, len(names))
	for i, j := range idx {
		sortedIDs[i] = ids[j]
		sortedNames[i] = names[j]
	}
	copy(ids, sortedIDs)
	copy(names, sortedNames)
}

func equalStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
