package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

func TestRateLimit_PerIPBurst(t *testing.T) {
	rl := NewIPRateLimiter(rate.Limit(0), 2) // burst 2, no refill (rate=0)
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	hit := func(ip string) int {
		req := httptest.NewRequest("GET", "/api/channels", nil)
		req.RemoteAddr = ip + ":1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}

	// Burst of 2 from IP1 succeeds; 3rd is throttled.
	if got := hit("10.0.0.1"); got != 200 {
		t.Errorf("hit 1 = %d", got)
	}
	if got := hit("10.0.0.1"); got != 200 {
		t.Errorf("hit 2 = %d", got)
	}
	if got := hit("10.0.0.1"); got != 429 {
		t.Errorf("hit 3 (over burst) = %d, want 429", got)
	}

	// Different IP gets its own bucket.
	if got := hit("10.0.0.2"); got != 200 {
		t.Errorf("hit (other ip) = %d", got)
	}
}

func TestClientIP_ForwardedHeader(t *testing.T) {
	tests := []struct {
		name string
		req  *http.Request
		want string
	}{
		{
			"plain RemoteAddr",
			func() *http.Request {
				r := httptest.NewRequest("GET", "/", nil)
				r.RemoteAddr = "1.2.3.4:5678"
				return r
			}(),
			"1.2.3.4",
		},
		{
			"X-Forwarded-For single",
			func() *http.Request {
				r := httptest.NewRequest("GET", "/", nil)
				r.Header.Set("X-Forwarded-For", "9.9.9.9")
				return r
			}(),
			"9.9.9.9",
		},
		{
			"X-Forwarded-For chain (leftmost wins)",
			func() *http.Request {
				r := httptest.NewRequest("GET", "/", nil)
				r.Header.Set("X-Forwarded-For", "9.9.9.9, 10.0.0.1, 10.0.0.2")
				return r
			}(),
			"9.9.9.9",
		},
		{
			"X-Real-IP fallback",
			func() *http.Request {
				r := httptest.NewRequest("GET", "/", nil)
				r.Header.Set("X-Real-IP", "8.8.8.8")
				return r
			}(),
			"8.8.8.8",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clientIP(tt.req); got != tt.want {
				t.Errorf("got=%q want=%q", got, tt.want)
			}
		})
	}
}
