package oauth

import (
	"regexp"
	"strconv"
)

// localhostRE matches http(s)://localhost or 127.0.0.1 with an optional
// port (1-65535) and path. Anchors are essential — without ^/$ a string
// like https://evil.com/localhost would falsely match.
var localhostRE = regexp.MustCompile(`^https?://(localhost|127\.0\.0\.1)(?::(\d{1,5}))?(/.*)?$`)

// clientIDAllowed reports whether id is in the static allowlist. An empty
// allowlist rejects every client (the operator must opt in by configuring
// MCP_CLIENT_IDS).
func (s *Server) clientIDAllowed(id string) bool {
	if len(s.AllowedClientIDs) == 0 {
		return false
	}
	_, ok := s.AllowedClientIDs[id]
	return ok
}

// redirectURIAllowed accepts the URI verbatim from the static allowlist
// or any localhost / 127.0.0.1 variant — the latter so desktop AI clients
// (Claude Desktop, Cursor, etc.) can use ephemeral ports without admin
// config. Out-of-range ports are still rejected.
func (s *Server) redirectURIAllowed(uri string) bool {
	if _, ok := s.StaticRedirectURIs[uri]; ok {
		return true
	}
	m := localhostRE.FindStringSubmatch(uri)
	if m == nil {
		return false
	}
	if m[2] != "" {
		port, err := strconv.Atoi(m[2])
		if err != nil || port < 1 || port > 65535 {
			return false
		}
	}
	return true
}
