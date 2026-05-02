package discord

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/lnyarl/discordlogbot/internal/db"
)

// dmFalse / managePerm / textChannel cached as variables so we can take
// addresses where the discordgo API requires *bool / *int64 fields.
var (
	dmFalse        = false
	managePerm     = int64(discordgo.PermissionManageGuild)
	textChannel    = []discordgo.ChannelType{discordgo.ChannelTypeGuildText}
	logbotCommands = []*discordgo.ApplicationCommand{
		{
			Name:                     "logbot",
			Description:              "로깅 봇 관리 커맨드",
			DefaultMemberPermissions: &managePerm,
			DMPermission:             &dmFalse,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name:        "add",
					Description: "로깅 대상 채널을 추가합니다",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:         "channel",
							Description:  "로깅할 텍스트 채널",
							Type:         discordgo.ApplicationCommandOptionChannel,
							ChannelTypes: textChannel,
							Required:     true,
						},
					},
				},
				{
					Name:        "add_all",
					Description: "모든 공개 텍스트 채널을 로깅 대상에 추가합니다",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
				{
					Name:        "remove",
					Description: "로깅 대상 채널을 제거합니다",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:         "channel",
							Description:  "제거할 텍스트 채널",
							Type:         discordgo.ApplicationCommandOptionChannel,
							ChannelTypes: textChannel,
							Required:     true,
						},
					},
				},
				{
					Name:        "list",
					Description: "현재 로깅 대상 채널 목록을 조회합니다",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
				{
					Name:        "search",
					Description: "로그에서 키워드를 검색합니다",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "keyword",
							Description: "검색할 키워드",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    true,
						},
					},
				},
				{
					Name:        "status",
					Description: "봇 상태 및 총 로그 수를 조회합니다",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
			},
		},
	}
)

// SyncCommands registers /logbot globally. The Discord cache for global
// commands can lag ~1h on rollout, but updates land within minutes.
// Pass a guildID via DISCORD_TEST_GUILD env if you want instant scoped
// registration for development.
func (b *Bot) SyncCommands(testGuildID string) error {
	if b.Session.State == nil || b.Session.State.User == nil {
		return fmt.Errorf("session state not ready — call after Open()")
	}
	appID := b.Session.State.User.ID
	_, err := b.Session.ApplicationCommandBulkOverwrite(appID, testGuildID, logbotCommands)
	return err
}

// onInteractionCreate dispatches /logbot subcommands.
func (b *Bot) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	// Defensive: DMPermission=false should already block this at the
	// Discord layer, but a missing guild_id would corrupt every DB
	// query below — refuse before they run.
	if i.GuildID == "" {
		return
	}
	data := i.ApplicationCommandData()
	if data.Name != "logbot" || len(data.Options) == 0 {
		return
	}
	sub := data.Options[0]
	ctx := context.Background()
	switch sub.Name {
	case "add":
		b.cmdAdd(ctx, s, i, sub)
	case "add_all":
		b.cmdAddAll(ctx, s, i)
	case "remove":
		b.cmdRemove(ctx, s, i, sub)
	case "list":
		b.cmdList(ctx, s, i)
	case "search":
		b.cmdSearch(ctx, s, i, sub)
	case "status":
		b.cmdStatus(ctx, s, i)
	}
}

// ── /logbot add ────────────────────────────────────────────────────────

func (b *Bot) cmdAdd(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	channel := sub.Options[0].ChannelValue(s)
	if channel == nil {
		respond(s, i, "채널을 가져올 수 없습니다.", true)
		return
	}
	guildName := b.guildName(i.GuildID)
	if err := db.AddLogChannel(ctx, b.Pool, i.GuildID, channel.ID, guildName, channel.Name); err != nil {
		slog.Error("AddLogChannel", "err", err)
		respond(s, i, "오류가 발생했습니다.", true)
		return
	}
	b.Channels.Invalidate(i.GuildID)
	// Seed pin cache so the first ChannelPinsUpdate on this channel
	// emits a real diff instead of the "first observation" swallow.
	b.seedChannelPins(channel.ID)
	respond(s, i, fmt.Sprintf("<#%s> 채널을 로깅 대상에 추가했습니다.", channel.ID), true)
}

// ── /logbot add_all ────────────────────────────────────────────────────

