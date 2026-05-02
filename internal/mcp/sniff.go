package mcp

import (
	"bytes"
	"net/http"
	"regexp"
)

// sessionIDPattern matches the SDK's first SSE event body, which includes
// "?sessionid=XXX". The MCP Go SDK uses crypto/rand.Text() (Go 1.24+),
// producing a base32 RFC-4648 token (uppercase A-Z + digits 2-7); 26
// chars at the time of writing. We accept ≥20 chars to absorb future
// length tweaks while still being strict enough that arbitrary tokens
// in unrelated body bytes don't false-match.
var sessionIDPattern = regexp.MustCompile(`sessionid=([A-Z2-7]{20,})`)

// sniffMaxBytes is the stop-trying threshold for capture.
const sniffMaxBytes = 4096

// sniffWriter wraps http.ResponseWriter to peek at the SSE response body
// for the endpoint event's session id. Once captured (or once the buffer
// exceeds sniffMaxBytes), no further inspection occurs and writes are
// passed through verbatim.
//
// This is the Go analogue of Python's ASGI _send interception in
// web/mcp_router.py — required because the SDK's SSEHandler does NOT
// expose session_id externally.
//
// FAIL-CLOSED CONTRACT: if onCapture is never invoked (pattern not
// matched within sniffMaxBytes, or SDK changes its event format), the
// sessionStore receives no entry for this session, so every POST to it
// will return 404 from handleMessages. That is a denial of service for
// the affected session, but never an authorization bypass — which is
// the security-relevant property. Compatibility regressions show up
// immediately in TestSSE_ConcurrentConnects.
type sniffWriter struct {
	http.ResponseWriter
	buf       bytes.Buffer
	stopped   bool
	captured  string
	onCapture func(sessionID string)
}

func newSniffWriter(w http.ResponseWriter, onCapture func(string)) *sniffWriter {
	return &sniffWriter{ResponseWriter: w, onCapture: onCapture}
}

func (sw *sniffWriter) Write(b []byte) (int, error) {
	if !sw.stopped {
		sw.buf.Write(b)
		if m := sessionIDPattern.FindSubmatch(sw.buf.Bytes()); m != nil {
			sw.stopped = true
			sw.captured = string(m[1])
			if sw.onCapture != nil {
				sw.onCapture(sw.captured)
			}
			sw.buf.Reset()
		} else if sw.buf.Len() > sniffMaxBytes {
			sw.stopped = true
			sw.buf.Reset()
		}
	}
	return sw.ResponseWriter.Write(b)
}

// Flush forwards to the underlying writer's Flusher if available.
// Required so SSE clients receive the endpoint event without buffering.
func (sw *sniffWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// SessionID returns the captured session id, or "" if not yet captured.
func (sw *sniffWriter) SessionID() string { return sw.captured }
