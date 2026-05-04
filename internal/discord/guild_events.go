package discord

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/lnyarl/discordlogbot/internal/db"
)

// ── Shadow caches for change diffs ───────────────────────────────────────
//
// discordgo doesn't ship a Before snapshot for many Update events
// (GuildUpdate, GuildRoleUpdate, UserUpdate, GuildEmojisUpdate,
// GuildStickersUpdate). discord.py supplies these via its built-in State,
// but our State accessors return the AFTER value by the time the handler
// runs. We shadow the affected entities to recover the before/after diff.

type guildShadow struct {
	mu    sync.RWMutex
	byID  map[string]*discordgo.Guild
}

func newGuildShadow() *guildShadow {
	return &guildShadow{byID: make(map[string]*discordgo.Guild)}
}

func (s *guildShadow) Get(id string) *discordgo.Guild {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byID[id]
}

func (s *guildShadow) Set(g *discordgo.Guild) {
	s.mu.Lock()
	s.byID[g.ID] = g
	s.mu.Unlock()
}

func (s *guildShadow) Delete(id string) {
	s.mu.Lock()
	delete(s.byID, id)
	s.mu.Unlock()
}

type roleShadow struct {
	mu     sync.RWMutex
	byPair map[string]*discordgo.Role // key = guildID + ":" + roleID
}

func newRoleShadow() *roleShadow {
	return &roleShadow{byPair: make(map[string]*discordgo.Role)}
}

func roleKey(guildID, roleID string) string { return guildID + ":" + roleID }

func (s *roleShadow) Get(guildID, roleID string) *discordgo.Role {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byPair[roleKey(guildID, roleID)]
}

func (s *roleShadow) Set(guildID string, r *discordgo.Role) {
	s.mu.Lock()
	s.byPair[roleKey(guildID, r.ID)] = r
	s.mu.Unlock()
}

func (s *roleShadow) Delete(guildID, roleID string) {
	s.mu.Lock()
	delete(s.byPair, roleKey(guildID, roleID))
	s.mu.Unlock()
}

type emojiShadow struct {
	mu      sync.RWMutex
	byGuild map[string]map[string]*discordgo.Emoji
}

func newEmojiShadow() *emojiShadow {
	return &emojiShadow{byGuild: make(map[string]map[string]*discordgo.Emoji)}
}

func (s *emojiShadow) Get(guildID string) map[string]*discordgo.Emoji {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m := s.byGuild[guildID]
	if m == nil {
		return nil
	}
	out := make(map[string]*discordgo.Emoji, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s *emojiShadow) Replace(guildID string, list []*discordgo.Emoji) {
	m := make(map[string]*discordgo.Emoji, len(list))
	for _, e := range list {
		m[e.ID] = e
	}
	s.mu.Lock()
	s.byGuild[guildID] = m
	s.mu.Unlock()
}

type stickerShadow struct {
	mu      sync.RWMutex
	byGuild map[string]map[string]*discordgo.Sticker
}

func newStickerShadow() *stickerShadow {
	return &stickerShadow{byGuild: make(map[string]map[string]*discordgo.Sticker)}
}

func (s *stickerShadow) Get(guildID string) map[string]*discordgo.Sticker {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m := s.byGuild[guildID]
	if m == nil {
		return nil
	}
	out := make(map[string]*discordgo.Sticker, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s *stickerShadow) Replace(guildID string, list []*discordgo.Sticker) {
	m := make(map[string]*discordgo.Sticker, len(list))
	for _, st := range list {
		m[st.ID] = st
	}
	s.mu.Lock()
	s.byGuild[guildID] = m
	s.mu.Unlock()
}

type userShadow struct {
	mu   sync.RWMutex
	byID map[string]*discordgo.User
}

func newUserShadow() *userShadow {
	return &userShadow{byID: make(map[string]*discordgo.User)}
}

func (s *userShadow) Get(id string) *discordgo.User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byID[id]
}

func (s *userShadow) Set(u *discordgo.User) {
	s.mu.Lock()
	s.byID[u.ID] = u
	s.mu.Unlock()
}

// initialGuildSet tracks the guild IDs Discord told us we belong to at the
// initial READY. Subsequent GuildCreate for an ID NOT in this set means a
// real "guild_join"; for an ID in the set, it's just the lazy availability
// payload from initial sync.
type initialGuildSet struct {
	mu     sync.RWMutex
	ready  bool
	guilds map[string]struct{}
}

func newInitialGuildSet() *initialGuildSet {
	return &initialGuildSet{guilds: make(map[string]struct{})}
}

func (s *initialGuildSet) MarkReady(ids []string) {
	s.mu.Lock()
	for _, id := range ids {
		s.guilds[id] = struct{}{}
	}
	s.ready = true
	s.mu.Unlock()
}

// IsInitialSync reports whether a GuildCreate for guildID is part of the
// initial READY-burst (and therefore should NOT emit guild_join).
func (s *initialGuildSet) IsInitialSync(guildID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		// We haven't seen READY yet — definitely the initial sync.
		return true
	}
	_, was := s.guilds[guildID]
	if was {
		// Consume so a later GuildCreate for the same ID (which would only
		// happen for a leave-then-rejoin within the session) is treated as
		// a real join.
		delete(s.guilds, guildID)
		return true
	}
	return false
}

