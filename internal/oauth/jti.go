package oauth

import (
	"sync"
	"time"
)

// jtiStore holds JWT IDs that have already been redeemed at /oauth/token.
// Auth codes are JWTs (so they're stateless to mint), but RFC 6749 §10.5
// requires single-use semantics — we enforce that here. The map is
// purged of expired entries on every Consume so size stays bounded.
//
// In a multi-process deployment this would need to move to Redis or
// similar; the migration plan calls that out as a Phase-9 follow-up.
type jtiStore struct {
	mu  sync.Mutex
	exp map[string]time.Time
}

func newJTIStore() *jtiStore {
	return &jtiStore{exp: make(map[string]time.Time)}
}

// Consume returns false if jti was already used; otherwise marks it used
// for ttl into the future.
func (s *jtiStore) Consume(jti string, ttl time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, t := range s.exp {
		if now.After(t) {
			delete(s.exp, k)
		}
	}
	if _, used := s.exp[jti]; used {
		return false
	}
	s.exp[jti] = now.Add(ttl)
	return true
}
