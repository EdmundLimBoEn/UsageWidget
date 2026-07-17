package server

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

type Notifier interface {
	SendAlert(ctx context.Context, deviceToken string, ev Event) error
	SendWidgetRefresh(ctx context.Context, widgetToken string) error
}

func NewNotifier(cfg Config) (Notifier, error) {
	if !cfg.APNsEnabled() {
		log.Printf("apns: disabled (env not set); pushes will be logged only")
		return noopNotifier{}, nil
	}
	key, err := loadECPrivateKey(cfg.APNsKeyPath)
	if err != nil {
		return nil, fmt.Errorf("apns: load key: %w", err)
	}
	host := "api.sandbox.push.apple.com"
	if cfg.APNsEnv == "production" {
		host = "api.push.apple.com"
	}
	return &apnsClient{
		key:      key,
		keyID:    cfg.APNsKeyID,
		teamID:   cfg.APNsTeamID,
		bundleID: cfg.APNsBundleID,
		host:     host,
		http:     &http.Client{Timeout: 15 * time.Second},
	}, nil
}

type noopNotifier struct{}

func (noopNotifier) SendAlert(_ context.Context, _ string, ev Event) error {
	log.Printf("apns(noop): alert type=%s window=%s used=%.1f%%", ev.Type, ev.WindowID, ev.UsedPercent)
	return nil
}

func (noopNotifier) SendWidgetRefresh(_ context.Context, _ string) error {
	log.Printf("apns(noop): widget refresh")
	return nil
}

type apnsClient struct {
	key      *ecdsa.PrivateKey
	keyID    string
	teamID   string
	bundleID string
	host     string
	http     *http.Client

	mu       sync.Mutex
	token    string
	issuedAt time.Time
}

func (c *apnsClient) SendAlert(ctx context.Context, deviceToken string, ev Event) error {
	body, err := json.Marshal(alertPayload(ev))
	if err != nil {
		return fmt.Errorf("apns: marshal alert: %w", err)
	}
	return c.push(ctx, deviceToken, "alert", c.bundleID, "10", body)
}

func (c *apnsClient) SendWidgetRefresh(ctx context.Context, widgetToken string) error {
	// ponytail: minimal widgets payload; tune content keys if WidgetKit needs more.
	body := []byte(`{"aps":{"content-changed":true}}`)
	return c.push(ctx, widgetToken, "widgets", c.bundleID+".push-type.widgets", "5", body)
}

func (c *apnsClient) push(ctx context.Context, deviceToken, pushType, topic, priority string, body []byte) error {
	tok, err := c.providerToken()
	if err != nil {
		return err
	}
	url := "https://" + c.host + "/3/device/" + deviceToken
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("apns: build request: %w", err)
	}
	req.Header.Set("authorization", "bearer "+tok)
	req.Header.Set("apns-topic", topic)
	req.Header.Set("apns-push-type", pushType)
	req.Header.Set("apns-priority", priority)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("apns: send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("apns: status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (c *apnsClient) providerToken() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Since(c.issuedAt) < 40*time.Minute {
		return c.token, nil
	}
	tok, err := buildProviderToken(c.key, c.keyID, c.teamID, time.Now())
	if err != nil {
		return "", err
	}
	c.token = tok
	c.issuedAt = time.Now()
	return tok, nil
}

func buildProviderToken(key *ecdsa.PrivateKey, keyID, teamID string, now time.Time) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": "ES256", "kid": keyID})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(map[string]any{"iss": teamID, "iat": now.Unix()})
	if err != nil {
		return "", err
	}
	signingInput := b64(header) + "." + b64(claims)

	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", fmt.Errorf("apns: sign token: %w", err)
	}
	size := (key.Curve.Params().BitSize + 7) / 8
	sig := make([]byte, 2*size)
	r.FillBytes(sig[:size])
	s.FillBytes(sig[size:])

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func b64(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func loadECPrivateKey(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in %s", path)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is %T, want ECDSA", parsed)
	}
	return key, nil
}

func alertPayload(ev Event) map[string]any {
	payload := map[string]any{
		"aps": map[string]any{
			"alert": map[string]any{"title": ev.Title, "body": alertBody(ev)},
			"sound": "default",
		},
		"provider":         ev.ProviderID,
		"providerName":     ev.ProviderName,
		"windowTitle":      ev.WindowTitle,
		"windowID":         ev.WindowID,
		"usedPercent":      ev.UsedPercent,
		"remainingPercent": ev.RemainingPercent,
		"eventType":        ev.Type,
	}
	if ev.ResetsAt != nil {
		payload["resetsAt"] = ev.ResetsAt.UTC().Format(time.RFC3339)
	}
	return payload
}

func alertBody(ev Event) string {
	if ev.WindowTitle == "" {
		return ev.ProviderName
	}
	return fmt.Sprintf("%s · %s — %.0f%% used, %.0f%% left", ev.ProviderName, ev.WindowTitle, ev.UsedPercent, ev.RemainingPercent)
}
