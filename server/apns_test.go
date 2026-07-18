package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type apnsRoundTripFunc func(*http.Request) (*http.Response, error)

func (f apnsRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func testKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func TestProviderTokenShape(t *testing.T) {
	key := testKey(t)
	c := &apnsClient{key: key, keyID: "ABC123KEYID", teamID: "TEAM1234ID"}

	tok, err := c.providerToken()
	if err != nil {
		t.Fatalf("providerToken: %v", err)
	}

	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT segments, got %d", len(parts))
	}

	var header map[string]any
	decodeSegment(t, parts[0], &header)
	if header["alg"] != "ES256" {
		t.Fatalf("alg=%v, want ES256", header["alg"])
	}
	if header["kid"] != "ABC123KEYID" {
		t.Fatalf("kid=%v, want ABC123KEYID", header["kid"])
	}

	var claims map[string]any
	decodeSegment(t, parts[1], &claims)
	if claims["iss"] != "TEAM1234ID" {
		t.Fatalf("iss=%v, want TEAM1234ID", claims["iss"])
	}
	if _, ok := claims["iat"]; !ok {
		t.Fatalf("expected iat claim")
	}

	// Signature must verify against the public key over the signing input.
	signingInput := parts[0] + "." + parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	if len(sig) != 64 {
		t.Fatalf("expected 64-byte ES256 signature, got %d", len(sig))
	}
	digest := sha256.Sum256([]byte(signingInput))
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(&key.PublicKey, digest[:], r, s) {
		t.Fatalf("signature does not verify")
	}
}

func TestProviderTokenCached(t *testing.T) {
	c := &apnsClient{key: testKey(t), keyID: "K", teamID: "T"}
	first, err := c.providerToken()
	if err != nil {
		t.Fatalf("providerToken: %v", err)
	}
	second, err := c.providerToken()
	if err != nil {
		t.Fatalf("providerToken: %v", err)
	}
	if first != second {
		t.Fatalf("expected cached token to be reused")
	}

	c.issuedAt = time.Now().Add(-41 * time.Minute)
	third, err := c.providerToken()
	if err != nil {
		t.Fatalf("providerToken: %v", err)
	}
	if third == second {
		t.Fatalf("expected token to refresh after expiry")
	}
}

func TestInvalidProviderTokenRefreshesAndRetriesOnce(t *testing.T) {
	requests := 0
	var authorizations []string
	client := &apnsClient{
		key: testKey(t), keyID: "K", teamID: "T", bundleID: "bundle", host: "apns.test",
		http: &http.Client{Transport: apnsRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests++
			authorizations = append(authorizations, req.Header.Get("authorization"))
			if requests == 1 {
				return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader(`{"reason":"InvalidProviderToken"}`)), Header: make(http.Header)}, nil
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		})},
	}
	if err := client.SendWidgetRefresh(context.Background(), "widget-token"); err != nil {
		t.Fatalf("expected refreshed retry to succeed: %v", err)
	}
	if requests != 2 || authorizations[0] == authorizations[1] {
		t.Fatalf("expected two requests with distinct provider tokens: %v", authorizations)
	}
}

func TestAPNSErrorTerminalDeviceTokenClassification(t *testing.T) {
	if !(&APNSError{Reason: "Unregistered"}).TerminalDeviceToken() {
		t.Fatal("Unregistered must clear a device token")
	}
	if (&APNSError{Reason: "TooManyRequests"}).TerminalDeviceToken() {
		t.Fatal("transient APNs failures must retain device tokens")
	}
}

func TestNoopNotifierMode(t *testing.T) {
	n, err := NewNotifier(Config{})
	if err != nil {
		t.Fatalf("NewNotifier: %v", err)
	}
	if _, ok := n.(noopNotifier); !ok {
		t.Fatalf("expected noopNotifier when APNs env unset, got %T", n)
	}
	if status, ok := n.(notifierStatus); !ok || status.Enabled() {
		t.Fatalf("noop notifier must report disabled, got %T", n)
	}
	if err := n.SendAlert(context.Background(), "token", Event{Type: "reset", WindowID: "codex.primary"}); err != nil {
		t.Fatalf("noop SendAlert: %v", err)
	}
	if err := n.SendWidgetRefresh(context.Background(), "token"); err != nil {
		t.Fatalf("noop SendWidgetRefresh: %v", err)
	}
}

func TestNewNotifierLoadsP8(t *testing.T) {
	key := testKey(t)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	path := filepath.Join(t.TempDir(), "AuthKey.p8")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("write p8: %v", err)
	}

	cfg := Config{
		APNsKeyPath:  path,
		APNsKeyID:    "KEYID",
		APNsTeamID:   "TEAMID",
		APNsBundleID: "systems.edmundlim.UsageWidget",
		APNsEnv:      "production",
	}
	n, err := NewNotifier(cfg)
	if err != nil {
		t.Fatalf("NewNotifier: %v", err)
	}
	client, ok := n.(*apnsClient)
	if !ok {
		t.Fatalf("expected apnsClient, got %T", n)
	}
	if client.host != "api.push.apple.com" {
		t.Fatalf("host=%s, want production host", client.host)
	}
	if !client.Enabled() {
		t.Fatal("real APNs client must report enabled")
	}
	if _, err := client.providerToken(); err != nil {
		t.Fatalf("providerToken from loaded key: %v", err)
	}
}

func decodeSegment(t *testing.T, seg string, v any) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		t.Fatalf("decode segment: %v", err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("unmarshal segment: %v", err)
	}
}
