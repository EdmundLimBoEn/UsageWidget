package server

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Token           string
	UsageSource     string // openusage | codexbar | auto
	OpenUsageURL    string
	OpenUsageCmd    string
	OpenUsageBin    string
	OpenUsageSocket string
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

	source := strings.ToLower(strings.TrimSpace(envOr("USAGE_SOURCE", "auto")))
	switch source {
	case "auto", "openusage", "codexbar":
	default:
		return Config{}, fmt.Errorf("USAGE_SOURCE must be auto, openusage, or codexbar")
	}

	return Config{
		Token:           token,
		UsageSource:     source,
		OpenUsageURL:    os.Getenv("OPENUSAGE_URL"),
		OpenUsageCmd:    os.Getenv("OPENUSAGE_CMD"),
		OpenUsageBin:    os.Getenv("OPENUSAGE_BIN"),
		OpenUsageSocket: os.Getenv("OPENUSAGE_SOCKET"),
		CodexBarURL:     os.Getenv("CODEXBAR_URL"),
		CodexBarCmd:     os.Getenv("CODEXBAR_CMD"),
		CodexBarBin:     os.Getenv("CODEXBAR_BIN"),
		CollectorSocket: envOr("COLLECTOR_SOCKET", "/run/usagewidget/collector.sock"),
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

// NewUsageSourceFromConfig picks OpenUsage first (quota gauges + Cursor), then CodexBar.
func NewUsageSourceFromConfig(cfg Config) UsageSource {
	wantOpen := cfg.UsageSource == "openusage" || cfg.UsageSource == "auto"
	wantCodex := cfg.UsageSource == "codexbar" || cfg.UsageSource == "auto"

	if wantOpen {
		if cfg.OpenUsageCmd != "" {
			return NewOpenUsageCommandClient(cfg.OpenUsageCmd)
		}
		if cfg.OpenUsageURL != "" {
			return NewOpenUsageHTTPClient(cfg.OpenUsageURL)
		}
		if cfg.OpenUsageBin != "" {
			return NewOpenUsageBinaryClient(cfg.OpenUsageBin)
		}
		if cfg.OpenUsageSocket != "" {
			return NewOpenUsageUnixClient(cfg.OpenUsageSocket)
		}
		if cfg.UsageSource == "openusage" || (cfg.UsageSource == "auto" && cfg.CodexBarURL == "" && cfg.CodexBarCmd == "" && cfg.CodexBarBin == "") {
			// Default production path: collector sidecar speaking OpenUsage export JSON.
			return NewOpenUsageUnixClient(cfg.CollectorSocket)
		}
	}

	if wantCodex {
		if cfg.CodexBarCmd != "" {
			return NewCodexBarCommandClient(cfg.CodexBarCmd)
		}
		if cfg.CodexBarURL != "" {
			return NewCodexBarClient(cfg.CodexBarURL)
		}
		if cfg.CodexBarBin != "" {
			return NewCodexBarBinaryClient(cfg.CodexBarBin)
		}
		return NewCodexBarUnixClient(cfg.CollectorSocket)
	}

	return NewOpenUsageUnixClient(cfg.CollectorSocket)
}
