package permissions

import (
	"strconv"
	"testing"
)

const (
	tGuild = "100"
	tUser  = "200"
)

func roleSet(ids ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

func u(v uint64) string { return strconv.FormatUint(v, 10) }

func TestCanViewChannel_SpecCases(t *testing.T) {
	view := u(PermViewChannel)
	admin := u(PermAdmin)

	tests := []struct {
		name        string
		guild       *Guild
		memberRoles map[string]struct{}
		channel     *Channel
		categories  map[string]*Channel
		want        bool
	}{
		{
			name:        "owner always sees",
			guild:       &Guild{OwnerID: tUser, Roles: []Role{{ID: tGuild, Permissions: "0"}}},
			memberRoles: roleSet(),
			channel:     &Channel{ID: "1", Type: 0},
			want:        true,
		},
		{
			name: "everyone has VIEW_CHANNEL by default",
			guild: &Guild{Roles: []Role{
				{ID: tGuild, Permissions: view},
			}},
			memberRoles: roleSet(),
			channel:     &Channel{ID: "1", Type: 0},
			want:        true,
		},
		{
			name: "no roles, no everyone permission → false",
			guild: &Guild{Roles: []Role{
				{ID: tGuild, Permissions: "0"},
			}},
			memberRoles: roleSet(),
			channel:     &Channel{ID: "1", Type: 0},
			want:        false,
		},
		{
			name: "everyone deny VIEW via channel overwrite",
			guild: &Guild{Roles: []Role{
				{ID: tGuild, Permissions: view},
			}},
			memberRoles: roleSet(),
			channel: &Channel{ID: "1", Type: 0, PermissionOverwrites: []Overwrite{
				{ID: tGuild, Deny: view},
			}},
			want: false,
		},
		{
			name: "role allow overrides everyone deny",
			guild: &Guild{Roles: []Role{
				{ID: tGuild, Permissions: "0"},
				{ID: "role-a", Permissions: "0"},
			}},
			memberRoles: roleSet("role-a"),
			channel: &Channel{ID: "1", Type: 0, PermissionOverwrites: []Overwrite{
				{ID: tGuild, Deny: view},
				{ID: "role-a", Allow: view},
			}},
			want: true,
		},
		{
			name: "member deny overrides role allow",
			guild: &Guild{Roles: []Role{
				{ID: tGuild, Permissions: view},
				{ID: "role-a", Permissions: "0"},
			}},
			memberRoles: roleSet("role-a"),
			channel: &Channel{ID: "1", Type: 0, PermissionOverwrites: []Overwrite{
				{ID: "role-a", Allow: view},
				{ID: tUser, Deny: view},
			}},
			want: false,
		},
		{
			name: "member allow overrides role deny",
			guild: &Guild{Roles: []Role{
				{ID: tGuild, Permissions: view},
				{ID: "role-a", Permissions: "0"},
			}},
			memberRoles: roleSet("role-a"),
			channel: &Channel{ID: "1", Type: 0, PermissionOverwrites: []Overwrite{
				{ID: "role-a", Deny: view},
				{ID: tUser, Allow: view},
			}},
			want: true,
		},
		{
			name: "ADMINISTRATOR bit grants all (channel deny ignored)",
			guild: &Guild{Roles: []Role{
				{ID: tGuild, Permissions: "0"},
				{ID: "role-a", Permissions: admin},
			}},
			memberRoles: roleSet("role-a"),
			channel: &Channel{ID: "1", Type: 0, PermissionOverwrites: []Overwrite{
				{ID: tGuild, Deny: view},
			}},
			want: true,
		},
		{
			name: "category deny inherits to channel",
			guild: &Guild{Roles: []Role{
				{ID: tGuild, Permissions: view},
			}},
			memberRoles: roleSet(),
			channel:     &Channel{ID: "1", Type: 0, ParentID: "cat-1"},
			categories: map[string]*Channel{
				"cat-1": {ID: "cat-1", Type: 4, PermissionOverwrites: []Overwrite{
					{ID: tGuild, Deny: view},
				}},
			},
			want: false,
		},
		{
			name: "channel allow overrides category deny",
			guild: &Guild{Roles: []Role{
				{ID: tGuild, Permissions: view},
			}},
			memberRoles: roleSet(),
			channel: &Channel{ID: "1", Type: 0, ParentID: "cat-1", PermissionOverwrites: []Overwrite{
				{ID: tGuild, Allow: view},
			}},
			categories: map[string]*Channel{
				"cat-1": {ID: "cat-1", Type: 4, PermissionOverwrites: []Overwrite{
					{ID: tGuild, Deny: view},
				}},
			},
			want: true,
		},
		{
			name: "two roles deny+allow same bit → role allow wins (spec batch)",
			guild: &Guild{Roles: []Role{
				{ID: tGuild, Permissions: view},
				{ID: "r-deny", Permissions: "0"},
				{ID: "r-allow", Permissions: "0"},
			}},
			memberRoles: roleSet("r-deny", "r-allow"),
			channel: &Channel{ID: "1", Type: 0, PermissionOverwrites: []Overwrite{
				{ID: "r-deny", Deny: view},
				{ID: "r-allow", Allow: view},
			}},
			want: true,
		},
		{
			name: "role permission grants VIEW even if everyone is 0",
			guild: &Guild{Roles: []Role{
				{ID: tGuild, Permissions: "0"},
				{ID: "role-a", Permissions: view},
			}},
			memberRoles: roleSet("role-a"),
			channel:     &Channel{ID: "1", Type: 0},
			want:        true,
		},
		{
			name: "unknown parent_id falls through to channel overwrites only",
			guild: &Guild{Roles: []Role{
				{ID: tGuild, Permissions: view},
			}},
			memberRoles: roleSet(),
			channel:     &Channel{ID: "1", Type: 0, ParentID: "missing-cat"},
			categories:  map[string]*Channel{},
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CanViewChannel(tt.channel, tt.memberRoles, tt.guild, tUser, tGuild, tt.categories)
			if got != tt.want {
				t.Fatalf("got=%v want=%v", got, tt.want)
			}
		})
	}
}
