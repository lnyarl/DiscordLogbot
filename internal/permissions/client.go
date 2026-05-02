package permissions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const DefaultBaseURL = "https://discord.com/api/v10"

// Client is a minimal Discord REST client scoped to the endpoints needed
// for channel-access computation. BaseURL is overridable so httptest can
// inject a mock server.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func NewClient(baseURL, botToken string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL: baseURL,
		Token:   botToken,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

// get returns the HTTP status code; non-200 is signalled via the status,
// not via err, so callers can branch on 404 (e.g. user not in guild).
func (c *Client) get(ctx context.Context, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bot "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, nil
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode %s: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}

type BotGuild struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Member struct {
	Roles []string `json:"roles"`
}

func (c *Client) BotGuilds(ctx context.Context) ([]BotGuild, error) {
	var out []BotGuild
	if _, err := c.get(ctx, "/users/@me/guilds", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) Member(ctx context.Context, guildID, userID string) (*Member, int, error) {
	var m Member
	status, err := c.get(ctx, fmt.Sprintf("/guilds/%s/members/%s", guildID, userID), &m)
	if status != http.StatusOK {
		return nil, status, err
	}
	return &m, status, err
}

func (c *Client) Channels(ctx context.Context, guildID string) ([]Channel, int, error) {
	var out []Channel
	status, err := c.get(ctx, fmt.Sprintf("/guilds/%s/channels", guildID), &out)
	if status != http.StatusOK {
		return nil, status, err
	}
	return out, status, err
}

func (c *Client) Guild(ctx context.Context, guildID string) (*Guild, int, error) {
	var g Guild
	status, err := c.get(ctx, fmt.Sprintf("/guilds/%s", guildID), &g)
	if status != http.StatusOK {
		return nil, status, err
	}
	return &g, status, err
}
