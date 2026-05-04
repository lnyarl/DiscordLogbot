package discord

import (
	"reflect"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

// ── Snowflake → timestamp ────────────────────────────────────────────────

func TestSnowflakeTimestamp(t *testing.T) {
	// Snowflake 175928847299117063 = 2016-04-30T11:18:25.796Z (Discord docs example).
	got := snowflakeTimestamp("175928847299117063").UTC()
	want := time.Date(2016, 4, 30, 11, 18, 25, 796_000_000, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got=%s want=%s", got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}

func TestSnowflakeTimestamp_Invalid(t *testing.T) {
	got := snowflakeTimestamp("not-a-number")
	if !got.IsZero() {
		t.Errorf("expected zero time for invalid snowflake, got %v", got)
	}
}

// ── rolesEqual / overwritesEqual ─────────────────────────────────────────

func TestRolesEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want bool
	}{
		{"both nil", nil, nil, true},
		{"empty == empty", []string{}, []string{}, true},
		{"same order", []string{"r1", "r2"}, []string{"r1", "r2"}, true},
		{"different order", []string{"r1", "r2"}, []string{"r2", "r1"}, true},
		{"differ", []string{"r1"}, []string{"r2"}, false},
		{"length differ", []string{"r1"}, []string{"r1", "r2"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rolesEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("got=%v want=%v", got, tt.want)
			}
		})
	}
}

func TestOverwritesEqual(t *testing.T) {
	o1 := &discordgo.PermissionOverwrite{ID: "r1", Type: discordgo.PermissionOverwriteTypeRole, Allow: 10, Deny: 0}
	o2 := &discordgo.PermissionOverwrite{ID: "r2", Type: discordgo.PermissionOverwriteTypeRole, Allow: 0, Deny: 5}
	o2Mod := &discordgo.PermissionOverwrite{ID: "r2", Type: discordgo.PermissionOverwriteTypeRole, Allow: 0, Deny: 6}

	if !overwritesEqual([]*discordgo.PermissionOverwrite{o1, o2}, []*discordgo.PermissionOverwrite{o2, o1}) {
		t.Error("set-equal lists must compare equal")
	}
	if overwritesEqual([]*discordgo.PermissionOverwrite{o1, o2}, []*discordgo.PermissionOverwrite{o1, o2Mod}) {
		t.Error("differing Deny must not compare equal")
	}
	if overwritesEqual([]*discordgo.PermissionOverwrite{o1}, []*discordgo.PermissionOverwrite{o1, o2}) {
		t.Error("length differs must not compare equal")
	}
	if !overwritesEqual(nil, []*discordgo.PermissionOverwrite{}) {
		t.Error("nil and empty must compare equal")
	}
}

// ── diffChannel ──────────────────────────────────────────────────────────

func TestDiffChannel(t *testing.T) {
	before := &discordgo.Channel{
		Type: discordgo.ChannelTypeGuildText, Name: "old", Topic: "t1",
		RateLimitPerUser: 0, NSFW: false,
	}
	after := &discordgo.Channel{
		Type: discordgo.ChannelTypeGuildText, Name: "new", Topic: "t1",
		RateLimitPerUser: 5, NSFW: true,
	}
	c := diffChannel(before, after)
	if _, ok := c["name"]; !ok {
		t.Error("missing name change")
	}
	if _, ok := c["slowmode_delay"]; !ok {
		t.Error("missing slowmode_delay change")
	}
	if _, ok := c["nsfw"]; !ok {
		t.Error("missing nsfw change")
	}
	if _, ok := c["topic"]; ok {
		t.Error("topic should not be changed")
	}
}

func TestDiffChannel_NonTextSkipsTextFields(t *testing.T) {
	before := &discordgo.Channel{Type: discordgo.ChannelTypeGuildVoice, Topic: "x"}
	after := &discordgo.Channel{Type: discordgo.ChannelTypeGuildVoice, Topic: "y"}
	c := diffChannel(before, after)
	if _, ok := c["topic"]; ok {
		t.Error("topic must not be diffed for non-text channels")
	}
}

// ── diffGuild ────────────────────────────────────────────────────────────

func TestDiffGuild(t *testing.T) {
	before := &discordgo.Guild{Name: "A", Description: "d1", VerificationLevel: 1}
	after := &discordgo.Guild{Name: "B", Description: "d1", VerificationLevel: 2}
	c := diffGuild(before, after)
	if _, ok := c["name"]; !ok {
		t.Error("missing name change")
	}
	if _, ok := c["verification_level"]; !ok {
		t.Error("missing verification_level change")
	}
	if _, ok := c["description"]; ok {
		t.Error("description should not be flagged")
	}
}