// ── Members ──────────────────────────────────────────────────────────────

func (b *Bot) onGuildMemberAdd(_ *discordgo.Session, e *discordgo.GuildMemberAdd) {
	if e.GuildID == "" || e.User == nil {
		return
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType: "member_join",
		GuildID:   e.GuildID,
		ActorID:   e.User.ID,
		ActorName: authorTag(e.User),
		Details: map[string]any{
			"account_created_at": isoformatPy(snowflakeTimestamp(e.User.ID)),
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save member_join", "err", err, "user_id", e.User.ID)
	}
}

func (b *Bot) onGuildMemberRemove(_ *discordgo.Session, e *discordgo.GuildMemberRemove) {
	if e.GuildID == "" || e.User == nil {
		return
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "member_leave",
		GuildID:    e.GuildID,
		ActorID:    e.User.ID,
		ActorName:  authorTag(e.User),
		Details:    map[string]any{},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save member_leave", "err", err, "user_id", e.User.ID)
	}
	b.invalidateUser(e.User.ID)
}

func (b *Bot) onGuildBanAdd(_ *discordgo.Session, e *discordgo.GuildBanAdd) {
	if e.GuildID == "" || e.User == nil {
		return
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "member_ban",
		GuildID:    e.GuildID,
		TargetID:   e.User.ID,
		TargetName: authorTag(e.User),
		Details:    map[string]any{},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save member_ban", "err", err, "user_id", e.User.ID)
	}
	b.invalidateUser(e.User.ID)
}

func (b *Bot) onGuildBanRemove(_ *discordgo.Session, e *discordgo.GuildBanRemove) {
	if e.GuildID == "" || e.User == nil {
		return
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "member_unban",
		GuildID:    e.GuildID,
		TargetID:   e.User.ID,
		TargetName: authorTag(e.User),
		Details:    map[string]any{},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save member_unban", "err", err, "user_id", e.User.ID)
	}
}

// snowflakeTimestamp extracts the creation time encoded in a Discord
// snowflake ID (Discord epoch = 2015-01-01T00:00:00Z, ms since epoch in
// the upper 42 bits).
func snowflakeTimestamp(id string) time.Time {
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return time.Time{}
	}
	const discordEpochMs = 1420070400000
	ms := int64(n>>22) + discordEpochMs
	return time.UnixMilli(ms).UTC()
}

// ── Channels ─────────────────────────────────────────────────────────────

func (b *Bot) onChannelCreate(_ *discordgo.Session, e *discordgo.ChannelCreate) {
	if e.GuildID == "" {
		return
	}
	b.invalidateGuild(e.GuildID)
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "channel_create",
		GuildID:    e.GuildID,
		TargetID:   e.ID,
		TargetName: e.Name,
		Details:    map[string]any{"channel_type": channelTypeStr(e.Type)},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save channel_create", "err", err, "channel_id", e.ID)
	}
}

func (b *Bot) onChannelDelete(_ *discordgo.Session, e *discordgo.ChannelDelete) {
	if e.GuildID == "" {
		return
	}
	b.invalidateGuild(e.GuildID)
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "channel_delete",
		GuildID:    e.GuildID,
		TargetID:   e.ID,
		TargetName: e.Name,
		Details:    map[string]any{"channel_type": channelTypeStr(e.Type)},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save channel_delete", "err", err, "channel_id", e.ID)
	}
}

// isTextLike reports whether channelType is one of the channel kinds whose
// fields topic/slowmode/nsfw are meaningful (mirrors Python's
// isinstance(after, discord.TextChannel)).
func isTextLike(t discordgo.ChannelType) bool {
	switch t {
	case discordgo.ChannelTypeGuildText,
		discordgo.ChannelTypeGuildNews,
		discordgo.ChannelTypeGuildForum,
		discordgo.ChannelTypeGuildMedia:
		return true
	}
	return false
}

func diffChannel(before, after *discordgo.Channel) map[string]any {
	changes := map[string]any{}
	if before.Name != after.Name {
		changes["name"] = map[string]any{"before": before.Name, "after": after.Name}
	}
	if isTextLike(before.Type) && isTextLike(after.Type) {
		if before.Topic != after.Topic {
			changes["topic"] = map[string]any{"before": before.Topic, "after": after.Topic}
		}
		if before.RateLimitPerUser != after.RateLimitPerUser {
			changes["slowmode_delay"] = map[string]any{
				"before": before.RateLimitPerUser, "after": after.RateLimitPerUser,
			}
		}
		if before.NSFW != after.NSFW {
			changes["nsfw"] = map[string]any{"before": before.NSFW, "after": after.NSFW}
		}
	}
	return changes
}

