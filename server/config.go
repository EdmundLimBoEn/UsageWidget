package server

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Token           string
	CodexBarURL     string
	CodexBarCmd     string
	CodexBarBin     string
	CollectorSocket string
	DBPath          string
	ListenAddr      string

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

	return Config{
		Token:           token,
		CodexBarURL:     os.Getenv("CODEXBAR_URL"),
		CodexBarCmd:     os.Getenv("CODEXBAR_CMD"),
		CodexBarBin:     os.Getenv("CODEXBAR_BIN"),
		CollectorSocket: envOr("COLLECTOR_SOCKET", "/run/usagewidget/codexbar.sock"),
		DBPath:          envOr("DB_PATH", "./usagewidget.db"),
		ListenAddr:      envOr("LISTEN_ADDR", "127.0.0.1:8377"),

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
