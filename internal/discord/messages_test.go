package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/lnyarl/discordlogbot/internal/db"
)

func ptr(s string) *string { return &s }

func TestAuthorTag(t *testing.T) {
	tests := []struct {
		name string
		user *discordgo.User
		want string
	}{
		{"nil user", nil, ""},
		{"discriminator 0 (migrated)", &discordgo.User{Username: "alice", Discriminator: "0"}, "alice"},
		{"empty discriminator", &discordgo.User{Username: "bob", Discriminator: ""}, "bob"},
		{"legacy 4-digit", &discordgo.User{Username: "carol", Discriminator: "1234"}, "carol#1234"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := authorTag(tt.user); got != tt.want {
				t.Errorf("got=%q want=%q", got, tt.want)
			}
		})
	}
}

func TestAugmentContent(t *testing.T) {
	att := func(name string) db.Attachment { return db.Attachment{Filename: name, LocalPath: ptr("p")} }
	stk := func(name string) *discordgo.StickerItem { return &discordgo.StickerItem{Name: name} }

	tests := []struct {
		name     string
		content  string
		atts     []db.Attachment
		stickers []*discordgo.StickerItem
		want     string
	}{
		{"plain content only", "hello", nil, nil, "hello"},
		{"empty everything", "", nil, nil, ""},
		{"empty + one attachment", "", []db.Attachment{att("a.png")}, nil, "[a.png]"},
		{"content + one attachment", "hi", []db.Attachment{att("a.png")}, nil, "hi [a.png]"},
		{"content + two attachments", "hi", []db.Attachment{att("a.png"), att("b.gif")}, nil, "hi [a.png] [b.gif]"},
		{"empty + two attachments", "", []db.Attachment{att("a.png"), att("b.gif")}, nil, "[a.png] [b.gif]"},
		{"content + sticker", "hi", nil, []*discordgo.StickerItem{stk("wave")}, "hi [스티커: wave]"},
		{"empty + sticker", "", nil, []*discordgo.StickerItem{stk("wave")}, "[스티커: wave]"},
		{
			"content + attachment + sticker",
			"hi", []db.Attachment{att("a.png")}, []*discordgo.StickerItem{stk("wave")},
			"hi [a.png] [스티커: wave]",
		},
		{
			"empty + attachment + sticker",
			"", []db.Attachment{att("a.png")}, []*discordgo.StickerItem{stk("wave")},
			"[a.png] [스티커: wave]",
		},
		{
			"two stickers",
			"hi", nil, []*discordgo.StickerItem{stk("wave"), stk("thumb")},
			"hi [스티커: wave] [스티커: thumb]",
		},
		{
			"trailing-space content + sticker (no double space)",
			"hi ", nil, []*discordgo.StickerItem{stk("wave")},
			"hi [스티커: wave]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := augmentContent(tt.content, tt.atts, tt.stickers)
			if got != tt.want {
				t.Errorf("\ngot:  %q\nwant: %q", got, tt.want)
			}
		})
	}
}