func (b *Bot) onChannelUpdate(_ *discordgo.Session, e *discordgo.ChannelUpdate) {
	if e.GuildID == "" || e.BeforeUpdate == nil {
		return
	}
	// Permission overwrite or category change → user→channel access map shifts.
	if !overwritesEqual(e.BeforeUpdate.PermissionOverwrites, e.PermissionOverwrites) ||
		e.BeforeUpdate.ParentID != e.ParentID {
		b.invalidateGuild(e.GuildID)
	}
	changes := diffChannel(e.BeforeUpdate, e.Channel)
	if len(changes) == 0 {
		return
	}
	ctx := context.Background()
	if err := db.SaveGuildEvent(ctx, b.Pool, db.GuildEventInput{
		EventType:  "channel_update",
		GuildID:    e.GuildID,
		TargetID:   e.ID,
		TargetName: e.Name,
		Details:    map[string]any{"changes": changes},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save channel_update", "err", err, "channel_id", e.ID)
	}
	// Channel rename → log_channels guild_name/channel_name refresh.
	if e.BeforeUpdate.Name != e.Name {
		logged, err := b.Channels.IsLogged(ctx, b.Pool, e.GuildID, e.ID)
		if err == nil && logged {
			guildName := b.guildName(e.GuildID)
			if err := db.AddLogChannel(ctx, b.Pool, e.GuildID, e.ID, guildName, e.Name); err != nil {
				slog.Error("refresh log_channel", "err", err, "channel_id", e.ID)
			}
		}
	}
}

// ── Guild update ─────────────────────────────────────────────────────────

func diffGuild(before, after *discordgo.Guild) map[string]any {
	changes := map[string]any{}
	if before.Name != after.Name {
		changes["name"] = map[string]any{"before": before.Name, "after": after.Name}
	}
	if before.Description != after.Description {
		changes["description"] = map[string]any{"before": before.Description, "after": after.Description}
	}
	if before.VerificationLevel != after.VerificationLevel {
		changes["verification_level"] = map[string]any{
			"before": verificationLevelStr(before.VerificationLevel),
			"after":  verificationLevelStr(after.VerificationLevel),
		}
	}
	if before.ExplicitContentFilter != after.ExplicitContentFilter {
		changes["explicit_content_filter"] = map[string]any{
			"before": contentFilterStr(before.ExplicitContentFilter),
			"after":  contentFilterStr(after.ExplicitContentFilter),
		}
	}
	if before.DefaultMessageNotifications != after.DefaultMessageNotifications {
		changes["default_notifications"] = map[string]any{
			"before": notificationLevelStr(before.DefaultMessageNotifications),
			"after":  notificationLevelStr(after.DefaultMessageNotifications),
		}
	}
	return changes
}

func (b *Bot) onGuildUpdate(_ *discordgo.Session, e *discordgo.GuildUpdate) {
	if e.ID == "" {
		return
	}
	before := b.Guilds.Get(e.ID)
	b.Guilds.Set(e.Guild)
	if before == nil {
		return
	}
	changes := diffGuild(before, e.Guild)
	if len(changes) == 0 {
		return
	}
	ctx := context.Background()
	if err := db.SaveGuildEvent(ctx, b.Pool, db.GuildEventInput{
		EventType:  "guild_update",
		GuildID:    e.ID,
		TargetID:   e.ID,
		TargetName: e.Name,
		Details:    map[string]any{"changes": changes},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save guild_update", "err", err, "guild_id", e.ID)
	}
	// Guild rename → all logged channels' guild_name refresh.
	if before.Name != e.Name {
		ids, err := db.GetLogChannels(ctx, b.Pool, e.ID)
		if err != nil {
			slog.Error("guild rename: GetLogChannels", "err", err)
			return
		}
		for _, chID := range ids {
			chName := b.channelName(chID)
			if chName == "" {
				chName = chID
			}
			if err := db.AddLogChannel(ctx, b.Pool, e.ID, chID, e.Name, chName); err != nil {
				slog.Error("guild rename: refresh log_channel", "err", err, "channel_id", chID)
			}
		}
	}
}

// ── Roles ────────────────────────────────────────────────────────────────

func (b *Bot) onGuildRoleCreate(_ *discordgo.Session, e *discordgo.GuildRoleCreate) {
	if e.GuildID == "" || e.Role == nil {
		return
	}
	b.Roles.Set(e.GuildID, e.Role)
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "role_create",
		GuildID:    e.GuildID,
		TargetID:   e.Role.ID,
		TargetName: e.Role.Name,
		Details:    map[string]any{},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save role_create", "err", err, "role_id", e.Role.ID)
	}
}

func (b *Bot) onGuildRoleDelete(_ *discordgo.Session, e *discordgo.GuildRoleDelete) {
	if e.GuildID == "" || e.RoleID == "" {
		return
	}
	prev := b.Roles.Get(e.GuildID, e.RoleID)
	b.Roles.Delete(e.GuildID, e.RoleID)
	b.invalidateGuild(e.GuildID)
	name := ""
	if prev != nil {
		name = prev.Name
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "role_delete",
		GuildID:    e.GuildID,
		TargetID:   e.RoleID,
		TargetName: name,
		Details:    map[string]any{},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save role_delete", "err", err, "role_id", e.RoleID)
	}
}

func diffRole(before, after *discordgo.Role) map[string]any {
	changes := map[string]any{}
	if before.Name != after.Name {
		changes["name"] = map[string]any{"before": before.Name, "after": after.Name}
	}
	if before.Color != after.Color {
		changes["colour"] = map[string]any{
			"before": colourString(before.Color),
			"after":  colourString(after.Color),
		}
	}
	if before.Hoist != after.Hoist {
		changes["hoist"] = map[string]any{"before": before.Hoist, "after": after.Hoist}
	}
	if before.Mentionable != after.Mentionable {
		changes["mentionable"] = map[string]any{"before": before.Mentionable, "after": after.Mentionable}
	}
	if before.Permissions != after.Permissions {
		changes["permissions"] = map[string]any{"before": before.Permissions, "after": after.Permissions}
	}
	return changes
}

// colourString mirrors discord.py's str(Colour) which formats as "#rrggbb".
func colourString(c int) string {
	return "#" + padHex6(c)
}

func padHex6(n int) string {
	const hex = "0123456789abcdef"
	if n < 0 {
		n = 0
	}
	out := make([]byte, 6)
	for i := 5; i >= 0; i-- {
		out[i] = hex[n&0xF]
		n >>= 4
	}
	return string(out)
}

func (b *Bot) onGuildRoleUpdate(_ *discordgo.Session, e *discordgo.GuildRoleUpdate) {
	if e.GuildID == "" || e.Role == nil {
		return
	}
	before := b.Roles.Get(e.GuildID, e.Role.ID)
	b.Roles.Set(e.GuildID, e.Role)
	if before == nil {
		return
	}
	if before.Permissions != e.Role.Permissions {
		b.invalidateGuild(e.GuildID)
	}
	changes := diffRole(before, e.Role)
	if len(changes) == 0 {
		return
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "role_update",
		GuildID:    e.GuildID,
		TargetID:   e.Role.ID,
		TargetName: e.Role.Name,
		Details:    map[string]any{"changes": changes},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save role_update", "err", err, "role_id", e.Role.ID)
	}
}

// ── Member updates ───────────────────────────────────────────────────────

// rolesEqual compares two role-ID slices as sets (order-independent).
func rolesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, id := range a {
		set[id] = struct{}{}
	}
	for _, id := range b {
		if _, ok := set[id]; !ok {
			return false
		}
	}
	return true
}

