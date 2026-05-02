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
				// Match Python: log per-guild HTTP errors and skip the guild
				// rather than failing the whole computation.
				slog.Error("fetch guild failed", "guild_id", bg.ID, "err", err)
				return nil
			}
			results[i] = access
			return nil
		})
	}
	// Inner goroutines always return nil (errors are swallowed and logged
	// above), so g.Wait() can never return an error. Call it for the
	// barrier effect only.
	_ = g.Wait()

	var all []AccessibleChannel
	for _, r := range results {
		all = append(all, r...)
	}
	return all, nil
}

func fetchGuildAccess(
	ctx context.Context, c *Client, userID string, bg BotGuild,
) ([]AccessibleChannel, error) {
	// Each goroutine writes to a *different* shared variable below, so no
	// mutex is needed; errgroup.Wait acts as the happens-before barrier.
	var (
		member    *Member
		memStatus int
		chans     []Channel
		guild     *Guild
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		m, s, err := c.Member(gctx, bg.ID, userID)
		if err != nil {
			return err
		}
		member, memStatus = m, s
		return nil
	})
	g.Go(func() error {
		cs, err := c.Channels(gctx, bg.ID)
		if err != nil {
			return err
		}
		chans = cs
		return nil
	})
	g.Go(func() error {
		gd, err := c.Guild(gctx, bg.ID)
		if err != nil {
			return err
		}
		guild = gd
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}

	// 404 on Member = user not in this guild → empty result, not an error.
	if memStatus == http.StatusNotFound || member == nil {
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
