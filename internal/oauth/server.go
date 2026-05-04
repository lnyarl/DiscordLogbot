// Package oauth implements the OAuth 2.0 authorization server that mints
// access tokens for the MCP API. It mirrors web/oauth_server.py:
//
//   - GET /.well-known/oauth-authorization-server  RFC 8414 metadata
//   - GET /oauth/authorize                          AI client → Discord redirect
//   - GET /oauth/discord_callback                   Discord → auth code mint
//   - POST /oauth/token                             auth code → access token
//
// PKCE S256 + JWT-encoded auth codes (10 min) + JTI one-time consumption +
// static client_id whitelist + localhost-auto redirect_uri allowlist.
//
// File layout within this package:
//   - server.go     this file: Server struct, New, Mount, 4 route handlers
//   - allowlist.go  client_id + redirect_uri policy
//   - jti.go        one-time JTI store
//   - tokens.go     state / auth code / access token JWT sign + parse
//   - pkce.go       VerifyPKCE primitive
//   - discord.go    Discord OAuth code exchange + /users/@me fetch
package oauth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lnyarl/discordlogbot/internal/permissions"
)

// AuthCodeTTL = 10 min, matches Python.
const AuthCodeTTL = 10 * time.Minute

// AccessTokenTTL = 24 h, matches Python.
const AccessTokenTTL = 24 * time.Hour

// StateTTL = 10 min for the Discord OAuth state parameter we encode.
const StateTTL = 10 * time.Minute

// Server is the dependency bundle the HTTP routes share.
type Server struct {
	BaseURL              string
	JWTSecret            []byte
	DiscordClientID      string
	DiscordClientSecret  string
	AllowedClientIDs     map[string]struct{}
	StaticRedirectURIs   map[string]struct{}
	Pool                 *pgxpool.Pool
	Permissions          *permissions.Client
	HTTPClient           *http.Client
	jtiStore             *jtiStore
	discordCallbackURL   string
}

// New builds an authorization server. allowedClientIDs / staticRedirectURIs
// are comma-separated env values (empty = entry rejected).
func New(
	baseURL string,
	jwtSecret []byte,
	discordClientID, discordClientSecret string,
	allowedClientIDs, staticRedirectURIs string,
	pool *pgxpool.Pool,
	perms *permissions.Client,
) *Server {
	s := &Server{
		BaseURL:             baseURL,
		JWTSecret:           jwtSecret,
		DiscordClientID:     discordClientID,
		DiscordClientSecret: discordClientSecret,
		AllowedClientIDs:    parseSet(allowedClientIDs),
		StaticRedirectURIs:  parseSet(staticRedirectURIs),
		Pool:                pool,
		Permissions:         perms,
		HTTPClient:          &http.Client{Timeout: 10 * time.Second},
		jtiStore:            newJTIStore(),
	}
	s.discordCallbackURL = strings.TrimRight(baseURL, "/") + "/oauth/discord_callback"
	if len(s.AllowedClientIDs) == 0 {
		slog.Warn("MCP_CLIENT_IDS not set — every OAuth request will be rejected")
	}
	return s
}

func parseSet(csv string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, raw := range strings.Split(csv, ",") {
		v := strings.TrimSpace(raw)
		if v != "" {
			out[v] = struct{}{}
		}
	}
	return out
}

// ── Routes ───────────────────────────────────────────────────────────────

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.handleMetadata)
	mux.HandleFunc("GET /oauth/authorize", s.handleAuthorize)
	mux.HandleFunc("GET /oauth/discord_callback", s.handleDiscordCallback)
	mux.HandleFunc("POST /oauth/token", s.handleToken)
}

// metadataResponse mirrors the JSON shape Python emits.
type metadataResponse struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
}

func (s *Server) handleMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(metadataResponse{
		Issuer:                            s.BaseURL,
		AuthorizationEndpoint:             s.BaseURL + "/oauth/authorize",
		TokenEndpoint:                     s.BaseURL + "/oauth/token",
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{"none"},
	})
}

