// Package web is the Phase 5 HTTP layer: Discord OAuth login, cookie
// session JWT, search UI/API, static file serving, and the rate limit
// middleware. It is mounted alongside the Phase 2 MCP routes inside
// cmd/web/main.go.
package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/lnyarl/discordlogbot/internal/auth"
)

// SessionCookieName is the cookie under which the signed session JWT lives.
// Matches Python web/auth.py's `set_cookie("session", token, ...)`.
const SessionCookieName = "session"

// SessionExpire mirrors web/auth.py JWT_EXPIRE_HOURS — 24h.
const SessionExpire = 24 * time.Hour

// SessionClaims is what the cookie session JWT carries: user id, username,
// expiry. Python's web/auth.py minted this exact shape (no `type` claim,
// no `guild_ids` after the Phase 5 channel_access_cache migration).
type SessionClaims struct {
	Username string `json:"username,omitempty"`
	jwt.RegisteredClaims
}

// MintSession signs a fresh cookie session JWT with HS256.
func MintSession(secret []byte, userID, username string) (string, error) {
	claims := SessionClaims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(SessionExpire)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(secret)
}

// ── Context-bound user ───────────────────────────────────────────────────

type sessionCtxKey struct{}

// Session is the resolved cookie payload.
type Session struct {
	UserID   string
	Username string
}

func WithSession(ctx context.Context, s Session) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, s)
}

// SessionFrom returns the session attached by RequireSession middleware.
// The bool is false when the request was not authenticated (handlers
// should never see this if they're behind RequireSession).
func SessionFrom(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(sessionCtxKey{}).(Session)
	return s, ok
}

// ── Middleware ───────────────────────────────────────────────────────────

// AuthOptional verifies the session cookie (when present) and attaches the
// resolved user to the request context. Used by handlers that render
// differently for logged-in vs anonymous (the / index page).
func AuthOptional(verifier *auth.Verifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c := readSessionCookie(r); c != "" {
			if claims, err := verifier.VerifyClaims(c); err == nil {
				r = r.WithContext(WithSession(r.Context(), Session{
					UserID:   claims.Subject,
					Username: claims.Username,
				}))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireSession is the middleware applied to /search, /api/*, /auth/logout.
// 401s any request that doesn't carry a valid session cookie.
func RequireSession(verifier *auth.Verifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := readSessionCookie(r)
		if c == "" {
			redirectOrUnauthorized(w, r)
			return
		}
		claims, err := verifier.VerifyClaims(c)
		if err != nil {
			redirectOrUnauthorized(w, r)
			return
		}
		r = r.WithContext(WithSession(r.Context(), Session{
			UserID:   claims.Subject,
			Username: claims.Username,
		}))
		next.ServeHTTP(w, r)
	})
}

func readSessionCookie(r *http.Request) string {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// redirectOrUnauthorized: API paths get a 401 JSON response; HTML page
// requests get redirected to /. Mirrors the Python search.py behavior
// (HTMLResponse 401 + JSONResponse 401 split).
func redirectOrUnauthorized(w http.ResponseWriter, r *http.Request) {
	if isAPIRequest(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Unauthorized"}`))
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func isAPIRequest(r *http.Request) bool {
	return len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/"
}

// ClearSessionCookie issues a Set-Cookie header that immediately expires
// the session cookie — the logout response uses it.
func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// SetSessionCookie issues a long-lived session cookie carrying the JWT.
func SetSessionCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(SessionExpire.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// Errors helpers
var (
	ErrNoSession = errors.New("no session")
)