// ── diffRole ─────────────────────────────────────────────────────────────

func TestDiffRole(t *testing.T) {
	before := &discordgo.Role{Name: "old", Color: 0xFF0000, Hoist: false, Permissions: 1}
	after := &discordgo.Role{Name: "new", Color: 0xFF0000, Hoist: true, Permissions: 1}
	c := diffRole(before, after)
	if _, ok := c["name"]; !ok {
		t.Error("missing name change")
	}
	if _, ok := c["hoist"]; !ok {
		t.Error("missing hoist change")
	}
	if _, ok := c["colour"]; ok {
		t.Error("colour should not have changed")
	}
}

// ── colourString ─────────────────────────────────────────────────────────

func TestColourString(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0x000000, "#000000"},
		{0xFFFFFF, "#ffffff"},
		{0xFF0000, "#ff0000"},
		{0x123456, "#123456"},
	}
	for _, tt := range tests {
		if got := colourString(tt.in); got != tt.want {
			t.Errorf("colourString(%#x) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ── diffMember ───────────────────────────────────────────────────────────

func TestDiffMember_RoleAddRemove(t *testing.T) {
	before := &discordgo.Member{Roles: []string{"r1", "r2"}}
	after := &discordgo.Member{Roles: []string{"r1", "r3"}}
	c := diffMember(nil, "G1", before, after)
	rolesAny, ok := c["roles"]
	if !ok {
		t.Fatal("expected roles change")
	}
	roles := rolesAny.(map[string]any)
	added := roles["added"].([]map[string]any)
	removed := roles["removed"].([]map[string]any)
	if len(added) != 1 || added[0]["id"] != "r3" {
		t.Errorf("added unexpected: %v", added)
	}
	if len(removed) != 1 || removed[0]["id"] != "r2" {
		t.Errorf("removed unexpected: %v", removed)
	}
}

func TestDiffMember_NickOnly(t *testing.T) {
	before := &discordgo.Member{Nick: "old"}
	after := &discordgo.Member{Nick: "new"}
	c := diffMember(nil, "G1", before, after)
	if _, ok := c["nick"]; !ok {
		t.Error("missing nick change")
	}
	if _, ok := c["roles"]; ok {
		t.Error("no role change expected")
	}
}

func TestDiffMember_Timeout(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	before := &discordgo.Member{}
	after := &discordgo.Member{CommunicationDisabledUntil: &t1}
	c := diffMember(nil, "G1", before, after)
	if _, ok := c["timed_out_until"]; !ok {
		t.Error("missing timed_out_until change")
	}
}

// ── diffVoiceState ───────────────────────────────────────────────────────

func TestDiffVoiceState(t *testing.T) {
	before := &discordgo.VoiceState{SelfMute: false, Deaf: false}
	after := &discordgo.VoiceState{SelfMute: true, Deaf: true}
	c := diffVoiceState(before, after)
	if _, ok := c["self_mute"]; !ok {
		t.Error("missing self_mute change")
	}
	if _, ok := c["deaf"]; !ok {
		t.Error("missing deaf change")
	}
	if len(c) != 2 {
		t.Errorf("expected 2 changes, got %d", len(c))
	}
}

// ── diffEmojis / diffStickers ────────────────────────────────────────────

func TestDiffEmojis(t *testing.T) {
	prev := map[string]*discordgo.Emoji{
		"e1": {ID: "e1", Name: "wave"},
		"e2": {ID: "e2", Name: "smile"},
	}
	cur := []*discordgo.Emoji{
		{ID: "e2", Name: "smile"},
		{ID: "e3", Name: "fire"},
	}
	added, removed := diffEmojis(prev, cur)
	if len(added) != 1 || added[0]["id"] != "e3" {
		t.Errorf("added: %v", added)
	}
	if len(removed) != 1 || removed[0]["id"] != "e1" {
		t.Errorf("removed: %v", removed)
	}
}

func TestDiffStickers(t *testing.T) {
	prev := map[string]*discordgo.Sticker{
		"s1": {ID: "s1", Name: "old"},
	}
	cur := []*discordgo.Sticker{
		{ID: "s1", Name: "old"},
		{ID: "s2", Name: "new"},
	}
	added, removed := diffStickers(prev, cur)
	if len(added) != 1 || added[0]["id"] != "s2" {
		t.Errorf("added: %v", added)
	}
	if len(removed) != 0 {
		t.Errorf("removed: %v", removed)
	}
}

// ── diffUser ─────────────────────────────────────────────────────────────

func TestDiffUser(t *testing.T) {
	before := &discordgo.User{Username: "alice", GlobalName: "Alice"}
	after := &discordgo.User{Username: "alice2", GlobalName: "Alice"}
	c := diffUser(before, after)
	if _, ok := c["name"]; !ok {
		t.Error("missing name change")
	}
	if _, ok := c["global_name"]; ok {
		t.Error("global_name should not have changed")
	}
}

// ── initialGuildSet ──────────────────────────────────────────────────────

func TestInitialGuildSet_BeforeReadyAlwaysInitial(t *testing.T) {
	s := newInitialGuildSet()
	if !s.IsInitialSync("anything") {
		t.Error("before MarkReady, all GuildCreate should be initial sync")
	}
}

func TestInitialGuildSet_FirstHitInitialSecondReal(t *testing.T) {
	s := newInitialGuildSet()
	s.MarkReady([]string{"G1", "G2"})
	if !s.IsInitialSync("G1") {
		t.Error("first GuildCreate for G1 must be initial sync")
	}
	if s.IsInitialSync("G1") {
		t.Error("second GuildCreate for G1 (re-join) must be a real join")
	}
	if s.IsInitialSync("Gnew") {
		t.Error("GuildCreate for ID not in READY must be a real join")
	}
}

// ── readyTracker ─────────────────────────────────────────────────────────

func TestReadyTracker_FirstReadyDoesNothing(t *testing.T) {
	rt := newReadyTracker()
	if rt.started.Load() {
		t.Error("fresh tracker must not be started")
	}
}

// Sanity check that the dispatch helper gates correctly (the actual cache
// invalidation requires a live pool, asserted in cache_invalidation_test).

// ── shadow caches ────────────────────────────────────────────────────────

func TestGuildShadow_RoundTrip(t *testing.T) {
	s := newGuildShadow()
	if g := s.Get("G1"); g != nil {
		t.Error("empty shadow must return nil")
	}
	s.Set(&discordgo.Guild{ID: "G1", Name: "alpha"})
	got := s.Get("G1")
	if got == nil || got.Name != "alpha" {
		t.Errorf("expected alpha, got %v", got)
	}
	s.Delete("G1")
	if g := s.Get("G1"); g != nil {
		t.Error("Delete must clear")
	}
}

func TestEmojiShadow_GetReturnsCopy(t *testing.T) {
	s := newEmojiShadow()
	s.Replace("G1", []*discordgo.Emoji{{ID: "e1", Name: "wave"}})
	got := s.Get("G1")
	delete(got, "e1")
	again := s.Get("G1")
	if _, ok := again["e1"]; !ok {
		t.Error("Get must return defensive copy")
	}
}

// ── isTextLike ──────────────────────────────────────────────────────────

func TestIsTextLike(t *testing.T) {
	cases := map[discordgo.ChannelType]bool{
		discordgo.ChannelTypeGuildText:    true,
		discordgo.ChannelTypeGuildNews:    true,
		discordgo.ChannelTypeGuildForum:   true,
		discordgo.ChannelTypeGuildMedia:   true,
		discordgo.ChannelTypeGuildVoice:   false,
		discordgo.ChannelTypeDM:           false,
		discordgo.ChannelTypeGuildPublicThread: false,
	}
	for ct, want := range cases {
		if got := isTextLike(ct); got != want {
			t.Errorf("isTextLike(%v) = %v, want %v", ct, got, want)
		}
	}
}

// ── diffScheduledEvent / diffStageInstance ─────────────────────────────

func TestDiffScheduledEvent(t *testing.T) {
	t1 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 1, 13, 0, 0, 0, time.UTC)
	before := &discordgo.GuildScheduledEvent{Name: "A", ScheduledStartTime: t1, Status: 1}
	after := &discordgo.GuildScheduledEvent{Name: "B", ScheduledStartTime: t2, Status: 2}
	loc := func(*discordgo.GuildScheduledEvent) string { return "" }
	c := diffScheduledEvent(before, after, loc)
	if _, ok := c["name"]; !ok {
		t.Error("missing name")
	}
	if _, ok := c["start_time"]; !ok {
		t.Error("missing start_time")
	}
	if _, ok := c["status"]; !ok {
		t.Error("missing status")
	}
}

func TestDiffScheduledEvent_NoChange(t *testing.T) {
	t1 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	e := &discordgo.GuildScheduledEvent{Name: "A", ScheduledStartTime: t1}
	loc := func(*discordgo.GuildScheduledEvent) string { return "x" }
	c := diffScheduledEvent(e, e, loc)
	if len(c) != 0 {
		t.Errorf("expected no changes, got %v", c)
	}
}

func TestDiffStageInstance(t *testing.T) {
	before := &discordgo.StageInstance{Topic: "old", PrivacyLevel: 1}
	after := &discordgo.StageInstance{Topic: "new", PrivacyLevel: 2}
	c := diffStageInstance(before, after)
	if _, ok := c["topic"]; !ok {
		t.Error("missing topic")
	}
	if _, ok := c["privacy_level"]; !ok {
		t.Error("missing privacy_level")
	}
}

// ── isoformatPy ──────────────────────────────────────────────────────────

func TestIsoformatPy(t *testing.T) {
	whole := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := isoformatPy(whole); got != "2026-01-02T03:04:05+00:00" {
		t.Errorf("whole-second got %q", got)
	}
	frac := time.Date(2026, 1, 2, 3, 4, 5, 500_000_000, time.UTC)
	if got := isoformatPy(frac); got != "2026-01-02T03:04:05.500000+00:00" {
		t.Errorf("fractional got %q", got)
	}
}

func TestIsoformatPyPtr(t *testing.T) {
	if got := isoformatPyPtr(nil); got != nil {
		t.Errorf("nil should be nil, got %v", got)
	}
	tt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got := isoformatPyPtr(&tt)
	if got != "2026-01-02T03:04:05+00:00" {
		t.Errorf("got %v", got)
	}
}

// ── emojiString ──────────────────────────────────────────────────────────

func TestEmojiString(t *testing.T) {
	tests := []struct {
		name string
		e    *discordgo.Emoji
		want string
	}{
		{"nil", nil, ""},
		{"unicode", &discordgo.Emoji{Name: "👋"}, "👋"},
		{"custom static", &discordgo.Emoji{ID: "123", Name: "wave"}, "<:wave:123>"},
		{"custom animated", &discordgo.Emoji{ID: "456", Name: "spin", Animated: true}, "<a:spin:456>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := emojiString(tt.e); got != tt.want {
				t.Errorf("got=%q want=%q", got, tt.want)
			}
		})
	}
}

