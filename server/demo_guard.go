package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const demoGuardTTL = 15 * time.Minute

func issueCSRFToken(key []byte, now time.Time) string {
	exp := now.Add(demoGuardTTL).Unix()
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	payload := strconv.FormatInt(exp, 10) + "." + hexNonce(nonce)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))))
}

func hexNonce(nonce []byte) string {
	const alphabet = "0123456789abcdef"
	encoded := make([]byte, len(nonce)*2)
	for i, value := range nonce {
		encoded[2*i] = alphabet[value>>4]
		encoded[2*i+1] = alphabet[value&0x0f]
	}
	return string(encoded)
}

func verifyCSRFToken(key []byte, token string, now time.Time) error {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return errors.New("invalid csrf token")
	}
	parts := strings.Split(string(decoded), ".")
	if len(parts) != 3 || parts[0] == "" || !validHexNonce(parts[1]) || parts[2] == "" {
		return errors.New("invalid csrf token")
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || now.Unix() > exp {
		return errors.New("expired csrf token")
	}
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return errors.New("invalid csrf token")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(got, mac.Sum(nil)) {
		return errors.New("invalid csrf token")
	}
	return nil
}

func validHexNonce(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, ch := range value {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

func sameOriginOK(r *http.Request) bool {
	fetchSite := r.Header.Get("Sec-Fetch-Site")
	if fetchSite != "same-origin" && fetchSite != "none" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return fetchSite == "same-origin"
	}
	host, ok := originHost(origin)
	return ok && strings.EqualFold(host, r.Host)
}

func originHost(origin string) (string, bool) {
	if !strings.HasPrefix(origin, "https://") && !strings.HasPrefix(origin, "http://") {
		return "", false
	}
	host := strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	if host == "" || strings.ContainsAny(host, "/?#@") {
		return "", false
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		if _, _, err := net.SplitHostPort(host); err != nil {
			return "", false
		}
	}
	return host, true
}

// ponytail: fixed-window in-process limits are safe only for this single demo
// process. Multi-replica deployments must use SQLite or a shared store.
type rateLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	perID   int
	global  int
	buckets map[string]int
	globalN int
	resetAt time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{window: time.Minute, perID: 30, global: 120, buckets: make(map[string]int)}
}

func (l *rateLimiter) allow(identity string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.window <= 0 {
		l.window = time.Minute
	}
	if l.perID <= 0 {
		l.perID = 30
	}
	if l.global <= 0 {
		l.global = 120
	}
	if l.buckets == nil {
		l.buckets = make(map[string]int)
	}
	if l.resetAt.IsZero() || !now.Before(l.resetAt) {
		l.resetAt, l.globalN, l.buckets = now.Add(l.window), 0, make(map[string]int)
	}
	retry := l.resetAt.Sub(now)
	if l.globalN >= l.global || l.buckets[identity] >= l.perID {
		return false, retry
	}
	l.globalN++
	l.buckets[identity]++
	return true, retry
}

type idempotencyKey struct{ Identity, Route, Key string }
type idempotencyEntry struct {
	status    int
	body      []byte
	createdAt time.Time
	done      chan struct{}
	complete  bool
}

// ponytail: this process-local cache is bounded. Restart or replica fan-out
// can permit one duplicate side effect; a durable store is the upgrade path.
type idempotencyStore struct {
	mu      sync.Mutex
	ttl     time.Duration
	cap     int
	entries map[idempotencyKey]idempotencyEntry
}

func newIdempotencyStore() *idempotencyStore {
	return &idempotencyStore{ttl: demoGuardTTL, cap: 500, entries: make(map[idempotencyKey]idempotencyEntry)}
}

func (s *idempotencyStore) reserve(key idempotencyKey, now time.Time) (idempotencyEntry, bool, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ttl <= 0 {
		s.ttl = demoGuardTTL
	}
	if s.cap <= 0 {
		s.cap = 500
	}
	if s.entries == nil {
		s.entries = make(map[idempotencyKey]idempotencyEntry)
	}
	for k, entry := range s.entries {
		if now.Sub(entry.createdAt) > s.ttl {
			delete(s.entries, k)
		}
	}
	if entry, ok := s.entries[key]; ok {
		return entry, false, entry.done
	}
	for len(s.entries) >= s.cap {
		var oldestKey idempotencyKey
		var oldest time.Time
		for k, entry := range s.entries {
			if oldest.IsZero() || entry.createdAt.Before(oldest) {
				oldestKey, oldest = k, entry.createdAt
			}
		}
		delete(s.entries, oldestKey)
	}
	entry := idempotencyEntry{createdAt: now, done: make(chan struct{})}
	s.entries[key] = entry
	return entry, true, entry.done
}

func (s *idempotencyStore) complete(key idempotencyKey, status int, body []byte, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok || entry.complete {
		return
	}
	entry.status, entry.body, entry.complete, entry.createdAt = status, append([]byte(nil), body...), true, now
	s.entries[key] = entry
	close(entry.done)
}
