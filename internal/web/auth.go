package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lnyarl/discordlogbot/internal/cache"
	"github.com/lnyarl/discordlogbot/internal/permissions"
)

// AuthHandler bundles the dependencies the OAuth flow needs. The Verifier
// here is the SESSION verifier (no `type` claim required).
type AuthHandler struct {
	OAuth       OAuth2Config
	JWTSecret   []byte
	Pool        *pgxpool.Pool
	Permissions *permissions.Client
	// Secure controls whether issued cookies set the Secure flag. Set
	// from a config flag at startup so dev environments over plain HTTP
	// still work (Discord's redirect requires HTTPS in production though).
	Secure bool
}

// IndexHandler renders the login page when no session is set, redirecting
// to /search otherwise. Mirrors web/auth.py index().
func (h *AuthHandler) IndexHandler(tpl *Templates) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := SessionFrom(r.Context()); ok {
			http.Redirect(w, r, "/search", http.StatusSeeOther)
			return
		}
		tpl.Render(w, "login.html", PageData{})
	})
}

// LoginHandler issues the Discord authorize redirect with a fresh state
// cookie. Mirrors web/auth.py login().
func (h *AuthHandler) LoginHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state, err := randomState()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		SetOAuthStateCookie(w, state, h.Secure)
		http.Redirect(w, r, h.OAuth.AuthorizeURL(state), http.StatusSeeOther)
	})
}

// CallbackHandler validates the OAuth state cookie, exchanges the code for
// an access token, fetches the user, computes accessible channels, writes
// the cache, and finally mints the cookie session JWT.
func (h *AuthHandler) CallbackHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")
		if code == "" || state == "" {
			http.Error(w, "missing code/state", http.StatusBadRequest)
			return
		}
		cookieState := ReadOAuthStateCookie(r)
		if state != cookieState || cookieState == "" {
			slog.Warn("OAuth state mismatch", "state", state, "cookie", cookieState)
			http.Error(w, "Invalid state", http.StatusBadRequest)
			return
		}

		ctx := r.Context()
		tok, err := h.OAuth.ExchangeCode(ctx, code)
		if err != nil {
			slog.Warn("token exchange failed", "err", err)
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}
		user, err := h.OAuth.FetchUser(ctx, tok.AccessToken)
		if err != nil {
			slog.Error("fetch user failed", "err", err)
			http.Error(w, "Discord 사용자 정보를 가져올 수 없습니다. 다시 시도해주세요.", http.StatusBadGateway)
			return
		}

		// Pre-compute the accessible channel set so /api/channels and
		// /api/search can lazy-fill from the cache. Failure here is
		// non-fatal: search will retry the computation on first hit.
		if h.Permissions != nil && h.Pool != nil {
			if err := h.populateCache(ctx, user.ID); err != nil {
				slog.Error("populate channel cache", "err", err, "user_id", user.ID)
			}
		}

		token, err := MintSession(h.JWTSecret, user.ID, user.Username)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		SetSessionCookie(w, token, h.Secure)
		ClearOAuthStateCookie(w)
		http.Redirect(w, r, "/search", http.StatusSeeOther)
	})
}

// LogoutHandler is a POST-only endpoint guarded by an Origin/Referer check
// (CSRF mitigation matching web/auth.py logout()).
func (h *AuthHandler) LogoutHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !originAllowed(r, h.OAuth.RedirectURI) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		ClearSessionCookie(w, h.Secure)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
}

// populateCache mirrors the Python callback's permissions.compute → cache
// write. Surfaces compute errors so the caller can log them.
func (h *AuthHandler) populateCache(ctx context.Context, userID string) error {
	channels, err := permissions.ComputeAccessibleChannels(ctx, h.Permissions, userID)
	if err != nil {
		return err
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
	return cache.Write(ctx, h.Pool, userID, out)
}

// originAllowed mirrors Python's `allowed = DISCORD_REDIRECT_URI.rsplit("/", 2)[0]`
// — extract scheme://host (everything before the path tail) and require
// Origin or Referer to start with it.
func originAllowed(r *http.Request, redirectURI string) bool {
	allowed := strings.TrimSuffix(stripPath(redirectURI), "/")
	if allowed == "" {
		return false
	}
	for _, h := range []string{r.Header.Get("Origin"), r.Header.Get("Referer")} {
		if h != "" && strings.HasPrefix(h, allowed) {
			return true
		}
	}
	return false
}

func stripPath(u string) string {
	// scheme://host[:port][/...]
	const sep = "://"
	idx := strings.Index(u, sep)
	if idx < 0 {
		return ""
	}
	rest := u[idx+len(sep):]
	if slash := strings.Index(rest, "/"); slash >= 0 {
		return u[:idx+len(sep)+slash]
	}
	return u
}

// randomState generates a URL-safe random token for the OAuth state cookie.
// Mirrors secrets.token_urlsafe(16) — 16 random bytes encoded as base64.
func randomState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