// overwritesEqual compares two permission-overwrite slices as sets keyed by
// (ID, Type, Allow, Deny). Two channels with the same set of overwrites
// in different list order are equal — this is what discord.py's Mapping
// equality does and matches the cache_invalidation_cog intent.
func overwritesEqual(a, b []*discordgo.PermissionOverwrite) bool {
	if len(a) != len(b) {
		return false
	}
	type key struct {
		ID    string
		Type  discordgo.PermissionOverwriteType
		Allow int64
		Deny  int64
	}
	set := make(map[key]struct{}, len(a))
	for _, o := range a {
		set[key{o.ID, o.Type, o.Allow, o.Deny}] = struct{}{}
	}
	for _, o := range b {
		if _, ok := set[key{o.ID, o.Type, o.Allow, o.Deny}]; !ok {
			return false
		}
	}
	return true
}

func roleNameFromState(s *discordgo.Session, guildID, roleID string) string {
	if s == nil || s.State == nil {
		return ""
	}
	if r, err := s.State.Role(guildID, roleID); err == nil && r != nil {
		return r.Name
	}
	return ""
}

func diffMember(s *discordgo.Session, guildID string, before, after *discordgo.Member) map[string]any {
	changes := map[string]any{}
	if before.Nick != after.Nick {
		changes["nick"] = map[string]any{"before": before.Nick, "after": after.Nick}
	}

	beforeIDs := make(map[string]struct{}, len(before.Roles))
	for _, id := range before.Roles {
		beforeIDs[id] = struct{}{}
	}
	afterIDs := make(map[string]struct{}, len(after.Roles))
	for _, id := range after.Roles {
		afterIDs[id] = struct{}{}
	}
	added := []map[string]any{}
	for _, id := range after.Roles {
		if _, was := beforeIDs[id]; !was {
			added = append(added, map[string]any{"id": id, "name": roleNameFromState(s, guildID, id)})
		}
	}
	removed := []map[string]any{}
	for _, id := range before.Roles {
		if _, still := afterIDs[id]; !still {
			removed = append(removed, map[string]any{"id": id, "name": roleNameFromState(s, guildID, id)})
		}
	}
	if len(added) > 0 || len(removed) > 0 {
		changes["roles"] = map[string]any{"added": added, "removed": removed}
	}

	if !timePtrEqual(before.CommunicationDisabledUntil, after.CommunicationDisabledUntil) {
		changes["timed_out_until"] = map[string]any{
			"before": isoformatPyPtr(before.CommunicationDisabledUntil),
			"after":  isoformatPyPtr(after.CommunicationDisabledUntil),
		}
	}
	return changes
}