// /oauth/authorize: validate AI client params, build a state JWT carrying
// {redirect_uri, client_state, cc, cid}, redirect to Discord.
func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	responseType := q.Get("response_type")
	codeChallenge := q.Get("code_challenge")
	codeChallengeMethod := q.Get("code_challenge_method")
	if codeChallengeMethod == "" {
		codeChallengeMethod = "S256"
	}
	clientState := q.Get("state")

	// RFC 6749: client_id → redirect_uri first; remaining params after.
	if !s.clientIDAllowed(clientID) {
		http.Error(w, "Unknown client_id", http.StatusBadRequest)
		return
	}
	if !s.redirectURIAllowed(redirectURI) {
		http.Error(w, "redirect_uri not allowed", http.StatusBadRequest)
		return
	}
	if responseType != "code" {
		http.Error(w, "Only response_type=code supported", http.StatusBadRequest)
		return
	}
	if codeChallengeMethod != "S256" {
		http.Error(w, "Only S256 code_challenge_method supported", http.StatusBadRequest)
		return
	}
	if codeChallenge == "" {
		http.Error(w, "code_challenge required", http.StatusBadRequest)
		return
	}

	stateTok, err := s.signState(stateClaims{
		RedirectURI: redirectURI,
		ClientState: clientState,
		CC:          codeChallenge,
		CID:         clientID,
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	v := url.Values{}
	v.Set("client_id", s.DiscordClientID)
	v.Set("redirect_uri", s.discordCallbackURL)
	v.Set("response_type", "code")
	v.Set("scope", "identify guilds guilds.members.read")
	v.Set("state", stateTok)
	http.Redirect(w, r, "https://discord.com/oauth2/authorize?"+v.Encode(), http.StatusSeeOther)
}

func (s *Server) handleDiscordCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	code := q.Get("code")
	stateTok := q.Get("state")
	if code == "" || stateTok == "" {
		http.Error(w, "missing code/state", http.StatusBadRequest)
		return
	}
	state, err := s.parseState(stateTok)
	if err != nil {
		http.Error(w, "Invalid or expired state", http.StatusBadRequest)
		return
	}

	// Exchange code for Discord access token.
	tok, err := s.discordExchangeCode(r.Context(), code)
	if err != nil {
		slog.Warn("discord token exchange failed", "err", err)
		http.Error(w, "Discord token exchange failed", http.StatusBadGateway)
		return
	}

	user, err := s.discordFetchUser(r.Context(), tok.AccessToken)
	if err != nil {
		slog.Warn("discord user fetch failed", "err", err)
		http.Error(w, "Discord user fetch failed", http.StatusBadGateway)
		return
	}
	if user.ID == "" || user.Username == "" {
		http.Error(w, "Discord did not return valid user info", http.StatusBadGateway)
		return
	}

	// Refresh the channel cache. Failure is non-fatal — next lazy fill will retry.
	s.populateCache(r.Context(), user.ID)

	// Defense-in-depth re-check on redirect_uri.
	if !s.redirectURIAllowed(state.RedirectURI) {
		http.Error(w, "redirect_uri not allowed", http.StatusBadRequest)
		return
	}

	authCode, err := s.signAuthCode(user.ID, user.Username, state.CC, state.CID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	v := url.Values{}
	v.Set("code", authCode)
	if state.ClientState != "" {
		v.Set("state", state.ClientState)
	}
	http.Redirect(w, r, state.RedirectURI+"?"+v.Encode(), http.StatusSeeOther)
}

// /oauth/token: trade auth code (+ PKCE verifier) for an access token.
func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	grantType := r.PostForm.Get("grant_type")
	code := r.PostForm.Get("code")
	verifier := r.PostForm.Get("code_verifier")
	clientID := r.PostForm.Get("client_id")
	// redirect_uri is accepted but not validated — Python doesn't either.

	if grantType != "authorization_code" {
		http.Error(w, "Only authorization_code grant type supported", http.StatusBadRequest)
		return
	}
	if code == "" {
		http.Error(w, "code required", http.StatusBadRequest)
		return
	}
	if verifier == "" {
		http.Error(w, "code_verifier required", http.StatusBadRequest)
		return
	}
	c, err := s.parseAuthCode(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if clientID == "" {
		http.Error(w, "client_id is required", http.StatusBadRequest)
		return
	}
	if !s.clientIDAllowed(c.CID) {
		http.Error(w, "Unknown client_id in auth code", http.StatusBadRequest)
		return
	}
	if clientID != c.CID {
		http.Error(w, "client_id mismatch", http.StatusBadRequest)
		return
	}
	if c.JTI == "" {
		http.Error(w, "Invalid auth code: missing jti", http.StatusBadRequest)
		return
	}
	if !s.jtiStore.Consume(c.JTI, AuthCodeTTL) {
		http.Error(w, "Auth code already used", http.StatusBadRequest)
		return
	}
	if !VerifyPKCE(verifier, c.CC) {
		http.Error(w, "PKCE verification failed", http.StatusBadRequest)
		return
	}
	if c.Subject == "" || c.Username == "" {
		http.Error(w, "Invalid auth code: missing claims", http.StatusBadRequest)
		return
	}
	access, err := s.MakeAccessToken(c.Subject, c.Username)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": access,
		"token_type":   "Bearer",
		"expires_in":   int(AccessTokenTTL.Seconds()),
	})
}
