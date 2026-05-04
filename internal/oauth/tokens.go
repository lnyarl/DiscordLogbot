package oauth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// stateClaims is the short-lived JWT we hand to Discord as the OAuth
// `state` parameter. It carries the AI-client context (redirect_uri,
// PKCE challenge, client_id) so we can re-bind the auth code without a
// server-side state store.
type stateClaims struct {
	RedirectURI string `json:"redirect_uri"`
	ClientState string `json:"client_state,omitempty"`
	CC          string `json:"cc"`
	CID         string `json:"cid"`
	jwt.RegisteredClaims
}

// authCodeClaims encodes the 10-minute authorization code redeemed at
// /oauth/token. JTI gives one-time semantics; CC binds PKCE; CID binds
// the originating client_id.
type authCodeClaims struct {
	Type     string `json:"type"`
	JTI      string `json:"jti"`
	Username string `json:"username"`
	CC       string `json:"cc"`
	CID      string `json:"cid"`
	jwt.RegisteredClaims
}

// accessTokenClaims is the 24-hour Bearer issued to the MCP client.
// Channel permissions are NOT embedded — MCP looks them up in
// channel_access_cache by `sub` on every request.
type accessTokenClaims struct {
	Type     string `json:"type"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

func (s *Server) signState(c stateClaims) (string, error) {
	c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(StateTTL))
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return tok.SignedString(s.JWTSecret)
}

func (s *Server) parseState(tok string) (*stateClaims, error) {
	var c stateClaims
	parsed, err := jwt.ParseWithClaims(tok, &c, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return s.JWTSecret, nil
	})
	if err != nil || !parsed.Valid {
		return nil, errors.New("invalid or expired state")
	}
	return &c, nil
}

func (s *Server) signAuthCode(userID, username, cc, cid string) (string, error) {
	jtiBuf := make([]byte, 16)
	if _, err := rand.Read(jtiBuf); err != nil {
		return "", err
	}
	c := authCodeClaims{
		Type:     "mcp_auth_code",
		JTI:      base64.RawURLEncoding.EncodeToString(jtiBuf),
		Username: username,
		CC:       cc,
		CID:      cid,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(AuthCodeTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return tok.SignedString(s.JWTSecret)
}

func (s *Server) parseAuthCode(tok string) (*authCodeClaims, error) {
	var c authCodeClaims
	parsed, err := jwt.ParseWithClaims(tok, &c, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return s.JWTSecret, nil
	})
	if err != nil || !parsed.Valid {
		return nil, errors.New("invalid or expired auth code")
	}
	if c.Type != "mcp_auth_code" {
		return nil, errors.New("invalid token type")
	}
	return &c, nil
}

// MakeAccessToken mints a 24-hour MCP access token (type=mcp_access).
func (s *Server) MakeAccessToken(userID, username string) (string, error) {
	c := accessTokenClaims{
		Type:     "mcp_access",
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(AccessTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return tok.SignedString(s.JWTSecret)
}
