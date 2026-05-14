package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter_AllowsUpToLimit(t *testing.T) {
	rl := newRateLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !rl.allow("1.2.3.4") {
			t.Errorf("attempt %d: expected allow", i+1)
		}
	}
	if rl.allow("1.2.3.4") {
		t.Errorf("4th attempt should be denied")
	}
}

func TestRateLimiter_PerIp(t *testing.T) {
	rl := newRateLimiter(1, time.Minute)
	if !rl.allow("1.1.1.1") {
		t.Errorf("first IP should be allowed")
	}
	if !rl.allow("2.2.2.2") {
		t.Errorf("second IP should be independently allowed")
	}
	if rl.allow("1.1.1.1") {
		t.Errorf("first IP should be denied on second hit")
	}
}

func TestRateLimiter_WindowReset(t *testing.T) {
	rl := newRateLimiter(1, 10*time.Millisecond)
	if !rl.allow("x") {
		t.Errorf("first should allow")
	}
	if rl.allow("x") {
		t.Errorf("second should deny")
	}
	time.Sleep(15 * time.Millisecond)
	if !rl.allow("x") {
		t.Errorf("after window expiry should allow again")
	}
}

func TestClientIP_RemoteAddr(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.5:54321"
	got := clientIP(r)
	if got != "10.0.0.5" {
		t.Errorf("clientIP(RemoteAddr) = %q, want 10.0.0.5", got)
	}
}

func TestClientIP_XForwardedFor(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")
	got := clientIP(r)
	if got != "203.0.113.5" {
		t.Errorf("clientIP(XFF) = %q, want 203.0.113.5", got)
	}
}

func TestLoginRateLimit_KicksIn(t *testing.T) {
	srv, _ := newTestServer(t)
	// The test server is configured with the default RateLimitConfig
	// loaded by Validate, which is 5/15min. Five POSTs from the same
	// IP should be allowed; the sixth gets the rate-limit page.
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/login", nil)
		r.RemoteAddr = "9.9.9.9:1234"
		srv.Router().ServeHTTP(w, r)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/login", nil)
	r.RemoteAddr = "9.9.9.9:1234"
	srv.Router().ServeHTTP(w, r)
	if !contains(w.Body.String(), "Too many attempts") {
		t.Errorf("expected rate-limit message; got %s", w.Body.String())
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) > 0 && len(needle) > 0 &&
		indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
