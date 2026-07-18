package server

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Token       string
	CodexBarURL string
	CodexBarCmd string
	DBPath      string
	ListenAddr  string

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
	if token == "" {
		return Config{}, fmt.Errorf("USAGEWIDGET_TOKEN is required")
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

	return Config{
		Token:                token,
		CodexBarURL:          envOr("CODEXBAR_URL", "http://127.0.0.1:8765/usage"),
		CodexBarCmd:          os.Getenv("CODEXBAR_CMD"),
		DBPath:               envOr("DB_PATH", "./usagewidget.db"),
		ListenAddr:           envOr("LISTEN_ADDR", ":8377"),
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
