package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ── PKCE primitives ──────────────────────────────────────────────────────

func TestVerifyPKCE_HappyPath(t *testing.T) {
	verifier := "abcdefghijklmnopqrstuvwxyz0123456789-_"
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])
	if !VerifyPKCE(verifier, challenge) {
		t.Error("expected verify success")
	}
}

func TestVerifyPKCE_RejectMismatch(t *testing.T) {
	if VerifyPKCE("verifier", "wrong-challenge") {
		t.Error("expected verify failure")
	}
}

func TestVerifyPKCE_ConstantTimeOnDifferentLengths(t *testing.T) {
	// Empty / short / long-mismatch challenges must all fail without
	// short-circuiting before the timing-safe compare. Behavioral
	// verification only — actual timing is hard to assert in unit tests,
	// but crypto/subtle.ConstantTimeCompare returns 0 on length mismatch
	// without branching on the contents.
	if VerifyPKCE("v", "") {
		t.Error("empty challenge must fail")
	}
	if VerifyPKCE("v", "much longer than the expected sha256 base64") {
		t.Error("longer-than-expected challenge must fail")
	}
}

// ── client_id allowlist ──────────────────────────────────────────────────

func TestParseSet(t *testing.T) {
	got := parseSet("a, b ,, c")
	for _, want := range []string{"a", "b", "c"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %q", want)
		}
	}
	if _, ok := got[""]; ok {
		t.Error("empty entry should be filtered")
	}
}

func TestClientIDAllowed(t *testing.T) {
	s := New("http://localhost", []byte("k"), "did", "dsec", "client-A,client-B", "", nil, nil)
	if !s.clientIDAllowed("client-A") {
		t.Error("client-A should be allowed")
	}
	if s.clientIDAllowed("nope") {
		t.Error("unknown client must be rejected")
	}
}

func TestClientIDAllowed_EmptyAllowlistRejectsAll(t *testing.T) {
	s := New("http://localhost", []byte("k"), "did", "dsec", "", "", nil, nil)
	if s.clientIDAllowed("anything") {
		t.Error("empty allowlist must reject everything")
	}
}

// ── redirect_uri allowlist ───────────────────────────────────────────────

func TestRedirectURIAllowed_StaticAndLocalhost(t *testing.T) {
	s := New("http://localhost", []byte("k"), "did", "dsec",
		"client-A", "https://app.example/cb", nil, nil)

	cases := map[string]bool{
		"https://app.example/cb":               true,
		"http://localhost:54321/cb":             true,
		"http://localhost/cb":                   true,
		"http://127.0.0.1:8080":                 true,
		"http://127.0.0.1":                      true,
		"https://127.0.0.1:8443/cb":             true,
		"https://evil.example/cb":               false,
		"http://localhost:99999/cb":             false, // out-of-range port
		"":                                      false,
		"javascript:alert(1)":                   false,
	}
	for u, want := range cases {
		if got := s.redirectURIAllowed(u); got != want {
			t.Errorf("redirectURIAllowed(%q) = %v, want %v", u, got, want)
		}
	}
}

// ── JTI store ────────────────────────────────────────────────────────────

func TestJTIStore_FirstUseSucceedsSecondFails(t *testing.T) {
	store := newJTIStore()
	if !store.Consume("J1", time.Hour) {
		t.Fatal("first consume must succeed")
	}
	if store.Consume("J1", time.Hour) {
		t.Fatal("reuse must fail")
	}
}

func TestJTIStore_ExpiredEntryEvicted(t *testing.T) {
	store := newJTIStore()
	if !store.Consume("J1", time.Millisecond) {
		t.Fatal("first consume must succeed")
	}
	time.Sleep(20 * time.Millisecond)
	// Trigger purge by consuming a different jti.
	if !store.Consume("J2", time.Hour) {
		t.Fatal("J2 must succeed (different jti)")
	}
	// Now J1 is gone — fresh consume permitted.
	if !store.Consume("J1", time.Hour) {
		t.Error("expired J1 must be eligible again after purge")
	}
}

// ── Metadata endpoint ────────────────────────────────────────────────────

