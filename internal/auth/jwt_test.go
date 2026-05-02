package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-secret-do-not-use-in-prod"

func makeToken(t *testing.T, secret, typ, sub string, exp time.Duration) string {
	t.Helper()
	claims := Claims{
		Type: typ,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   sub,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(exp)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}

func TestVerify_HappyPath(t *testing.T) {
	v := NewMCPVerifier(testSecret)
	tok := makeToken(t, testSecret, "mcp_access", "user-1", time.Hour)

	got, err := v.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got != "user-1" {
		t.Fatalf("subject: got=%q want=user-1", got)
	}
}

func TestVerify_WrongSecret(t *testing.T) {
	v := NewMCPVerifier(testSecret)
	tok := makeToken(t, "different-secret", "mcp_access", "user-1", time.Hour)

	if _, err := v.Verify(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
}

func TestVerify_WrongType(t *testing.T) {
	v := NewMCPVerifier(testSecret)
	tok := makeToken(t, testSecret, "session", "user-1", time.Hour)

	_, err := v.Verify(tok)
	if !errors.Is(err, ErrInvalidType) {
		t.Fatalf("want ErrInvalidType, got %v", err)
	}
}

func TestVerify_Expired(t *testing.T) {
	v := NewMCPVerifier(testSecret)
	tok := makeToken(t, testSecret, "mcp_access", "user-1", -time.Minute)

	if _, err := v.Verify(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected expired token to fail, got %v", err)
	}
}

func TestVerify_MissingSub(t *testing.T) {
	v := NewMCPVerifier(testSecret)
	tok := makeToken(t, testSecret, "mcp_access", "", time.Hour)

	if _, err := v.Verify(tok); !errors.Is(err, ErrMissingSub) {
		t.Fatalf("want ErrMissingSub, got %v", err)
	}
}

func TestVerify_RejectsNoneAlg(t *testing.T) {
	v := NewMCPVerifier(testSecret)
	// Forge a token with alg=none — signed string is empty.
	claims := Claims{Type: "mcp_access", RegisteredClaims: jwt.RegisteredClaims{
		Subject:   "user-1",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}}
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	signed, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if _, err := v.Verify(signed); err == nil {
		t.Fatal("want failure for alg=none, got nil")
	}
}