func (b *Bot) cmdAddAll(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Defer because iterating + N inserts can exceed Discord's 3-second
	// interaction response window on a busy guild.
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	}); err != nil {
		slog.Error("defer add_all", "err", err)
		return
	}

	guild, err := s.State.Guild(i.GuildID)
	if err != nil || guild == nil {
		followup(s, i, "길드 정보를 가져올 수 없습니다.", true)
		return
	}

	count := 0
	for _, ch := range guild.Channels {
		if ch.Type != discordgo.ChannelTypeGuildText {
			continue
		}
		if everyoneViewDenied(ch, i.GuildID) {
			continue
		}
		if err := db.AddLogChannel(ctx, b.Pool, i.GuildID, ch.ID, guild.Name, ch.Name); err != nil {
			slog.Error("AddLogChannel in add_all", "err", err, "channel_id", ch.ID)
			continue
		}
		// Seed pin cache (same reason as cmdAdd above).
		b.seedChannelPins(ch.ID)
		count++
	}
	b.Channels.Invalidate(i.GuildID)
	// Python's followup.send is public by default; mirror that for the
	// "channel registered" announcement so members can see the change.
	followup(s, i, fmt.Sprintf("공개 텍스트 채널 %d개를 로깅 대상에 추가했습니다.", count), false)
}

// everyoneViewDenied returns true iff the channel has an @everyone
// PermissionOverwrite that denies VIEW_CHANNEL. Mirrors Python's
// `channel.overwrites_for(guild.default_role).view_channel is False`
// — the default_role's id equals the guild id.
//
// We additionally check Type == role to be defensive: while Discord
// API never produces a member overwrite whose id collides with the
// guild id, an explicit type guard makes future spec changes safe.
func everyoneViewDenied(ch *discordgo.Channel, guildID string) bool {
	for _, ow := range ch.PermissionOverwrites {
		if ow.ID == guildID && ow.Type == discordgo.PermissionOverwriteTypeRole {
			return ow.Deny&discordgo.PermissionViewChannel != 0
		}
	}
	return false
}

// ── /logbot remove ─────────────────────────────────────────────────────

func (b *Bot) cmdRemove(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	channel := sub.Options[0].ChannelValue(s)
	if channel == nil {
		respond(s, i, "채널을 가져올 수 없습니다.", true)
		return
	}
	removed, err := db.RemoveLogChannel(ctx, b.Pool, i.GuildID, channel.ID)
	if err != nil {
		slog.Error("RemoveLogChannel", "err", err)
		respond(s, i, "오류가 발생했습니다.", true)
		return
	}
	b.Channels.Invalidate(i.GuildID)
	if removed {
		respond(s, i, fmt.Sprintf("<#%s> 채널을 로깅 대상에서 제거했습니다.", channel.ID), true)
	} else {
		respond(s, i, fmt.Sprintf("<#%s> 채널은 로깅 대상에 없습니다.", channel.ID), true)
	}
}

// ── /logbot list ───────────────────────────────────────────────────────

func (b *Bot) cmdList(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	ids, err := db.GetLogChannels(ctx, b.Pool, i.GuildID)
	if err != nil {
		slog.Error("GetLogChannels", "err", err)
		respond(s, i, "오류가 발생했습니다.", true)
		return
	}
	if len(ids) == 0 {
		respond(s, i, "로깅 대상 채널이 없습니다. `/logbot add #채널`로 채널을 추가하세요.", true)
		return
	}
	lines := make([]string, len(ids))
	for j, id := range ids {
		// Python prints "(알 수 없는 채널: <id>)" when the channel cache
		// can't resolve the id (deleted channel, or the bot lost access).
		// Mirror that fallback so the Discord mention <#id> doesn't render
		// as an unresolved icon for stale rows.
		if ch, err := s.State.Channel(id); err == nil && ch != nil {
			lines[j] = "- <#" + id + ">"
		} else {
			lines[j] = fmt.Sprintf("- (알 수 없는 채널: %s)", id)
		}
	}
	respond(s, i, fmt.Sprintf("**로깅 대상 채널 (%d개):**\n%s", len(lines), strings.Join(lines, "\n")), true)
}

// ── /logbot search ─────────────────────────────────────────────────────