func TestMetadataEndpoint(t *testing.T) {
	s := New("https://example.test", []byte("k"), "did", "dsec", "c", "", nil, nil)
	mux := http.NewServeMux()
	s.Mount(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("code=%d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var m metadataResponse
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Issuer != "https://example.test" {
		t.Errorf("issuer=%q", m.Issuer)
	}
	if m.AuthorizationEndpoint != "https://example.test/oauth/authorize" {
		t.Errorf("auth endpoint=%q", m.AuthorizationEndpoint)
	}
	if len(m.CodeChallengeMethodsSupported) != 1 || m.CodeChallengeMethodsSupported[0] != "S256" {
		t.Errorf("pkce methods=%v", m.CodeChallengeMethodsSupported)
	}
}

// ── /oauth/authorize ─────────────────────────────────────────────────────

func TestAuthorize_RejectsUnknownClient(t *testing.T) {
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &http.Client{CheckRedirect: noFollow}
	resp, _ := c.Get(srv.URL + "/oauth/authorize?" + url.Values{
		"client_id":             []string{"unknown"},
		"redirect_uri":          []string{"http://localhost:1234/cb"},
		"response_type":         []string{"code"},
		"code_challenge":        []string{"x"},
		"code_challenge_method": []string{"S256"},
	}.Encode())
	if resp.StatusCode != 400 {
		t.Errorf("code=%d want 400", resp.StatusCode)
	}
}

func TestAuthorize_RejectsBadRedirectURI(t *testing.T) {
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &http.Client{CheckRedirect: noFollow}
	resp, _ := c.Get(srv.URL + "/oauth/authorize?" + url.Values{
		"client_id":             []string{"client-A"},
		"redirect_uri":          []string{"https://evil.example/cb"},
		"response_type":         []string{"code"},
		"code_challenge":        []string{"x"},
		"code_challenge_method": []string{"S256"},
	}.Encode())
	if resp.StatusCode != 400 {
		t.Errorf("code=%d want 400", resp.StatusCode)
	}
}

func TestAuthorize_RedirectsToDiscord(t *testing.T) {
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &http.Client{CheckRedirect: noFollow}
	resp, _ := c.Get(srv.URL + "/oauth/authorize?" + url.Values{
		"client_id":             []string{"client-A"},
		"redirect_uri":          []string{"http://localhost:1234/cb"},
		"response_type":         []string{"code"},
		"code_challenge":        []string{"abc"},
		"code_challenge_method": []string{"S256"},
		"state":                 []string{"client-state-XYZ"},
	}.Encode())
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("code=%d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "https://discord.com/oauth2/authorize?") {
		t.Errorf("location=%q", loc)
	}
}

// ── /oauth/token full PKCE flow ──────────────────────────────────────────

func TestToken_PKCEHappyPath(t *testing.T) {
	s := newTestServer(t)

	// Mint a verifier + challenge.
	verifier := randomVerifier()
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])

	authCode, err := s.signAuthCode("U1", "alice", challenge, "client-A")
	if err != nil {
		t.Fatalf("signAuthCode: %v", err)
	}

	tokRes := postToken(t, s, url.Values{
		"grant_type":    []string{"authorization_code"},
		"code":          []string{authCode},
		"code_verifier": []string{verifier},
		"client_id":     []string{"client-A"},
	})
	if tokRes.code != http.StatusOK {
		t.Fatalf("code=%d body=%s", tokRes.code, tokRes.body)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(tokRes.body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["token_type"] != "Bearer" {
		t.Errorf("token_type=%v", resp["token_type"])
	}
	access, _ := resp["access_token"].(string)
	if access == "" {
		t.Fatal("missing access_token")
	}
	// Verify the access token signs over sub/username.
	var c accessTokenClaims
	parsed, err := jwt.ParseWithClaims(access, &c, func(t *jwt.Token) (any, error) {
		return s.JWTSecret, nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("parse access token: %v", err)
	}
	if c.Subject != "U1" || c.Username != "alice" || c.Type != "mcp_access" {
		t.Errorf("claims=%+v", c)
	}
}

func TestToken_RejectsInvalidPKCE(t *testing.T) {
	s := newTestServer(t)
	authCode, _ := s.signAuthCode("U1", "alice", "right-challenge", "client-A")

	tr := postToken(t, s, url.Values{
		"grant_type":    []string{"authorization_code"},
		"code":          []string{authCode},
		"code_verifier": []string{"wrong-verifier"},
		"client_id":     []string{"client-A"},
	})
	if tr.code != http.StatusBadRequest {
		t.Errorf("code=%d", tr.code)
	}
}

func TestToken_JTIReuseRejected(t *testing.T) {
	s := newTestServer(t)
	verifier := randomVerifier()
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])
	authCode, _ := s.signAuthCode("U1", "alice", challenge, "client-A")

	form := url.Values{
		"grant_type":    []string{"authorization_code"},
		"code":          []string{authCode},
		"code_verifier": []string{verifier},
		"client_id":     []string{"client-A"},
	}
	first := postToken(t, s, form)
	if first.code != 200 {
		t.Fatalf("first call must succeed, got %d", first.code)
	}
	second := postToken(t, s, form)
	if second.code != http.StatusBadRequest || !strings.Contains(second.body, "already used") {
		t.Errorf("reuse: code=%d body=%q", second.code, second.body)
	}
}

func TestToken_RejectsExpiredCode(t *testing.T) {
	s := newTestServer(t)

	// Build an auth code with exp=past.
	verifier := randomVerifier()
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])
	c := authCodeClaims{
		Type:     "mcp_auth_code",
		JTI:      "j1",
		Username: "alice",
		CC:       challenge,
		CID:      "client-A",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "U1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	}
	authCode, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.JWTSecret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	tr := postToken(t, s, url.Values{
		"grant_type":    []string{"authorization_code"},
		"code":          []string{authCode},
		"code_verifier": []string{verifier},
		"client_id":     []string{"client-A"},
	})
	if tr.code != http.StatusBadRequest {
		t.Errorf("expired code: status=%d body=%q", tr.code, tr.body)
	}
}

