package mcp

import "sync"

// sessionStore maps SSE session_id → owner user_id.
//
// Mirrors Python's _session_owners dict in web/mcp_router.py. The Go
// equivalent MUST be guarded by a mutex because (unlike asyncio's
// cooperatively-scheduled coroutines) goroutines run concurrently on
// multiple OS threads, so an unsynchronized map would race on the
// first client connect.
type sessionStore struct {
	mu     sync.RWMutex
	owners map[string]string
}

func newSessionStore() *sessionStore {
	return &sessionStore{owners: make(map[string]string)}
}

func (s *sessionStore) Set(sessionID, userID string) {
	s.mu.Lock()
	s.owners[sessionID] = userID
	s.mu.Unlock()
}

// Owner returns the user id that opened the session, or ("", false).
func (s *sessionStore) Owner(sessionID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	uid, ok := s.owners[sessionID]
	return uid, ok
}

func (s *sessionStore) Delete(sessionID string) {
	s.mu.Lock()
	delete(s.owners, sessionID)
	s.mu.Unlock()
}

// Len is exposed for tests.
func (s *sessionStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.owners)
}
