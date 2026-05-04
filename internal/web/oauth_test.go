package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAuthorizeURL(t *testing.T) {
	c := OAuth2Config{
		ClientID:    "client123",
		RedirectURI: "http://localhost:8080/auth/callback",
		Scopes:      "identify guilds guilds.members.read",
	}
	got := c.AuthorizeURL("STATE_X")

	// Python uses literal " " → "%20" inside the scope (not '+'). We must
	// preserve the same quirk so any code/test golden matching the URL
	// stays consistent.
	if !strings.Contains(got, "scope=identify%20guilds%20guilds.members.read") {
		t.Errorf("scope encoding wrong: %s", got)
	}
	if !strings.Contains(got, "client_id=client123") {
		t.Errorf("missing client_id: %s", got)
	}
	if !strings.Contains(got, "state=STATE_X") {
		t.Errorf("missing state: %s", got)
	}
	if !strings.HasPrefix(got, "https://discord.com/oauth2/authorize?") {
		t.Errorf("wrong host/path: %s", got)
	}
}

func TestExchangeCode_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// We test against this URL by overriding Discord's host below; we
		// can't inject a different URL, so test via the Token endpoint
		// via a custom transport on the http.Client.
		_ = r.ParseForm()
		if r.FormValue("grant_type") != "authorization_code" {
			t.Errorf("grant_type=%q", r.FormValue("grant_type"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok123",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	// Override the Discord host via a custom transport.
	c := OAuth2Config{
		ClientID:     "x",
		ClientSecret: "s",
		RedirectURI:  "http://localhost/cb",
		HTTPClient: &http.Client{
			Transport: rewriteTransport{target: srv.URL},
		},
	}
	tok, err := c.ExchangeCode(context.Background(), "auth_code")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if tok.AccessToken != "tok123" {
		t.Errorf("access_token = %q", tok.AccessToken)
	}
}

func TestFetchUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer at" {
			t.Errorf("authz=%q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "U1", "username": "alice",
		})
	}))
	defer srv.Close()
	c := OAuth2Config{
		HTTPClient: &http.Client{Transport: rewriteTransport{target: srv.URL}},
	}
	u, err := c.FetchUser(context.Background(), "at")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if u.ID != "U1" || u.Username != "alice" {
		t.Errorf("user = %+v", u)
	}
}

func TestStateCookieFlow(t *testing.T) {
	w := httptest.NewRecorder()
	SetOAuthStateCookie(w, "S1", false)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Value != "S1" {
		t.Fatalf("cookies = %+v", cookies)
	}
	if cookies[0].SameSite != http.SameSiteNoneMode {
		t.Errorf("SameSite=%v want None", cookies[0].SameSite)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: OAuthStateCookieName, Value: "stored"})
	if got := ReadOAuthStateCookie(req); got != "stored" {
		t.Errorf("read=%q", got)
	}
}

// rewriteTransport rewrites every Request URL host to point at our test
// server so OAuth2Config's hardcoded discord.com URLs resolve to it.
type rewriteTransport struct{ target string }

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target, err := url.Parse(rt.target)
	if err != nil {
		return nil, err
	}
	req.URL.Scheme = target.Scheme
	req.URL.Host = target.Host
	req.Host = target.Host
	return http.DefaultTransport.RoundTrip(req)
}
