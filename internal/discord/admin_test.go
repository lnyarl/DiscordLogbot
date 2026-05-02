package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestEveryoneViewDenied(t *testing.T) {
	const guildID = "G1"
	view := int64(discordgo.PermissionViewChannel)
	other := int64(discordgo.PermissionManageMessages)

	tests := []struct {
		name string
		ch   *discordgo.Channel
		want bool
	}{
		{
			"no overwrites",
			&discordgo.Channel{},
			false,
		},
		{
			"@everyone deny VIEW",
			&discordgo.Channel{PermissionOverwrites: []*discordgo.PermissionOverwrite{
				{ID: guildID, Deny: view},
			}},
			true,
		},
		{
			"@everyone allow VIEW (no deny)",
			&discordgo.Channel{PermissionOverwrites: []*discordgo.PermissionOverwrite{
				{ID: guildID, Allow: view},
			}},
			false,
		},
		{
			"@everyone deny something else (not VIEW)",
			&discordgo.Channel{PermissionOverwrites: []*discordgo.PermissionOverwrite{
				{ID: guildID, Deny: other},
			}},
			false,
		},
		{
			"role deny VIEW (not @everyone) — public still",
			&discordgo.Channel{PermissionOverwrites: []*discordgo.PermissionOverwrite{
				{ID: "role-x", Deny: view},
			}},
			false,
		},
		{
			"@everyone deny VIEW + other allow",
			&discordgo.Channel{PermissionOverwrites: []*discordgo.PermissionOverwrite{
				{ID: guildID, Deny: view, Allow: other},
			}},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := everyoneViewDenied(tt.ch, guildID); got != tt.want {
				t.Errorf("got=%v want=%v", got, tt.want)
			}
		})
	}
}

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"shorter than max", "hi", 10, "hi"},
		{"exactly max", "hello", 5, "hello"},
		{"ascii truncate adds ellipsis", "abcdefghij", 8, "abcde..."},
		{"korean truncate is rune-safe", "안녕하세요반갑습니다", 6, "안녕하..."},
		{"max <= 3 returns first runes only", "abcdef", 3, "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateRunes(tt.in, tt.max)
			if got != tt.want {
				t.Errorf("got=%q want=%q", got, tt.want)
			}
		})
	}
}

func TestLogbotCommands_TopLevelShape(t *testing.T) {
	if len(logbotCommands) != 1 {
		t.Fatalf("expected 1 top-level command, got %d", len(logbotCommands))
	}
	c := logbotCommands[0]
	if c.Name != "logbot" {
		t.Errorf("name=%q want=logbot", c.Name)
	}
	if c.DefaultMemberPermissions == nil || *c.DefaultMemberPermissions != int64(discordgo.PermissionManageGuild) {
		t.Error("DefaultMemberPermissions not set to ManageGuild")
	}
	if c.DMPermission == nil || *c.DMPermission != false {
		t.Error("DMPermission not set to false")
	}
	wantSubs := []string{"add", "add_all", "remove", "list", "search", "status"}
	if len(c.Options) != len(wantSubs) {
		t.Fatalf("subcommand count: got=%d want=%d", len(c.Options), len(wantSubs))
	}
	for i, opt := range c.Options {
		if opt.Type != discordgo.ApplicationCommandOptionSubCommand {
			t.Errorf("[%d] type=%v want SubCommand", i, opt.Type)
		}
		if opt.Name != wantSubs[i] {
			t.Errorf("[%d] name=%q want=%q", i, opt.Name, wantSubs[i])
		}
	}
}
