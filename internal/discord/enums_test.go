package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

// These goldens were captured against discord.py 2.7.1 in a python:3.12
// container during Phase 4 review. Any divergence here = silent schema
// drift between Python and Go bots writing to the same table.

func TestChannelTypeStr(t *testing.T) {
	tests := []struct {
		t    discordgo.ChannelType
		want string
	}{
		{discordgo.ChannelTypeGuildText, "text"},
		{discordgo.ChannelTypeDM, "private"},
		{discordgo.ChannelTypeGuildVoice, "voice"},
		{discordgo.ChannelTypeGroupDM, "group"},
		{discordgo.ChannelTypeGuildCategory, "category"},
		{discordgo.ChannelTypeGuildNews, "news"},
		{discordgo.ChannelTypeGuildNewsThread, "news_thread"},
		{discordgo.ChannelTypeGuildPublicThread, "public_thread"},
		{discordgo.ChannelTypeGuildPrivateThread, "private_thread"},
		{discordgo.ChannelTypeGuildStageVoice, "stage_voice"},
		{discordgo.ChannelTypeGuildForum, "forum"},
		{discordgo.ChannelTypeGuildMedia, "media"},
		{discordgo.ChannelType(99), "99"},
	}
	for _, tt := range tests {
		if got := channelTypeStr(tt.t); got != tt.want {
			t.Errorf("channelTypeStr(%d) = %q, want %q", tt.t, got, tt.want)
		}
	}
}

func TestVerificationLevelStr(t *testing.T) {
	tests := map[discordgo.VerificationLevel]string{
		0: "none", 1: "low", 2: "medium", 3: "high", 4: "highest",
	}
	for v, want := range tests {
		if got := verificationLevelStr(v); got != want {
			t.Errorf("got=%q want=%q", got, want)
		}
	}
}

func TestContentFilterStr(t *testing.T) {
	tests := map[discordgo.ExplicitContentFilterLevel]string{
		0: "disabled", 1: "no_role", 2: "all_members",
	}
	for v, want := range tests {
		if got := contentFilterStr(v); got != want {
			t.Errorf("got=%q want=%q", got, want)
		}
	}
}

func TestNotificationLevelStr(t *testing.T) {
	if got := notificationLevelStr(0); got != "NotificationLevel.all_messages" {
		t.Errorf("got=%q", got)
	}
	if got := notificationLevelStr(1); got != "NotificationLevel.only_mentions" {
		t.Errorf("got=%q", got)
	}
}

func TestAuditLogActionStr(t *testing.T) {
	// Spot-check a handful — full list is in enums.go.
	tests := map[discordgo.AuditLogAction]string{
		1:   "AuditLogAction.guild_update",
		20:  "AuditLogAction.kick",
		22:  "AuditLogAction.ban",
		25:  "AuditLogAction.member_role_update",
		73:  "AuditLogAction.message_bulk_delete",
		145: "AuditLogAction.automod_timeout_member",
	}
	for v, want := range tests {
		if got := auditLogActionStr(v); got != want {
			t.Errorf("auditLogActionStr(%d) = %q, want %q", v, got, want)
		}
	}
	if got := auditLogActionStr(9999); got != "9999" {
		t.Errorf("unknown action = %q, want %q", got, "9999")
	}
}

func TestAutoModTriggerTypeStr(t *testing.T) {
	tests := map[discordgo.AutoModerationRuleTriggerType]string{
		1: "AutoModRuleTriggerType.keyword",
		2: "AutoModRuleTriggerType.harmful_link",
		3: "AutoModRuleTriggerType.spam",
		4: "AutoModRuleTriggerType.keyword_preset",
		5: "AutoModRuleTriggerType.mention_spam",
		6: "AutoModRuleTriggerType.member_profile",
	}
	for v, want := range tests {
		if got := autoModTriggerTypeStr(v); got != want {
			t.Errorf("got=%q want=%q", got, want)
		}
	}
}

func TestAutoModActionTypeStr(t *testing.T) {
	tests := map[discordgo.AutoModerationActionType]string{
		1: "AutoModRuleActionType.block_message",
		2: "AutoModRuleActionType.send_alert_message",
		3: "AutoModRuleActionType.timeout",
		4: "AutoModRuleActionType.block_member_interactions",
	}
	for v, want := range tests {
		if got := autoModActionTypeStr(v); got != want {
			t.Errorf("got=%q want=%q", got, want)
		}
	}
}

func TestEventStatusStr(t *testing.T) {
	tests := map[discordgo.GuildScheduledEventStatus]string{
		1: "EventStatus.scheduled",
		2: "EventStatus.active",
		3: "EventStatus.completed",
		4: "EventStatus.canceled",
	}
	for v, want := range tests {
		if got := eventStatusStr(v); got != want {
			t.Errorf("got=%q want=%q", got, want)
		}
	}
}

func TestStagePrivacyLevelStr(t *testing.T) {
	if got := stagePrivacyLevelStr(2); got != "PrivacyLevel.guild_only" {
		t.Errorf("got=%q", got)
	}
	if got := stagePrivacyLevelStr(99); got != "99" {
		t.Errorf("unknown level = %q", got)
	}
}

func TestEntityTypeStr(t *testing.T) {
	tests := map[discordgo.GuildScheduledEventEntityType]string{
		1: "EntityType.stage_instance",
		2: "EntityType.voice",
		3: "EntityType.external",
	}
	for v, want := range tests {
		if got := entityTypeStr(v); got != want {
			t.Errorf("got=%q want=%q", got, want)
		}
	}
}

// Extended autoModTriggerName coverage for newly added trigger types
// (mention_spam = 5, member_profile = 6).
func TestAutoModTriggerName_Extended(t *testing.T) {
	if got := autoModTriggerName(5); got != "mention_spam" {
		t.Errorf("got=%q", got)
	}
	if got := autoModTriggerName(6); got != "member_profile" {
		t.Errorf("got=%q", got)
	}
}
