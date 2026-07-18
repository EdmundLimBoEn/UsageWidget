package server

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestCSRFTokenRoundTrip(t *testing.T) {
	key := []byte("test-csrf-key-0123456789abcdef")
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	if err := verifyCSRFToken(key, issueCSRFToken(key, now), now.Add(14*time.Minute)); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
}
func TestCSRFTokenExpired(t *testing.T) {
	key := []byte("test-csrf-key-0123456789abcdef")
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	if err := verifyCSRFToken(key, issueCSRFToken(key, now), now.Add(16*time.Minute)); err == nil {
		t.Fatal("expected expired token")
	}
}
func TestCSRFTokenTampered(t *testing.T) {
	key := []byte("test-csrf-key-0123456789abcdef")
	now := time.Now()
	if err := verifyCSRFToken(key, issueCSRFToken(key, now)+"x", now); err == nil {
		t.Fatal("expected tampered token")
	}
}
func TestSameOriginOK(t *testing.T) {
	for _, tc := range []struct {
		origin, host, site string
		want               bool
	}{
		{"https://demo.test", "demo.test", "same-origin", true}, {"https://evil.test", "demo.test", "same-origin", false}, {"", "demo.test", "same-origin", true}, {"", "demo.test", "cross-site", false}, {"https://demo.test", "demo.test", "cross-site", false},
	} {
		r := httptest.NewRequest("POST", "http://"+tc.host+"/v1/demo", nil)
		r.Host = tc.host
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		if got := sameOriginOK(r); got != tc.want {
			t.Errorf("%+v: got %v", tc, got)
		}
	}
}
func TestRateLimiterPerIdentityAndGlobal(t *testing.T) {
	now := time.Now()
	l := &rateLimiter{window: time.Minute, perID: 30, global: 120}
	for i := 0; i < 30; i++ {
		if ok, _ := l.allow("a", now); !ok {
			t.Fatal("unexpected per-id denial")
		}
	}
	if ok, _ := l.allow("a", now); ok {
		t.Fatal("31st request allowed")
	}
	l = &rateLimiter{window: time.Minute, perID: 30, global: 2}
	if ok, _ := l.allow("a", now); !ok {
		t.Fatal()
	}
	if ok, _ := l.allow("b", now); !ok {
		t.Fatal()
	}
	if ok, _ := l.allow("c", now); ok {
		t.Fatal("global cap allowed")
	}
}
func TestIdempotencyStoreReplayAndEvict(t *testing.T) {
	now := time.Now()
	s := newIdempotencyStore()
	key := idempotencyKey{"id", "patch", "key"}
	_, owner, _ := s.reserve(key, now)
	if !owner {
		t.Fatal("first reservation not owner")
	}
	s.complete(key, 200, []byte(`{"ok":true}`), now)
	e, owner, _ := s.reserve(key, now.Add(time.Minute))
	if owner || e.status != 200 {
		t.Fatal("completed entry did not replay")
	}
	_, owner, _ = s.reserve(key, now.Add(16*time.Minute))
	if !owner {
		t.Fatal("expired entry did not evict")
	}
}
