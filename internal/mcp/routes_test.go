package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lnyarl/discordlogbot/internal/auth"
	"github.com/lnyarl/discordlogbot/internal/permissions"
)

const testSecret = "phase2-test-secret"

// stubLister returns a fixed channel set keyed by user_id so we can prove
// tool dispatch reaches the right user.
type stubLister struct {
	byUser map[string][]permissions.AccessibleChannel
}

func (s *stubLister) ListChannels(_ context.Context, uid string) ([]permissions.AccessibleChannel, error) {
	return s.byUser[uid], nil
}

func mintToken(t *testing.T, sub, typ string, exp time.Duration) string {
	t.Helper()
	claims := auth.Claims{
		Type: typ,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   sub,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(exp)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func newTestServer(t *testing.T, lister ChannelLister) (*httptest.Server, *Handler) {
	t.Helper()
	srv := NewServer(lister)
	verifier := auth.NewMCPVerifier(testSecret)
	// httptest binds to 127.0.0.1 with a Host header that doesn't match
	// the loopback hostname rule, so the SDK's DNS-rebinding guard would
	// otherwise reject every request.
	h := NewHandler(verifier, srv, &HandlerOptions{DisableLocalhostProtection: true})
	mux := http.NewServeMux()
	h.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, h
}

// bearerTransport adds the Authorization header to every request.
type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	if t.token != "" {
		r2.Header.Set("Authorization", "Bearer "+t.token)
	}
	return t.base.RoundTrip(r2)
}

// ── Direct SSE-over-HTTP tests (let us see status codes and capture
//    sessionid without going through the SDK client) ─────────────────────

// openSSE issues GET /mcp/sse, reads bytes until the endpoint event is
// observed, returns the session id and a func to close the connection.
func openSSE(t *testing.T, ts *httptest.Server, token string) (string, func()) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/mcp/sse", nil)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("sse status=%d body=%q", resp.StatusCode, string(body))
	}

	// Read until we see "?sessionid=..." in the body.
	const max = 4096
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 256)
	deadline := time.Now().Add(2 * time.Second)
	var sid string
	for time.Now().Before(deadline) {
		n, err := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if i := strings.Index(string(buf), "sessionid="); i >= 0 {
				rest := string(buf[i+len("sessionid="):])
				// session id ends at the first non-token byte (\n, \r, &, etc.)
				end := strings.IndexAny(rest, "\r\n&\" ")
				if end < 0 {
					end = len(rest)
				}
				sid = rest[:end]
				if sid != "" {
					break
				}
			}
		}
		if err != nil {
			break
		}
		if len(buf) > max {
			break
		}
	}
	if sid == "" {
		_ = resp.Body.Close()
		t.Fatalf("never observed sessionid; body so far: %q", string(buf))
	}

	closer := func() { _ = resp.Body.Close() }
	return sid, closer
}

// postMessage POSTs a tiny JSON-RPC notification to the messages endpoint.
// Body content is irrelevant for the auth/ownership checks.
func postMessage(t *testing.T, ts *httptest.Server, token, sid string) *http.Response {
	t.Helper()
	body := strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	req, err := http.NewRequest(http.MethodPost,
		ts.URL+"/mcp/sse?sessionid="+sid, body)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

// ── 보안 시나리오 ──────────────────────────────────────────────────────────

func TestSSE_RejectsMissingAuth(t *testing.T) {
	ts, _ := newTestServer(t, &stubLister{})
	resp, err := http.Get(ts.URL + "/mcp/sse")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", resp.StatusCode)
	}
}

func TestSSE_RejectsAlgNoneToken(t *testing.T) {
	ts, _ := newTestServer(t, &stubLister{})
	// Forge an unsigned token — alg=none is the classic JWT bypass.
	claims := auth.Claims{
		Type: "mcp_access",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "alice",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	signed, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/mcp/sse", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401 (alg=none must be rejected)", resp.StatusCode)
	}
}

func TestSSE_RejectsExpiredToken(t *testing.T) {
	ts, _ := newTestServer(t, &stubLister{})
	tok := mintToken(t, "user-1", "mcp_access", -time.Minute)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/mcp/sse", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", resp.StatusCode)
	}
}

