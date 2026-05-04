package discord

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
)

// Python's discord.py 2.7 produces these strings for the relevant enum
// values via str(enum_member). Some enums override __str__ to return the
// bare name ("text"); the rest fall through to the default Python repr
// "ClassName.member" ("AuditLogAction.kick"). We replicate both shapes
// exactly so our guild_events.details JSON stays comparable to the rows
// the Python bot has already written.
//
// Source of truth: confirmed by running the live discord.py 2.7.1 in a
// container during Phase 4 review (see commit message of the review-fix).

// ── Enums whose str() returns the bare name ──────────────────────────────

func channelTypeStr(t discordgo.ChannelType) string {
	switch t {
	case discordgo.ChannelTypeGuildText:
		return "text"
	case discordgo.ChannelTypeDM:
		return "private"
	case discordgo.ChannelTypeGuildVoice:
		return "voice"
	case discordgo.ChannelTypeGroupDM:
		return "group"
	case discordgo.ChannelTypeGuildCategory:
		return "category"
	case discordgo.ChannelTypeGuildNews:
		return "news"
	case discordgo.ChannelTypeGuildNewsThread:
		return "news_thread"
	case discordgo.ChannelTypeGuildPublicThread:
		return "public_thread"
	case discordgo.ChannelTypeGuildPrivateThread:
		return "private_thread"
	case discordgo.ChannelTypeGuildStageVoice:
		return "stage_voice"
	case discordgo.ChannelTypeGuildForum:
		return "forum"
	case discordgo.ChannelTypeGuildMedia:
		return "media"
	}
	// Unknown / future types fall back to the int — Python would do the
	// same when its enum can't represent the value.
	return fmt.Sprintf("%d", int(t))
}

func verificationLevelStr(v discordgo.VerificationLevel) string {
	switch v {
	case 0:
		return "none"
	case 1:
		return "low"
	case 2:
		return "medium"
	case 3:
		return "high"
	case 4:
		return "highest"
	}
	return fmt.Sprintf("%d", int(v))
}

// contentFilterStr maps discord.py's ContentFilter (== Discord's
// ExplicitContentFilterLevel).
func contentFilterStr(f discordgo.ExplicitContentFilterLevel) string {
	switch f {
	case 0:
		return "disabled"
	case 1:
		return "no_role"
	case 2:
		return "all_members"
	}
	return fmt.Sprintf("%d", int(f))
}

// ── Enums whose str() falls through to the default Python repr ───────────

func notificationLevelStr(n discordgo.MessageNotifications) string {
	var name string
	switch n {
	case 0:
		name = "all_messages"
	case 1:
		name = "only_mentions"
	default:
		return fmt.Sprintf("%d", int(n))
	}
	return "NotificationLevel." + name
}

// auditLogActionStr maps Discord audit-log action codes to the
// "AuditLogAction.<name>" repr discord.py produces. Names taken from
// discord.py 2.7.1 (the production runtime).
func auditLogActionStr(a discordgo.AuditLogAction) string {
	name := auditLogActionName(int(a))
	if name == "" {
		return fmt.Sprintf("%d", int(a))
	}
	return "AuditLogAction." + name
}