func TestToken_RejectsClientIDMismatch(t *testing.T) {
	s := newTestServer(t)
	verifier := randomVerifier()
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])
	authCode, _ := s.signAuthCode("U1", "alice", challenge, "client-A")

	tr := postToken(t, s, url.Values{
		"grant_type":    []string{"authorization_code"},
		"code":          []string{authCode},
		"code_verifier": []string{verifier},
		"client_id":     []string{"client-B"}, // differs from auth code's CID
	})
	if tr.code != http.StatusBadRequest || !strings.Contains(tr.body, "mismatch") {
		t.Errorf("mismatch: code=%d body=%q", tr.code, tr.body)
	}
}

func TestToken_RejectsUnknownClientInCode(t *testing.T) {
	s := newTestServer(t)
	// Encode an auth code with a client_id that's NOT in the allowlist —
	// simulates a stolen-secret scenario or operator misconfiguration.
	verifier := randomVerifier()
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])
	authCode, _ := s.signAuthCode("U1", "alice", challenge, "rogue-client")

	tr := postToken(t, s, url.Values{
		"grant_type":    []string{"authorization_code"},
		"code":          []string{authCode},
		"code_verifier": []string{verifier},
		"client_id":     []string{"rogue-client"},
	})
	if tr.code != http.StatusBadRequest || !strings.Contains(tr.body, "Unknown") {
		t.Errorf("unknown client in code: status=%d body=%q", tr.code, tr.body)
	}
}

// ── /oauth/discord_callback (state validation) ────────────────────────────

func TestDiscordCallback_RejectsInvalidState(t *testing.T) {
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.Mount(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &http.Client{CheckRedirect: noFollow}
	resp, _ := c.Get(srv.URL + "/oauth/discord_callback?code=foo&state=tampered")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("code=%d", resp.StatusCode)
	}
}

func TestDiscordCallback_RejectsExpiredState(t *testing.T) {
	s := newTestServer(t)
	// Build a state JWT with exp in the past.
	c := stateClaims{
		RedirectURI: "http://localhost:1234/cb",
		CC:          "x",
		CID:         "client-A",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
		},
	}
	tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.JWTSecret)

	mux := http.NewServeMux()
	s.Mount(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cl := &http.Client{CheckRedirect: noFollow}
	resp, _ := cl.Get(srv.URL + "/oauth/discord_callback?code=foo&state=" + url.QueryEscape(tok))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expired state: code=%d", resp.StatusCode)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────

func newTestServer(t *testing.T) *Server {
	t.Helper()
	return New("https://example.test",
		[]byte("test-secret"),
		"discord-cid", "discord-secret",
		"client-A,client-B",
		"http://app.example/cb",
		nil, nil)
}

type tokenResp struct {
	code int
	body string
}

func postToken(t *testing.T, s *Server, form url.Values) tokenResp {
	t.Helper()
	mux := http.NewServeMux()
	s.Mount(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.PostForm(srv.URL+"/oauth/token", form)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return tokenResp{code: resp.StatusCode, body: strings.TrimSpace(string(body))}
}

func randomVerifier() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func noFollow(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// suppress unused linter when only tests reference these.
var _ = context.Background
var _ = fmt.Sprintf