func (b *Bot) onGuildMemberUpdate(s *discordgo.Session, e *discordgo.GuildMemberUpdate) {
	if e.BeforeUpdate == nil || e.Member == nil {
		return
	}
	guildID := e.GuildID
	if guildID == "" && e.BeforeUpdate != nil {
		guildID = e.BeforeUpdate.GuildID
	}
	if guildID == "" {
		return
	}
	// Role-set change (not nick / not timeout) is the only kind of member
	// update that affects channel access — invalidate the user's cache.
	if !rolesEqual(e.BeforeUpdate.Roles, e.Member.Roles) && e.User != nil {
		b.invalidateUser(e.User.ID)
	}
	changes := diffMember(s, guildID, e.BeforeUpdate, e.Member)
	if len(changes) == 0 {
		return
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "member_update",
		GuildID:    guildID,
		ActorID:    e.User.ID,
		ActorName:  memberTag(e.BeforeUpdate),
		Details:    map[string]any{"changes": changes},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save member_update", "err", err, "user_id", e.User.ID)
	}
}

// ── Voice ────────────────────────────────────────────────────────────────

func (b *Bot) onVoiceStateUpdate(_ *discordgo.Session, e *discordgo.VoiceStateUpdate) {
	if e.GuildID == "" || e.UserID == "" {
		return
	}
	now := time.Now()
	actorName := b.userTagFromState(e.UserID)
	beforeChannel := ""
	if e.BeforeUpdate != nil {
		beforeChannel = e.BeforeUpdate.ChannelID
	}
	afterChannel := e.ChannelID

	// Channel transitions
	if beforeChannel != afterChannel {
		var eventType, targetID, targetName string
		details := map[string]any{}
		switch {
		case beforeChannel == "" && afterChannel != "":
			eventType = "voice_join"
			targetID = afterChannel
			targetName = b.channelName(afterChannel)
		case beforeChannel != "" && afterChannel == "":
			eventType = "voice_leave"
			targetID = beforeChannel
			targetName = b.channelName(beforeChannel)
		default:
			eventType = "voice_move"
			targetID = afterChannel
			targetName = b.channelName(afterChannel)
			details["from_channel_id"] = beforeChannel
			details["from_channel_name"] = b.channelName(beforeChannel)
		}
		if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
			EventType:  eventType,
			GuildID:    e.GuildID,
			ActorID:    e.UserID,
			ActorName:  actorName,
			TargetID:   targetID,
			TargetName: targetName,
			Details:    details,
			OccurredAt: now,
		}); err != nil {
			slog.Error("save voice channel transition", "err", err, "user_id", e.UserID)
		}
	}

	// Voice state mutations (mute/deaf/etc)
	if e.BeforeUpdate == nil {
		return
	}
	vc := diffVoiceState(e.BeforeUpdate, e.VoiceState)
	if len(vc) == 0 {
		return
	}
	channelID := afterChannel
	if channelID == "" {
		channelID = beforeChannel
	}
	channelName := b.channelName(channelID)
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "voice_state_change",
		GuildID:    e.GuildID,
		ActorID:    e.UserID,
		ActorName:  actorName,
		TargetID:   channelID,
		TargetName: channelName,
		Details:    map[string]any{"changes": vc},
		OccurredAt: now,
	}); err != nil {
		slog.Error("save voice_state_change", "err", err, "user_id", e.UserID)
	}
}

func diffVoiceState(before, after *discordgo.VoiceState) map[string]any {
	c := map[string]any{}
	if before.SelfMute != after.SelfMute {
		c["self_mute"] = map[string]any{"before": before.SelfMute, "after": after.SelfMute}
	}
	if before.SelfDeaf != after.SelfDeaf {
		c["self_deaf"] = map[string]any{"before": before.SelfDeaf, "after": after.SelfDeaf}
	}
	if before.Mute != after.Mute {
		c["mute"] = map[string]any{"before": before.Mute, "after": after.Mute}
	}
	if before.Deaf != after.Deaf {
		c["deaf"] = map[string]any{"before": before.Deaf, "after": after.Deaf}
	}
	if before.SelfStream != after.SelfStream {
		c["self_stream"] = map[string]any{"before": before.SelfStream, "after": after.SelfStream}
	}
	if before.SelfVideo != after.SelfVideo {
		c["self_video"] = map[string]any{"before": before.SelfVideo, "after": after.SelfVideo}
	}
	return c
}

// ── Threads ──────────────────────────────────────────────────────────────