func (b *Bot) cmdSearch(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	keyword := sub.Options[0].StringValue()
	results, err := db.SearchMessages(ctx, b.Pool, i.GuildID, keyword, 20)
	if err != nil {
		slog.Error("SearchMessages", "err", err)
		respond(s, i, "오류가 발생했습니다.", true)
		return
	}
	if len(results) == 0 {
		respond(s, i, fmt.Sprintf("`%s`에 대한 검색 결과가 없습니다.", keyword), true)
		return
	}
	lines := make([]string, len(results))
	for j, r := range results {
		date := r.CreatedAt
		if len(date) > 10 {
			date = date[:10]
		}
		lines[j] = fmt.Sprintf("**#%s** @%s (%s)\n> %s",
			r.ChannelName, r.AuthorName, date, truncateRunes(r.Content, 80))
	}
	text := fmt.Sprintf("**`%s` 검색 결과 (%d건):**\n\n%s",
		keyword, len(results), strings.Join(lines, "\n\n"))
	respond(s, i, truncateRunes(text, 2000), true)
}

// truncateRunes is rune-aware (multibyte-safe) truncation with "..." marker.
func truncateRunes(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	if maxRunes <= 3 {
		return string(r[:maxRunes])
	}
	return string(r[:maxRunes-3]) + "..."
}

// ── /logbot status ─────────────────────────────────────────────────────

func (b *Bot) cmdStatus(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	count, err := db.GetMessageCount(ctx, b.Pool, i.GuildID)
	if err != nil {
		slog.Error("GetMessageCount", "err", err)
		respond(s, i, "오류가 발생했습니다.", true)
		return
	}
	ids, err := db.GetLogChannels(ctx, b.Pool, i.GuildID)
	if err != nil {
		slog.Error("GetLogChannels", "err", err)
		respond(s, i, "오류가 발생했습니다.", true)
		return
	}
	target := "없음 (로깅 중단 상태)"
	if len(ids) > 0 {
		target = fmt.Sprintf("%d개 지정 채널", len(ids))
	}
	embed := &discordgo.MessageEmbed{
		Title: "Logbot 상태",
		Color: 0x3498DB, // discord blue
		Fields: []*discordgo.MessageEmbedField{
			{Name: "총 로그 메시지 수", Value: formatThousands(count) + "개", Inline: true},
			{Name: "로깅 대상", Value: target, Inline: true},
			{Name: "지연시간", Value: fmt.Sprintf("%dms", s.HeartbeatLatency().Milliseconds()), Inline: true},
		},
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		slog.Error("status respond", "err", err)
	}
}

// ── helpers ────────────────────────────────────────────────────────────

func (b *Bot) guildName(guildID string) string {
	if b.Session == nil || b.Session.State == nil {
		return ""
	}
	g, err := b.Session.State.Guild(guildID)
	if err != nil || g == nil {
		return ""
	}
	return g.Name
}

func respond(s *discordgo.Session, i *discordgo.InteractionCreate, content string, ephemeral bool) {
	var flags discordgo.MessageFlags
	if ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content, Flags: flags},
	}); err != nil {
		slog.Error("interaction respond", "err", err)
	}
}

// followup sends a follow-up message after a deferred response. Caller
// chooses ephemeral so we can match Python's per-callsite default (e.g.
// add_all was originally public).
func followup(s *discordgo.Session, i *discordgo.InteractionCreate, content string, ephemeral bool) {
	var flags discordgo.MessageFlags
	if ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}
	if _, err := s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
		Content: content,
		Flags:   flags,
	}); err != nil {
		slog.Error("interaction followup", "err", err)
	}
}

// formatThousands turns an int64 into "1,234,567" — mirrors Python's
// f"{n:,}". Avoids pulling in golang.org/x/text/message for a single
// numeric format.
func formatThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	negative := false
	if len(s) > 0 && s[0] == '-' {
		negative = true
		s = s[1:]
	}
	if len(s) <= 3 {
		if negative {
			return "-" + s
		}
		return s
	}
	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	rem := len(s) % 3
	if rem > 0 {
		b.WriteString(s[:rem])
		if len(s) > rem {
			b.WriteByte(',')
		}
	}
	for i := rem; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteByte(',')
		}
	}
	return b.String()
}