func auditLogActionName(code int) string {
	switch code {
	case 1:
		return "guild_update"
	case 10:
		return "channel_create"
	case 11:
		return "channel_update"
	case 12:
		return "channel_delete"
	case 13:
		return "overwrite_create"
	case 14:
		return "overwrite_update"
	case 15:
		return "overwrite_delete"
	case 20:
		return "kick"
	case 21:
		return "member_prune"
	case 22:
		return "ban"
	case 23:
		return "unban"
	case 24:
		return "member_update"
	case 25:
		return "member_role_update"
	case 26:
		return "member_move"
	case 27:
		return "member_disconnect"
	case 28:
		return "bot_add"
	case 30:
		return "role_create"
	case 31:
		return "role_update"
	case 32:
		return "role_delete"
	case 40:
		return "invite_create"
	case 41:
		return "invite_update"
	case 42:
		return "invite_delete"
	case 50:
		return "webhook_create"
	case 51:
		return "webhook_update"
	case 52:
		return "webhook_delete"
	case 60:
		return "emoji_create"
	case 61:
		return "emoji_update"
	case 62:
		return "emoji_delete"
	case 72:
		return "message_delete"
	case 73:
		return "message_bulk_delete"
	case 74:
		return "message_pin"
	case 75:
		return "message_unpin"
	case 80:
		return "integration_create"
	case 81:
		return "integration_update"
	case 82:
		return "integration_delete"
	case 83:
		return "stage_instance_create"
	case 84:
		return "stage_instance_update"
	case 85:
		return "stage_instance_delete"
	case 90:
		return "sticker_create"
	case 91:
		return "sticker_update"
	case 92:
		return "sticker_delete"
	case 100:
		return "scheduled_event_create"
	case 101:
		return "scheduled_event_update"
	case 102:
		return "scheduled_event_delete"
	case 110:
		return "thread_create"
	case 111:
		return "thread_update"
	case 112:
		return "thread_delete"
	case 121:
		return "app_command_permission_update"
	case 130:
		return "soundboard_sound_create"
	case 131:
		return "soundboard_sound_update"
	case 132:
		return "soundboard_sound_delete"
	case 140:
		return "automod_rule_create"
	case 141:
		return "automod_rule_update"
	case 142:
		return "automod_rule_delete"
	case 143:
		return "automod_block_message"
	case 144:
		return "automod_flag_message"
	case 145:
		return "automod_timeout_member"
	case 146:
		return "automod_quarantine_user"
	case 150:
		return "creator_monetization_request_created"
	case 151:
		return "creator_monetization_terms_accepted"
	case 163:
		return "onboarding_prompt_create"
	case 164:
		return "onboarding_prompt_update"
	case 165:
		return "onboarding_prompt_delete"
	case 166:
		return "onboarding_create"
	case 167:
		return "onboarding_update"
	case 190:
		return "home_settings_create"
	case 191:
		return "home_settings_update"
	}
	return ""
}

func autoModTriggerTypeStr(t discordgo.AutoModerationRuleTriggerType) string {
	var name string
	switch t {
	case 1:
		name = "keyword"
	case 2:
		name = "harmful_link"
	case 3:
		name = "spam"
	case 4:
		name = "keyword_preset"
	case 5:
		name = "mention_spam"
	case 6:
		name = "member_profile"
	default:
		return fmt.Sprintf("%d", int(t))
	}
	return "AutoModRuleTriggerType." + name
}

func autoModActionTypeStr(t discordgo.AutoModerationActionType) string {
	var name string
	switch t {
	case 1:
		name = "block_message"
	case 2:
		name = "send_alert_message"
	case 3:
		name = "timeout"
	case 4:
		name = "block_member_interactions"
	default:
		return fmt.Sprintf("%d", int(t))
	}
	return "AutoModRuleActionType." + name
}

func eventStatusStr(s discordgo.GuildScheduledEventStatus) string {
	var name string
	switch s {
	case 1:
		name = "scheduled"
	case 2:
		name = "active"
	case 3:
		name = "completed"
	case 4:
		name = "canceled"
	default:
		return fmt.Sprintf("%d", int(s))
	}
	return "EventStatus." + name
}

func stagePrivacyLevelStr(p discordgo.StageInstancePrivacyLevel) string {
	var name string
	switch p {
	case 2:
		name = "guild_only"
	default:
		return fmt.Sprintf("%d", int(p))
	}
	return "PrivacyLevel." + name
}

func entityTypeStr(t discordgo.GuildScheduledEventEntityType) string {
	var name string
	switch t {
	case 1:
		name = "stage_instance"
	case 2:
		name = "voice"
	case 3:
		name = "external"
	default:
		return fmt.Sprintf("%d", int(t))
	}
	return "EntityType." + name
}
