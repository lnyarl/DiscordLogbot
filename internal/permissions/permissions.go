// Package permissions evaluates Discord channel access for a user.
//
// This is a 1:1 translation of web/permissions.py's pure evaluation
// functions. The Discord-spec precedence is preserved exactly:
//
//	owner → server roles → ADMINISTRATOR → category overwrites → channel overwrites
//
// And within a single overwrite set:
//
//	@everyone → role deny batch → role allow batch → member individual
//
// Permission values arrive from the Discord API as decimal strings of a
// 64-bit bitfield, so all arithmetic is performed in uint64.
package permissions

import "strconv"

const (
	PermViewChannel uint64 = 1 << 10
	PermAdmin       uint64 = 1 << 3
)

// Overwrite mirrors a Discord PermissionOverwrite object.
// allow/deny are decimal-encoded uint64 bitfields (Discord serializes them
// as strings in the API).
type Overwrite struct {
	ID    string `json:"id"`
	Type  int    `json:"type"` // 0=role, 1=member
	Allow string `json:"allow"`
	Deny  string `json:"deny"`
}

type Role struct {
	ID          string `json:"id"`
	Permissions string `json:"permissions"`
}

type Channel struct {
	ID                   string      `json:"id"`
	Name                 string      `json:"name"`
	Type                 int         `json:"type"`
	ParentID             string      `json:"parent_id,omitempty"`
	PermissionOverwrites []Overwrite `json:"permission_overwrites"`
}

type Guild struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	OwnerID string `json:"owner_id"`
	Roles   []Role `json:"roles"`
}

func parsePerm(s string) uint64 {
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}

// applyOverwrites applies a single overwrite set in Discord-spec order.
// It does NOT consume the role-permissions field; that is folded in by
// CanViewChannel before calling this.
func applyOverwrites(
	perms uint64,
	overwrites []Overwrite,
	memberRoles map[string]struct{},
	userID, guildID string,
) uint64 {
	// 1. @everyone overwrite (role overwrite whose id == guild_id)
	for _, ow := range overwrites {
		if ow.ID == guildID {
			perms &^= parsePerm(ow.Deny)
			perms |= parsePerm(ow.Allow)
		}
	}

	// 2. role overwrites — Discord spec: deny全合算 → allow全合算 (batch)
	var roleDeny, roleAllow uint64
	for _, ow := range overwrites {
		if _, ok := memberRoles[ow.ID]; ok {
			roleDeny |= parsePerm(ow.Deny)
			roleAllow |= parsePerm(ow.Allow)
		}
	}
	perms &^= roleDeny
	perms |= roleAllow

	// 3. member individual overwrite (highest precedence)
	for _, ow := range overwrites {
		if ow.ID == userID {
			perms &^= parsePerm(ow.Deny)
			perms |= parsePerm(ow.Allow)
		}
	}

	return perms
}

// CanViewChannel reports whether the given user can VIEW_CHANNEL on the
// channel under Discord's full precedence rules.
func CanViewChannel(
	channel *Channel,
	memberRoles map[string]struct{},
	guild *Guild,
	userID, guildID string,
	categories map[string]*Channel,
) bool {
	if guild.OwnerID == userID {
		return true
	}

	// Base server permissions: @everyone role first, then OR every member role.
	var perms uint64
	for _, r := range guild.Roles {
		if r.ID == guildID {
			perms = parsePerm(r.Permissions)
			break
		}
	}
	for _, r := range guild.Roles {
		if _, ok := memberRoles[r.ID]; ok {
			perms |= parsePerm(r.Permissions)
		}
	}

	if perms&PermAdmin != 0 {
		return true
	}

	// Category overwrites first (channel overwrites can override category).
	if channel.ParentID != "" {
		if cat, ok := categories[channel.ParentID]; ok {
			perms = applyOverwrites(perms, cat.PermissionOverwrites, memberRoles, userID, guildID)
		}
	}
	perms = applyOverwrites(perms, channel.PermissionOverwrites, memberRoles, userID, guildID)

	return perms&PermViewChannel != 0
}
