// Package auth verifies HS256 JWTs minted by the OAuth server (Phase 6)
// and exposes the user identity to downstream handlers via context.
//
// Mirrors web/auth.py + web/mcp_router.py's HTTPBearer + JWT validation:
// the token's "type" claim must equal a fixed string (e.g. "mcp_access"),
// and the "sub" claim is the authenticated Discord user id.
package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// Errors callers may inspect.
var (
	ErrInvalidToken = errors.New("invalid token")
	ErrInvalidType  = errors.New("invalid token type")
	ErrMissingSub   = errors.New("missing sub claim")
)

type ctxKey struct{}

// WithUserID stores the authenticated user id on the context.
func WithUserID(ctx context.Context, uid string) context.Context {
	return context.WithValue(ctx, ctxKey{}, uid)
}

// UserIDFrom returns the authenticated user id and whether it was present.
func UserIDFrom(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(ctxKey{}).(string)
	return v, ok && v != ""
}

// Claims is the minimal subset Phase 2 needs.
type Claims struct {
	Type     string `json:"type"`
	Username string `json:"username,omitempty"`
	jwt.RegisteredClaims
}

// Verifier validates Bearer JWTs against a single shared secret + expected type.
type Verifier struct {
	secret       []byte
	expectedType string
}

// NewMCPVerifier returns a Verifier scoped to type=mcp_access tokens.
func NewMCPVerifier(secret string) *Verifier {
	return &Verifier{secret: []byte(secret), expectedType: "mcp_access"}
}

// NewVerifier lets callers pick the expected token type (e.g. session tokens).
func NewVerifier(secret, expectedType string) *Verifier {
	return &Verifier{secret: []byte(secret), expectedType: expectedType}
}

// NewSessionVerifier validates the cookie-session JWTs minted by the web
// auth flow. Python's web/auth.py emits these without a `type` claim, so
// the verifier only checks signature, validity, and `sub`.
func NewSessionVerifier(secret string) *Verifier {
	return &Verifier{secret: []byte(secret), expectedType: ""}
}

// Verify parses and validates a token, returning the subject (user id).
func (v *Verifier) Verify(token string) (string, error) {
	c, err := v.VerifyClaims(token)
	if err != nil {
		return "", err
	}
	return c.Subject, nil
}

// VerifyClaims is like Verify but returns the full validated claims so
// callers (e.g. the cookie session flow) can read username alongside sub.
func (v *Verifier) VerifyClaims(token string) (*Claims, error) {
	var c Claims
	parsed, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method %s", t.Method.Alg())
		}
		return v.secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if !parsed.Valid {
		return nil, ErrInvalidToken
	}
	// expectedType "" means "any/no type required" — used for cookie session
	// tokens that don't carry a `type` claim.
	if v.expectedType != "" && c.Type != v.expectedType {
		return nil, fmt.Errorf("%w: got %q want %q", ErrInvalidType, c.Type, v.expectedType)
	}
	if c.Subject == "" {
		return nil, ErrMissingSub
	}
	return &c, nil
}
