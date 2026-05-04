package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DiscordAPI base URL.
const DiscordAPI = "https://discord.com/api/v10"

// OAuth2Config holds the static client configuration for the Discord
// authorization-code flow.
type OAuth2Config struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Scopes       string // space-separated; spaces become "%20" in the auth URL
	HTTPClient   *http.Client
}

// AuthorizeURL builds the OAuth2 authorize redirect for the given state.
// Mirrors web/auth.py login() string-formatting exactly so existing clients
// (Discord console, redirect URIs) work unchanged. Python uses literal
// " " → "%20" (url.QueryEscape would emit "+") in the scope; keep the
// same so OAuth providers parsing strictly behave identically.
func (c OAuth2Config) AuthorizeURL(state string) string {
	scopeEscaped := strings.ReplaceAll(c.Scopes, " ", "%20")
	return fmt.Sprintf(
		"https://discord.com/oauth2/authorize?client_id=%s&redirect_uri=%s&response_type=code&scope=%s&state=%s",
		url.QueryEscape(c.ClientID),
		url.QueryEscape(c.RedirectURI),
		scopeEscaped,
		url.QueryEscape(state),
	)
}

// TokenResponse is the relevant subset of the token endpoint payload.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope"`
}

// User is the relevant subset of /users/@me.
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// ExchangeCode trades the authorization code for an access token. Mirrors
// web/auth.py callback's first httpx.post.
func (c OAuth2Config) ExchangeCode(ctx context.Context, code string) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", c.RedirectURI)

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://discord.com/api/oauth2/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange %d: %s", resp.StatusCode, body)
	}
	var t TokenResponse
	if err := json.Unmarshal(body, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// FetchUser calls /users/@me with the access token.
func (c OAuth2Config) FetchUser(ctx context.Context, accessToken string) (*User, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", DiscordAPI+"/users/@me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/users/@me %d: %s", resp.StatusCode, body)
	}
	var u User
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (c OAuth2Config) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// ── State cookie ─────────────────────────────────────────────────────────

// OAuthStateCookieName matches Python's set_cookie("oauth_state", ...).
const OAuthStateCookieName = "oauth_state"

// SetOAuthStateCookie issues the short-lived state cookie. Note Python uses
// samesite=none so the Discord cross-site redirect carries it back; secure
// must be true for that to be honored by the browser.
func SetOAuthStateCookie(w http.ResponseWriter, state string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     OAuthStateCookieName,
		Value:    state,
		Path:     "/",
		MaxAge:   300, // 5 minutes
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteNoneMode,
	})
}

// ClearOAuthStateCookie deletes the cookie after a successful callback.
func ClearOAuthStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     OAuthStateCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteNoneMode,
	})
}

// ReadOAuthStateCookie returns the state stored in the request, or "" when
// absent.
func ReadOAuthStateCookie(r *http.Request) string {
	c, err := r.Cookie(OAuthStateCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// Sentinel errors callers may distinguish.
var (
	ErrTokenExchangeFailed = errors.New("token exchange failed")
)
