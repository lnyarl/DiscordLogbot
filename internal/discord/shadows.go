package discord

import (
	"sync"

	"github.com/bwmarrin/discordgo"
)

// Shadow caches for change diffs.
//
// discordgo doesn't ship a Before snapshot for many Update events
// (GuildUpdate, GuildRoleUpdate, UserUpdate, GuildEmojisUpdate,
// GuildStickersUpdate). discord.py supplies these via its built-in State,
// but our State accessors return the AFTER value by the time the handler
// runs. We shadow the affected entities here to recover the before/after
// diff that the guild_events handlers need.
//
// All caches are sync.RWMutex-guarded; emoji/sticker variants additionally
// return defensive map copies because callers iterate the result while
// other gateway goroutines may concurrently Replace.

// ── Guild ────────────────────────────────────────────────────────────────

type guildShadow struct {
	mu   sync.RWMutex
	byID map[string]*discordgo.Guild
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

// ── Role ─────────────────────────────────────────────────────────────────

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

// ── Emoji ────────────────────────────────────────────────────────────────

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

// ── Sticker ──────────────────────────────────────────────────────────────

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

// ── User ─────────────────────────────────────────────────────────────────

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

// ── Initial-sync detection ───────────────────────────────────────────────

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
