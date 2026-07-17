package server

import (
	"fmt"
	"os"
)

type Config struct {
	Token       string
	CodexBarURL string
	DBPath      string
	ListenAddr  string

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

	return Config{
		Token:       token,
		CodexBarURL: envOr("CODEXBAR_URL", "http://127.0.0.1:8765/usage"),
		DBPath:      envOr("DB_PATH", "./usagewidget.db"),
		ListenAddr:  envOr("LISTEN_ADDR", ":8377"),

		APNsKeyPath:  os.Getenv("APNS_KEY_PATH"),
		APNsKeyID:    os.Getenv("APNS_KEY_ID"),
		APNsTeamID:   os.Getenv("APNS_TEAM_ID"),
		APNsBundleID: os.Getenv("APNS_BUNDLE_ID"),
		APNsEnv:      envOr("APNS_ENV", "sandbox"),
	}, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