// ── memberTag / authorTag interplay ──────────────────────────────────────

func TestMemberTag(t *testing.T) {
	if memberTag(nil) != "" {
		t.Error("nil member must produce empty tag")
	}
	m := &discordgo.Member{User: &discordgo.User{Username: "alice", Discriminator: "0"}}
	if got := memberTag(m); got != "alice" {
		t.Errorf("got %q", got)
	}
}

// ── auditLogChanges ──────────────────────────────────────────────────────

func TestAuditLogChanges(t *testing.T) {
	k1 := discordgo.AuditLogChangeKey("name")
	k2 := discordgo.AuditLogChangeKey("nsfw")
	changes := []*discordgo.AuditLogChange{
		{Key: &k1, OldValue: "old", NewValue: "new"},
		{Key: &k2, OldValue: false, NewValue: true},
		{Key: nil, OldValue: "x", NewValue: "y"}, // must be skipped
	}
	got := auditLogChanges(changes)
	want := map[string]any{
		"name": map[string]any{"before": "old", "after": "new"},
		"nsfw": map[string]any{"before": "False", "after": "True"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got=%v want=%v", got, want)
	}
}

// ── autoModTriggerName ──────────────────────────────────────────────────

func TestAutoModTriggerName(t *testing.T) {
	tests := []struct {
		in   discordgo.AutoModerationRuleTriggerType
		want string
	}{
		{discordgo.AutoModerationEventTriggerKeyword, "keyword"},
		{discordgo.AutoModerationEventTriggerHarmfulLink, "harmful_link"},
		{discordgo.AutoModerationEventTriggerSpam, "spam"},
		{discordgo.AutoModerationEventTriggerKeywordPreset, "keyword_preset"},
		{99, ""},
	}
	for _, tt := range tests {
		if got := autoModTriggerName(tt.in); got != tt.want {
			t.Errorf("got=%q want=%q", got, tt.want)
		}
	}
}

// ── timePtrEqual ─────────────────────────────────────────────────────────

func TestTimePtrEqual(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	if !timePtrEqual(nil, nil) {
		t.Error("nil/nil must be equal")
	}
	if timePtrEqual(&t1, nil) || timePtrEqual(nil, &t1) {
		t.Error("nil vs non-nil must not be equal")
	}
	if !timePtrEqual(&t1, &t2) {
		t.Error("equal instants must compare equal")
	}
	if timePtrEqual(&t1, &t3) {
		t.Error("different instants must not be equal")
	}
}
