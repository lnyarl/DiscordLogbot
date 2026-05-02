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

// BotGuilds mirrors Python's r.raise_for_status() — any non-200 (e.g. 429
// rate-limit, 5xx) becomes an error so callers don't silently treat it as
// "bot is in zero guilds" and overwrite user caches with empty results.
func (c *Client) BotGuilds(ctx context.Context) ([]BotGuild, error) {
	var out []BotGuild
	status, err := c.get(ctx, "/users/@me/guilds", &out)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("BotGuilds: unexpected status %d", status)
	}
	return out, nil
}

// Member returns the member object plus the raw HTTP status. 404 is a
// signalling status (= user not in this guild) that callers branch on,
// so it is NOT converted to error. Any other non-200 IS an error.
func (c *Client) Member(ctx context.Context, guildID, userID string) (*Member, int, error) {
	var m Member
	status, err := c.get(ctx, fmt.Sprintf("/guilds/%s/members/%s", guildID, userID), &m)
	if err != nil {
		return nil, status, err
	}
	if status == http.StatusNotFound {
		return nil, status, nil
	}
	if status != http.StatusOK {
		return nil, status, fmt.Errorf("Member %s/%s: unexpected status %d", guildID, userID, status)
	}
	return &m, status, nil
}

func (c *Client) Channels(ctx context.Context, guildID string) ([]Channel, error) {
	var out []Channel
	status, err := c.get(ctx, fmt.Sprintf("/guilds/%s/channels", guildID), &out)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Channels %s: unexpected status %d", guildID, status)
	}
	return out, nil
}

func (c *Client) Guild(ctx context.Context, guildID string) (*Guild, error) {
	var g Guild
	status, err := c.get(ctx, fmt.Sprintf("/guilds/%s", guildID), &g)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Guild %s: unexpected status %d", guildID, status)
	}
	return &g, nil
}