func TestMessages_RejectsForeignSessionInjection(t *testing.T) {
	ts, h := newTestServer(t, &stubLister{})
	alice := mintToken(t, "alice", "mcp_access", time.Hour)
	bob := mintToken(t, "bob", "mcp_access", time.Hour)

	sid, closeSSE := openSSE(t, ts, alice)
	defer closeSSE()

	// Alice's session must be registered.
	if owner, ok := h.sessions.Owner(sid); !ok || owner != "alice" {
		t.Fatalf("session owner: got=(%q,%v), want=(alice,true)", owner, ok)
	}

	// Bob attempts to inject into Alice's session.
	resp := postMessage(t, ts, bob, sid)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", resp.StatusCode)
	}
}

func TestMessages_RejectsUnknownSession(t *testing.T) {
	ts, _ := newTestServer(t, &stubLister{})
	alice := mintToken(t, "alice", "mcp_access", time.Hour)

	resp := postMessage(t, ts, alice, "ABCDEFGHIJKLMNOPQRSTUVWXYZ23")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", resp.StatusCode)
	}
}

func TestMessages_OwnerCanPost(t *testing.T) {
	ts, _ := newTestServer(t, &stubLister{})
	alice := mintToken(t, "alice", "mcp_access", time.Hour)

	sid, closeSSE := openSSE(t, ts, alice)
	defer closeSSE()

	resp := postMessage(t, ts, alice, sid)
	defer resp.Body.Close()
	// SDK responds 202 Accepted on a successfully-routed message.
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d, want 202; body=%q", resp.StatusCode, string(body))
	}
}

// ── 동시성 race 검증 (go test -race가 본 검사를 가치 있게 만듦) ───────────

func TestSessions_ConcurrentSetOwnerDelete(t *testing.T) {
	s := newSessionStore()
	const n = 200
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sid := fmt.Sprintf("S%030d", i)
			s.Set(sid, fmt.Sprintf("user-%d", i))
			_, _ = s.Owner(sid)
			s.Delete(sid)
		}(i)
	}
	wg.Wait()
	if got := s.Len(); got != 0 {
		t.Fatalf("expected empty store after deletes, got len=%d", got)
	}
}

func TestSSE_ConcurrentConnects(t *testing.T) {
	ts, h := newTestServer(t, &stubLister{})
	const n = 8
	var wg sync.WaitGroup
	closers := make([]func(), 0, n)
	var mu sync.Mutex
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok := mintToken(t, fmt.Sprintf("u-%d", i), "mcp_access", time.Hour)
			sid, closeSSE := openSSE(t, ts, tok)
			mu.Lock()
			closers = append(closers, closeSSE)
			mu.Unlock()
			if owner, ok := h.sessions.Owner(sid); !ok || owner != fmt.Sprintf("u-%d", i) {
				t.Errorf("owner mismatch sid=%s: got=%q ok=%v", sid, owner, ok)
			}
		}(i)
	}
	wg.Wait()
	for _, c := range closers {
		c()
	}
}

// ── list_channels 툴 동작 ─────────────────────────────────────────────────

func TestListChannelsTool_PerUserRouting(t *testing.T) {
	stub := &stubLister{byUser: map[string][]permissions.AccessibleChannel{
		"alice": {{ChannelID: "c1", ChannelName: "alice-ch", GuildID: "g1", GuildName: "G"}},
		"bob":   {{ChannelID: "c2", ChannelName: "bob-ch", GuildID: "g1", GuildName: "G"}},
	}}
	ts, _ := newTestServer(t, stub)
	tok := mintToken(t, "alice", "mcp_access", time.Hour)

	transport := &mcpsdk.SSEClientTransport{
		Endpoint:   ts.URL + "/mcp/sse",
		HTTPClient: &http.Client{Transport: bearerTransport{token: tok, base: http.DefaultTransport}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "0.1"}, nil)
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "list_channels"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("empty content")
	}
	text := res.Content[0].(*mcpsdk.TextContent).Text
	var got []permissions.AccessibleChannel
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode: %v; raw=%q", err, text)
	}
	if len(got) != 1 || got[0].ChannelID != "c1" {
		t.Fatalf("alice should see c1 only, got %#v", got)
	}
}
