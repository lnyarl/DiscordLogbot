package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/lnyarl/discordlogbot/internal/cache"
	"github.com/lnyarl/discordlogbot/internal/permissions"
)

// discordTokenResp / discordUserResp are the relevant subsets of Discord's
// OAuth token + /users/@me responses.
type discordTokenResp struct {
	AccessToken string `json:"access_token"`
}

type discordUserResp struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// discordExchangeCode trades the Discord OAuth code for a Bearer token.
func (s *Server) discordExchangeCode(ctx context.Context, code string) (*discordTokenResp, error) {
	v := url.Values{}
	v.Set("client_id", s.DiscordClientID)
	v.Set("client_secret", s.DiscordClientSecret)
	v.Set("grant_type", "authorization_code")
	v.Set("code", code)
	v.Set("redirect_uri", s.discordCallbackURL)
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://discord.com/api/oauth2/token", strings.NewReader(v.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange %d", resp.StatusCode)
	}
	var t discordTokenResp
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return nil, err
	}
	if t.AccessToken == "" {
		return nil, errors.New("empty access_token")
	}
	return &t, nil
}

// discordFetchUser hits /users/@me with the Discord access token.
func (s *Server) discordFetchUser(ctx context.Context, accessToken string) (*discordUserResp, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://discord.com/api/v10/users/@me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/users/@me %d", resp.StatusCode)
	}
	var u discordUserResp
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, err
	}
	return &u, nil
}

// populateCache mirrors web/oauth_server.py:discord_callback's
// permissions.compute → cache write. Failure logs only (next lazy fill
// retries from MCP search/get_messages).
func (s *Server) populateCache(ctx context.Context, userID string) {
	if s.Pool == nil || s.Permissions == nil {
		return
	}
	channels, err := permissions.ComputeAccessibleChannels(ctx, s.Permissions, userID)
	if err != nil {
		slog.Error("compute channels", "err", err, "user_id", userID)
		return
	}
	out := make([]cache.Channel, len(channels))
	for i, c := range channels {
		out[i] = cache.Channel{
			ChannelID:    c.ChannelID,
			ChannelName:  c.ChannelName,
			CategoryID:   c.CategoryID,
			CategoryName: c.CategoryName,
			GuildID:      c.GuildID,
			GuildName:    c.GuildName,
		}
	}
	if err := cache.Write(ctx, s.Pool, userID, out); err != nil {
		slog.Error("cache write", "err", err, "user_id", userID)
	}
}
