package server

import (
	"fmt"
	"net"
	"os"
	"strings"
)

type Config struct {
	Token           string
	CodexBarURL     string
	CodexBarCmd     string
	CollectorSocket string
	DBPath          string
	ListenAddr      string

	DemoEnabled          bool
	DemoListenAddr       string
	DemoDeviceIDs        []string
	AccessIdentityHeader string

	APNsKeyPath  string
	APNsKeyID    string
	APNsTeamID   string
	APNsBundleID string
	APNsEnv      string
}

func (c Config) APNsEnabled() bool {
	return c.APNsKeyPath != "" && c.APNsKeyID != "" && c.APNsTeamID != "" && c.APNsBundleID != ""
}

func LoadConfig() (Config, error) {
	token := os.Getenv("USAGEWIDGET_TOKEN")
	if len(token) < 32 {
		return Config{}, fmt.Errorf("USAGEWIDGET_TOKEN must be at least 32 characters")
	}
	if token != strings.TrimSpace(token) {
		return Config{}, fmt.Errorf("USAGEWIDGET_TOKEN must not have surrounding whitespace")
	}

	deviceIDs, err := parseDemoDeviceIDs(os.Getenv("DEMO_DEVICE_IDS"))
	if err != nil {
		return Config{}, err
	}
	identityHeader, configured := os.LookupEnv("ACCESS_IDENTITY_HEADER")
	if !configured {
		identityHeader = "Cf-Access-Authenticated-User-Email"
	}
	if strings.TrimSpace(identityHeader) == "" {
		return Config{}, fmt.Errorf("ACCESS_IDENTITY_HEADER must not be blank")
	}

	demoEnabled := os.Getenv("USAGEWIDGET_DEMO_ENABLED") == "true"
	demoListenAddr := envOr("DEMO_LISTEN_ADDR", "127.0.0.1:8378")
	if demoEnabled {
		host, _, err := net.SplitHostPort(demoListenAddr)
		if err != nil {
			return Config{}, fmt.Errorf("DEMO_LISTEN_ADDR: %w", err)
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return Config{}, fmt.Errorf("DEMO_LISTEN_ADDR must use a loopback IP")
		}
	}

	return Config{
		Token:                token,
		CodexBarURL:          os.Getenv("CODEXBAR_URL"),
		CodexBarCmd:          os.Getenv("CODEXBAR_CMD"),
		CollectorSocket:      envOr("COLLECTOR_SOCKET", "/run/usagewidget/codexbar.sock"),
		DBPath:               envOr("DB_PATH", "./usagewidget.db"),
		ListenAddr:           envOr("LISTEN_ADDR", "127.0.0.1:8377"),
		DemoEnabled:          demoEnabled,
		DemoListenAddr:       demoListenAddr,
		DemoDeviceIDs:        deviceIDs,
		AccessIdentityHeader: strings.TrimSpace(identityHeader),

		APNsKeyPath:  os.Getenv("APNS_KEY_PATH"),
		APNsKeyID:    os.Getenv("APNS_KEY_ID"),
		APNsTeamID:   os.Getenv("APNS_TEAM_ID"),
		APNsBundleID: os.Getenv("APNS_BUNDLE_ID"),
		APNsEnv:      envOr("APNS_ENV", "sandbox"),
	}, nil
}

func parseDemoDeviceIDs(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	seen := make(map[string]bool)
	var ids []string
	for _, item := range strings.Split(raw, ",") {
		id := strings.TrimSpace(item)
		if id == "" {
			return nil, fmt.Errorf("DEMO_DEVICE_IDS contains an empty device ID")
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
