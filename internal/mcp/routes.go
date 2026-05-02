package mcp

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lnyarl/discordlogbot/internal/auth"
)

// TokenVerifier is the minimal contract the routes need from auth.Verifier.
type TokenVerifier interface {
	Verify(token string) (userID string, err error)
}

// Handler exposes /mcp/sse and /mcp/messages with two security layers
// the SDK does not provide on its own:
//
//  1. Bearer JWT verification on EVERY request (both SSE and messages).
//  2. Session ownership: a POST /mcp/messages must come from the same
//     user_id whose JWT opened the SSE session, otherwise the SDK would
//     happily inject foreign messages into anyone's stream.
type Handler struct {
	verifier TokenVerifier
	server   *Server
	sessions *sessionStore
	sse      *mcpsdk.SSEHandler
}

func NewHandler(verifier TokenVerifier, srv *Server) *Handler {
	h := &Handler{
		verifier: verifier,
		server:   srv,
		sessions: newSessionStore(),
	}
	// getServer is called by the SDK on every request; we always serve
	// from the same configured server. user_id is carried via context.
	h.sse = mcpsdk.NewSSEHandler(
		func(_ *http.Request) *mcpsdk.Server { return srv.SDK() },
		&mcpsdk.SSEOptions{DisableLocalhostProtection: true},
	)
	return h
}

// Routes registers the MCP endpoints on a Go 1.22+ ServeMux.
//
// The Go SDK emits its endpoint event as a relative URL (`?sessionid=...`),
// so client POSTs land on the same path as the SSE GET — not on a
// separate /messages route as in Python's SseServerTransport. We keep
// the path under /mcp/sse for both methods and dispatch by method.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /mcp/sse", h.handleSSE)
	mux.HandleFunc("POST /mcp/sse", h.handleMessages)
}

var errInvalidAuthHeader = errors.New("invalid authorization header")

func (h *Handler) authenticate(r *http.Request) (string, error) {
	hdr := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(hdr, prefix) {
		return "", errInvalidAuthHeader
	}
	token := strings.TrimPrefix(hdr, prefix)
	return h.verifier.Verify(token)
}

func (h *Handler) handleSSE(w http.ResponseWriter, r *http.Request) {
	userID, err := h.authenticate(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var captured string
	sw := newSniffWriter(w, func(sid string) {
		captured = sid
		h.sessions.Set(sid, userID)
	})
	defer func() {
		if captured != "" {
			h.sessions.Delete(captured)
		}
	}()

	ctx := auth.WithUserID(r.Context(), userID)
	h.sse.ServeHTTP(sw, r.WithContext(ctx))
}

func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	userID, err := h.authenticate(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	sid := r.URL.Query().Get("sessionid")
	if sid == "" {
		http.Error(w, "missing sessionid", http.StatusBadRequest)
		return
	}
	owner, ok := h.sessions.Owner(sid)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if owner != userID {
		slog.Warn("mcp session owner mismatch",
			"session_id", sid, "expected", owner, "got", userID)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	h.sse.ServeHTTP(w, r)
}
