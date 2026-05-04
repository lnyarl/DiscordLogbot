package web

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// IPRateLimiter is the lightweight equivalent of slowapi: per-IP token
// bucket with the configured rate and burst. Limits are coarse — slowapi's
// "30/minute" maps to rate=0.5/s, burst=30, which lets short bursts pass
// then throttles to one every two seconds (same UX as the FastAPI side).
//
// The map grows for the lifetime of the process; for a self-hosted bot
// with a small known user set this is fine. A janitor goroutine evicts
// idle entries every cleanupEvery to keep memory bounded if a public
// instance ever picks this up.
type IPRateLimiter struct {
	rate    rate.Limit
	burst   int
	mu      sync.Mutex
	buckets map[string]*ipBucket
}

type ipBucket struct {
	limiter *rate.Limiter
	lastSeen time.Time
}

// NewIPRateLimiter builds a limiter; rps is the steady rate (e.g. 0.5 for
// 30/min) and burst is the bucket capacity.
func NewIPRateLimiter(rps rate.Limit, burst int) *IPRateLimiter {
	l := &IPRateLimiter{
		rate:    rps,
		burst:   burst,
		buckets: make(map[string]*ipBucket),
	}
	go l.janitor()
	return l
}

func (l *IPRateLimiter) get(ip string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[ip]
	if !ok {
		b = &ipBucket{limiter: rate.NewLimiter(l.rate, l.burst)}
		l.buckets[ip] = b
	}
	b.lastSeen = time.Now()
	return b.limiter
}

const idleEvict = 10 * time.Minute
const cleanupEvery = 5 * time.Minute

func (l *IPRateLimiter) janitor() {
	t := time.NewTicker(cleanupEvery)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		l.mu.Lock()
		for ip, b := range l.buckets {
			if now.Sub(b.lastSeen) > idleEvict {
				delete(l.buckets, ip)
			}
		}
		l.mu.Unlock()
	}
}

// Middleware wraps next with IP-keyed rate limiting. 429 + JSON on reject —
// matches Python's `RateLimitExceeded → JSONResponse({"error":"Too many
// requests"}, status_code=429)` exception handler.
func (l *IPRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !l.get(ip).Allow() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"Too many requests"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP extracts the request's IP, honoring X-Forwarded-For if present
// (slowapi's get_remote_address does the same). Strips the port so the
// limiter key is the raw IP.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the leftmost entry — the original client IP per convention.
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return trimSpace(xff[:i])
			}
		}
		return trimSpace(xff)
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return trimSpace(xrip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