func (b *Bot) onThreadCreate(_ *discordgo.Session, e *discordgo.ThreadCreate) {
	if e.GuildID == "" {
		return
	}
	ownerID := ""
	ownerName := ""
	if e.OwnerID != "" {
		ownerID = e.OwnerID
		ownerName = b.userTagFromState(e.OwnerID)
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "thread_create",
		GuildID:    e.GuildID,
		ActorID:    ownerID,
		ActorName:  ownerName,
		TargetID:   e.ID,
		TargetName: e.Name,
		Details: map[string]any{
			"parent_id": e.ParentID,
			"type":      channelTypeStr(e.Type),
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save thread_create", "err", err, "thread_id", e.ID)
	}
}

func diffThread(before, after *discordgo.Channel) map[string]any {
	changes := map[string]any{}
	if before.Name != after.Name {
		changes["name"] = map[string]any{"before": before.Name, "after": after.Name}
	}
	beforeArchived := false
	beforeLocked := false
	beforeAA := 0
	if before.ThreadMetadata != nil {
		beforeArchived = before.ThreadMetadata.Archived
		beforeLocked = before.ThreadMetadata.Locked
		beforeAA = before.ThreadMetadata.AutoArchiveDuration
	}
	afterArchived := false
	afterLocked := false
	afterAA := 0
	if after.ThreadMetadata != nil {
		afterArchived = after.ThreadMetadata.Archived
		afterLocked = after.ThreadMetadata.Locked
		afterAA = after.ThreadMetadata.AutoArchiveDuration
	}
	if beforeArchived != afterArchived {
		changes["archived"] = map[string]any{"before": beforeArchived, "after": afterArchived}
	}
	if beforeLocked != afterLocked {
		changes["locked"] = map[string]any{"before": beforeLocked, "after": afterLocked}
	}
	if before.RateLimitPerUser != after.RateLimitPerUser {
		changes["slowmode_delay"] = map[string]any{
			"before": before.RateLimitPerUser, "after": after.RateLimitPerUser,
		}
	}
	if beforeAA != afterAA {
		changes["auto_archive_duration"] = map[string]any{"before": beforeAA, "after": afterAA}
	}
	return changes
}

func (b *Bot) onThreadUpdate(_ *discordgo.Session, e *discordgo.ThreadUpdate) {
	if e.GuildID == "" || e.BeforeUpdate == nil {
		return
	}
	changes := diffThread(e.BeforeUpdate, e.Channel)
	if len(changes) == 0 {
		return
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "thread_update",
		GuildID:    e.GuildID,
		TargetID:   e.ID,
		TargetName: e.Name,
		Details: map[string]any{
			"parent_id": e.ParentID,
			"changes":   changes,
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save thread_update", "err", err, "thread_id", e.ID)
	}
}

func (b *Bot) onThreadDelete(_ *discordgo.Session, e *discordgo.ThreadDelete) {
	if e.GuildID == "" {
		return
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "thread_delete",
		GuildID:    e.GuildID,
		TargetID:   e.ID,
		TargetName: e.Name,
		Details:    map[string]any{"parent_id": e.ParentID},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save thread_delete", "err", err, "thread_id", e.ID)
	}
}

// onThreadMembersUpdate splits discordgo's batch (added + removed in one
// payload) into discord.py-equivalent thread_member_join / _remove events
// per the Phase 4 R6 mitigation note.
func (b *Bot) onThreadMembersUpdate(_ *discordgo.Session, e *discordgo.ThreadMembersUpdate) {
	if e.GuildID == "" {
		return
	}
	threadName := b.channelName(e.ID)
	now := time.Now()

	for _, m := range e.AddedMembers {
		if m.ThreadMember == nil {
			continue
		}
		userID := m.ThreadMember.UserID
		if userID == "" && m.Member != nil && m.Member.User != nil {
			userID = m.Member.User.ID
		}
		if userID == "" {
			continue
		}
		if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
			EventType:  "thread_member_join",
			GuildID:    e.GuildID,
			ActorID:    userID,
			TargetID:   e.ID,
			TargetName: threadName,
			Details: map[string]any{
				"thread_id":   e.ID,
				"thread_name": threadName,
				"user_id":     userID,
			},
			OccurredAt: now,
		}); err != nil {
			slog.Error("save thread_member_join", "err", err, "thread_id", e.ID)
		}
	}

	for _, userID := range e.RemovedMembers {
		if userID == "" {
			continue
		}
		if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
			EventType:  "thread_member_remove",
			GuildID:    e.GuildID,
			ActorID:    userID,
			TargetID:   e.ID,
			TargetName: threadName,
			Details: map[string]any{
				"thread_id":   e.ID,
				"thread_name": threadName,
				"user_id":     userID,
			},
			OccurredAt: now,
		}); err != nil {
			slog.Error("save thread_member_remove", "err", err, "thread_id", e.ID)
		}
	}
}

// ── Reactions ────────────────────────────────────────────────────────────
//
// discordgo dispatches MESSAGE_REACTION_ADD/REMOVE once per gateway event,
// regardless of whether the parent message is cached. Python's discord.py
// fired both `on_reaction_add` (cached path, message known) and
// `on_raw_reaction_add` (always), and the Python cog deduped via
// `if payload.cached_message is not None: return`. The Go side replaces
// both with this single handler — net effect is identical (one row per
// gateway dispatch), so no raw fallback is needed.
//
// Detail-shape divergence from Python: the cached `on_reaction_add` path
// historically omitted `user_id` from details (the actor was inferred
// from the Member object). Our Go handler always includes `user_id`,
// matching the raw path's shape. Strictly more information.

