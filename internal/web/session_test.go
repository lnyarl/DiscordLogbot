package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/lnyarl/discordlogbot/internal/auth"
)

func TestOriginAllowed(t *testing.T) {
	redirect := "https://historian.stashy.in/auth/callback"
	tests := []struct {
		name        string
		origin, ref string
		want        bool
	}{
		{"matching origin", "https://historian.stashy.in", "", true},
		{"matching referer", "", "https://historian.stashy.in/search", true},
		{"different host", "https://evil.example", "", false},
		{"empty both", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/auth/logout", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.ref != "" {
				req.Header.Set("Referer", tt.ref)
			}
			if got := originAllowed(req, redirect); got != tt.want {
				t.Errorf("got=%v want=%v", got, tt.want)
			}
		})
	}
}

func TestMintSession_RoundTrip(t *testing.T) {
	secret := []byte("test-secret")
	tok, err := MintSession(secret, "U123", "alice")
	if err != nil {
		t.Fatalf("mint failed: %v", err)
	}
	v := auth.NewSessionVerifier("test-secret")
	claims, err := v.VerifyClaims(tok)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if claims.Subject != "U123" {
		t.Errorf("sub = %q want U123", claims.Subject)
	}
	if claims.Username != "alice" {
		t.Errorf("username = %q want alice", claims.Username)
	}
}

func TestRequireSession_RedirectsHTML(t *testing.T) {
	v := auth.NewSessionVerifier("s")
	h := RequireSession(v, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner handler must not run on missing cookie")
	}))
	req := httptest.NewRequest("GET", "/search", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Errorf("html path code=%d want %d", w.Code, http.StatusSeeOther)
	}
	if w.Header().Get("Location") != "/" {
		t.Errorf("redirect target = %q", w.Header().Get("Location"))
	}
}

func TestRequireSession_API401JSON(t *testing.T) {
	v := auth.NewSessionVerifier("s")
	h := RequireSession(v, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner must not run")
	}))
	req := httptest.NewRequest("GET", "/api/search", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("api code=%d want 401", w.Code)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("content-type=%q", w.Header().Get("Content-Type"))
	}
}

func TestRequireSession_AcceptsValidCookie(t *testing.T) {
	secret := []byte("s")
	v := auth.NewSessionVerifier("s")
	tok, _ := MintSession(secret, "U1", "bob")

	called := false
	h := RequireSession(v, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		s, ok := SessionFrom(r.Context())
		if !ok {
			t.Error("session not attached to ctx")
		}
		if s.UserID != "U1" || s.Username != "bob" {
			t.Errorf("session = %+v", s)
		}
	}))
	req := httptest.NewRequest("GET", "/search", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: tok})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !called {
		t.Error("inner handler did not run")
	}
}

func TestRequireSession_RejectsExpiredCookie(t *testing.T) {
	secret := []byte("s")
	// Mint a token with iat/exp in the past.
	c := SessionClaims{
		Username: "x",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "U1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	}
	tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(secret)

	v := auth.NewSessionVerifier("s")
	h := RequireSession(v, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner must not run on expired token")
	}))
	req := httptest.NewRequest("GET", "/search", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: tok})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Errorf("expired token code=%d", w.Code)
	}
}

func TestSessionCookieFlow(t *testing.T) {
	w := httptest.NewRecorder()
	SetSessionCookie(w, "abc", false)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Value != "abc" {
		t.Fatalf("cookies = %+v", cookies)
	}
	if cookies[0].MaxAge <= 0 {
		t.Errorf("max-age = %d", cookies[0].MaxAge)
	}

	w = httptest.NewRecorder()
	ClearSessionCookie(w, true)
	cookies = w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge != -1 {
		t.Errorf("clear cookies = %+v", cookies)
	}
}
