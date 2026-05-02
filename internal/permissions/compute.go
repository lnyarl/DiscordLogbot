package permissions

import (
	"context"
	"log/slog"
	"net/http"

	"golang.org/x/sync/errgroup"
)

// Channel type enum from the Discord API.
const (
	channelTypeText         = 0
	channelTypeCategory     = 4
	channelTypeAnnouncement = 5
)

// AccessibleChannel mirrors the per-channel record produced by
// Python's compute_accessible_channels.
type AccessibleChannel struct {
	ChannelID    string `json:"channel_id"`
	ChannelName  string `json:"channel_name"`
	CategoryID   string `json:"category_id,omitempty"`
	CategoryName string `json:"category_name,omitempty"`
	GuildID      string `json:"guild_id"`
	GuildName    string `json:"guild_name"`
}

// ComputeAccessibleChannels walks every guild the bot is in, fetches the
// user's member object + the guild's channels + guild metadata in parallel,
// and applies CanViewChannel. It mirrors Python's compute_accessible_channels.
//
// Per-guild errors are logged and treated as "skip this guild" so a single
// flaky guild does not break the whole result.
func ComputeAccessibleChannels(ctx context.Context, c *Client, userID string) ([]AccessibleChannel, error) {
	botGuilds, err := c.BotGuilds(ctx)
	if err != nil {
		return nil, err
	}

	results := make([][]AccessibleChannel, len(botGuilds))
	g, gctx := errgroup.WithContext(ctx)

	for i := range botGuilds {
		bg := botGuilds[i]
		i := i
		g.Go(func() error {
			access, err := fetchGuildAccess(gctx, c, userID, bg)
			if err != nil {
				slog.Error("fetch guild failed", "guild_id", bg.ID, "err", err)
				return nil // match Python: per-guild HTTPError → skip
			}
			results[i] = access
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	var all []AccessibleChannel
	for _, r := range results {
		all = append(all, r...)
	}
	return all, nil
}

func fetchGuildAccess(
	ctx context.Context, c *Client, userID string, bg BotGuild,
) ([]AccessibleChannel, error) {
	var (
		member    *Member
		memStatus int
		chans     []Channel
		guild     *Guild
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		m, s, err := c.Member(gctx, bg.ID, userID)
		member, memStatus = m, s
		// 404 is not an error — user simply not in this guild.
		if err == nil && s != http.StatusOK && s != http.StatusNotFound {
			return nil // other non-200: treat as skip (matches Python)
		}
		return err
	})
	g.Go(func() error {
		cs, _, err := c.Channels(gctx, bg.ID)
		chans = cs
		return err
	})
	g.Go(func() error {
		gd, _, err := c.Guild(gctx, bg.ID)
		guild = gd
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}

	if memStatus == http.StatusNotFound || member == nil || chans == nil || guild == nil {
		return nil, nil
	}

	memberRoles := make(map[string]struct{}, len(member.Roles))
	for _, r := range member.Roles {
		memberRoles[r] = struct{}{}
	}
	categories := make(map[string]*Channel)
	for i := range chans {
		if chans[i].Type == channelTypeCategory {
			categories[chans[i].ID] = &chans[i]
		}
	}

	var out []AccessibleChannel
	for i := range chans {
		ch := &chans[i]
		if ch.Type != channelTypeText && ch.Type != channelTypeAnnouncement {
			continue
		}
		if !CanViewChannel(ch, memberRoles, guild, userID, bg.ID, categories) {
			continue
		}
		var catName string
		if ch.ParentID != "" {
			if cat, ok := categories[ch.ParentID]; ok {
				catName = cat.Name
			}
		}
		out = append(out, AccessibleChannel{
			ChannelID:    ch.ID,
			ChannelName:  ch.Name,
			CategoryID:   ch.ParentID,
			CategoryName: catName,
			GuildID:      bg.ID,
			GuildName:    bg.Name,
		})
	}
	return out, nil
}