func (b *Bot) onMessageReactionAdd(_ *discordgo.Session, e *discordgo.MessageReactionAdd) {
	if e.GuildID == "" {
		return
	}
	// Skip bot reactions (mirrors `if user.bot: return`).
	if e.Member != nil && e.Member.User != nil && e.Member.User.Bot {
		return
	}
	emojiStr := emojiString(&e.Emoji)
	actorName := ""
	if e.Member != nil && e.Member.User != nil {
		actorName = authorTag(e.Member.User)
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "reaction_add",
		GuildID:    e.GuildID,
		ActorID:    e.UserID,
		ActorName:  actorName,
		TargetID:   e.MessageID,
		TargetName: emojiStr,
		Details: map[string]any{
			"channel_id": e.ChannelID,
			"emoji":      emojiStr,
			"message_id": e.MessageID,
			"user_id":    e.UserID,
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save reaction_add", "err", err, "message_id", e.MessageID)
	}
}

func (b *Bot) onMessageReactionRemove(_ *discordgo.Session, e *discordgo.MessageReactionRemove) {
	if e.GuildID == "" {
		return
	}
	emojiStr := emojiString(&e.Emoji)
	actorName := b.userTagFromState(e.UserID)
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "reaction_remove",
		GuildID:    e.GuildID,
		ActorID:    e.UserID,
		ActorName:  actorName,
		TargetID:   e.MessageID,
		TargetName: emojiStr,
		Details: map[string]any{
			"channel_id": e.ChannelID,
			"emoji":      emojiStr,
			"message_id": e.MessageID,
			"user_id":    e.UserID,
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save reaction_remove", "err", err, "message_id", e.MessageID)
	}
}

// NOTE: Python's `on_reaction_clear_emoji` (single-emoji clear via gateway
// MESSAGE_REACTION_REMOVE_EMOJI) is intentionally not translated — discordgo
// v0.29.0 does not surface this event. Loss is benign: the parent message's
// new reaction list will arrive on the next reaction add/remove from a user.

func (b *Bot) onMessageReactionRemoveAll(_ *discordgo.Session, e *discordgo.MessageReactionRemoveAll) {
	if e.GuildID == "" {
		return
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType: "reaction_clear",
		GuildID:   e.GuildID,
		TargetID:  e.MessageID,
		Details: map[string]any{
			"channel_id": e.ChannelID,
			"message_id": e.MessageID,
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save reaction_clear", "err", err, "message_id", e.MessageID)
	}
}

// ── Invites ──────────────────────────────────────────────────────────────

func (b *Bot) onInviteCreate(_ *discordgo.Session, e *discordgo.InviteCreate) {
	if e.GuildID == "" {
		return
	}
	var inviterID, inviterName string
	if e.Inviter != nil {
		inviterID = e.Inviter.ID
		inviterName = authorTag(e.Inviter)
	}
	chName := b.channelName(e.ChannelID)
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "invite_create",
		GuildID:    e.GuildID,
		ActorID:    inviterID,
		ActorName:  inviterName,
		TargetID:   e.Code,
		TargetName: e.Code,
		Details: map[string]any{
			"channel_id":   e.ChannelID,
			"channel_name": chName,
			"max_uses":     e.MaxUses,
			"max_age":      e.MaxAge,
			"temporary":    e.Temporary,
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save invite_create", "err", err, "code", e.Code)
	}
}

func (b *Bot) onInviteDelete(_ *discordgo.Session, e *discordgo.InviteDelete) {
	if e.GuildID == "" {
		return
	}
	chName := b.channelName(e.ChannelID)
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "invite_delete",
		GuildID:    e.GuildID,
		TargetID:   e.Code,
		TargetName: e.Code,
		Details: map[string]any{
			"channel_id":   e.ChannelID,
			"channel_name": chName,
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save invite_delete", "err", err, "code", e.Code)
	}
}

// ── Emojis / Stickers ────────────────────────────────────────────────────

func diffEmojis(prev map[string]*discordgo.Emoji, current []*discordgo.Emoji) (added, removed []map[string]any) {
	currentByID := make(map[string]*discordgo.Emoji, len(current))
	for _, e := range current {
		currentByID[e.ID] = e
	}
	for _, e := range current {
		if _, was := prev[e.ID]; !was {
			added = append(added, map[string]any{"id": e.ID, "name": e.Name})
		}
	}
	for id, e := range prev {
		if _, still := currentByID[id]; !still {
			removed = append(removed, map[string]any{"id": id, "name": e.Name})
		}
	}
	return
}

func (b *Bot) onGuildEmojisUpdate(_ *discordgo.Session, e *discordgo.GuildEmojisUpdate) {
	if e.GuildID == "" {
		return
	}
	prev := b.Emojis.Get(e.GuildID)
	b.Emojis.Replace(e.GuildID, e.Emojis)
	if prev == nil {
		return
	}
	added, removed := diffEmojis(prev, e.Emojis)
	if len(added) == 0 && len(removed) == 0 {
		return
	}
	if added == nil {
		added = []map[string]any{}
	}
	if removed == nil {
		removed = []map[string]any{}
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "emojis_update",
		GuildID:    e.GuildID,
		Details:    map[string]any{"added": added, "removed": removed},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save emojis_update", "err", err, "guild_id", e.GuildID)
	}
}

func diffStickers(prev map[string]*discordgo.Sticker, current []*discordgo.Sticker) (added, removed []map[string]any) {
	currentByID := make(map[string]*discordgo.Sticker, len(current))
	for _, s := range current {
		currentByID[s.ID] = s
	}
	for _, s := range current {
		if _, was := prev[s.ID]; !was {
			added = append(added, map[string]any{"id": s.ID, "name": s.Name})
		}
	}
	for id, s := range prev {
		if _, still := currentByID[id]; !still {
			removed = append(removed, map[string]any{"id": id, "name": s.Name})
		}
	}
	return
}

func (b *Bot) onGuildStickersUpdate(_ *discordgo.Session, e *discordgo.GuildStickersUpdate) {
	if e.GuildID == "" {
		return
	}
	prev := b.Stickers.Get(e.GuildID)
	b.Stickers.Replace(e.GuildID, e.Stickers)
	if prev == nil {
		return
	}
	added, removed := diffStickers(prev, e.Stickers)
	if len(added) == 0 && len(removed) == 0 {
		return
	}
	if added == nil {
		added = []map[string]any{}
	}
	if removed == nil {
		removed = []map[string]any{}
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "stickers_update",
		GuildID:    e.GuildID,
		Details:    map[string]any{"added": added, "removed": removed},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save stickers_update", "err", err, "guild_id", e.GuildID)
	}
}

// ── User update ──────────────────────────────────────────────────────────

func avatarURL(u *discordgo.User) any {
	if u == nil || u.Avatar == "" {
		return nil
	}
	return u.AvatarURL("")
}

func diffUser(before, after *discordgo.User) map[string]any {
	changes := map[string]any{}
	if before.Username != after.Username {
		changes["name"] = map[string]any{"before": before.Username, "after": after.Username}
	}
	if before.GlobalName != after.GlobalName {
		changes["global_name"] = map[string]any{"before": before.GlobalName, "after": after.GlobalName}
	}
	if before.Avatar != after.Avatar {
		changes["avatar"] = map[string]any{
			"before": avatarURL(before),
			"after":  avatarURL(after),
		}
	}
	return changes
}

func (b *Bot) onUserUpdate(_ *discordgo.Session, e *discordgo.UserUpdate) {
	if e.User == nil {
		return
	}
	before := b.Users.Get(e.User.ID)
	b.Users.Set(e.User)
	if before == nil {
		return
	}
	changes := diffUser(before, e.User)
	if len(changes) == 0 {
		return
	}
	guildID := b.firstMutualGuild(e.User.ID)
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "user_update",
		GuildID:    guildID,
		ActorID:    e.User.ID,
		ActorName:  authorTag(e.User),
		Details:    map[string]any{"changes": changes},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save user_update", "err", err, "user_id", e.User.ID)
	}
}

// firstMutualGuild returns the first guild ID where the user is a known
// member, or "0" if none — mirrors discord.py's mutual_guilds[0].id fallback.
func (b *Bot) firstMutualGuild(userID string) string {
	if b.Session == nil || b.Session.State == nil {
		return "0"
	}
	for _, g := range b.Session.State.Guilds {
		if _, err := b.Session.State.Member(g.ID, userID); err == nil {
			return g.ID
		}
	}
	return "0"
}

// ── Guild join / remove (initial-sync aware) ─────────────────────────────

func (b *Bot) onGuildCreate(_ *discordgo.Session, e *discordgo.GuildCreate) {
	if e.Guild == nil {
		return
	}
	// Always seed shadows so subsequent Update events have a baseline.
	b.Guilds.Set(e.Guild)
	for _, r := range e.Roles {
		b.Roles.Set(e.ID, r)
	}
	b.Emojis.Replace(e.ID, e.Emojis)
	b.Stickers.Replace(e.ID, e.Stickers)

	if b.InitialGuilds.IsInitialSync(e.ID) {
		// Lazy availability burst — not a real join.
		return
	}
	var actorID, actorName string
	if b.Session != nil && b.Session.State != nil && b.Session.State.User != nil {
		actorID = b.Session.State.User.ID
		actorName = authorTag(b.Session.State.User)
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "guild_join",
		GuildID:    e.ID,
		ActorID:    actorID,
		ActorName:  actorName,
		TargetID:   e.ID,
		TargetName: e.Name,
		Details: map[string]any{
			"guild_name":   e.Name,
			"member_count": e.MemberCount,
			"owner_id":     e.OwnerID,
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save guild_join", "err", err, "guild_id", e.ID)
	}
}

func (b *Bot) onGuildDelete(_ *discordgo.Session, e *discordgo.GuildDelete) {
	if e.Guild == nil {
		return
	}
	// Discord sends GuildDelete with Unavailable=true during outages — the
	// bot is still in the guild, just temporarily disconnected. Don't
	// emit guild_remove for those.
	if e.Unavailable {
		return
	}
	b.Guilds.Delete(e.ID)
	b.invalidateGuild(e.ID)
	name := ""
	memberCount := 0
	if e.BeforeDelete != nil {
		name = e.BeforeDelete.Name
		memberCount = e.BeforeDelete.MemberCount
	}
	var actorID, actorName string
	if b.Session != nil && b.Session.State != nil && b.Session.State.User != nil {
		actorID = b.Session.State.User.ID
		actorName = authorTag(b.Session.State.User)
	}
	if err := db.SaveGuildEvent(context.Background(), b.Pool, db.GuildEventInput{
		EventType:  "guild_remove",
		GuildID:    e.ID,
		ActorID:    actorID,
		ActorName:  actorName,
		TargetID:   e.ID,
		TargetName: name,
		Details: map[string]any{
			"guild_name":   name,
			"member_count": memberCount,
		},
		OccurredAt: time.Now(),
	}); err != nil {
		slog.Error("save guild_remove", "err", err, "guild_id", e.ID)
	}
}

