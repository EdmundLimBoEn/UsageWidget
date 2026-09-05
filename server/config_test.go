package server

import (
	"testing"
)

const validTestToken = "0123456789abcdef0123456789abcdef"

func TestLoadConfigRequiresToken(t *testing.T) {
	t.Setenv("USAGEWIDGET_TOKEN", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatalf("expected error when USAGEWIDGET_TOKEN is unset")
	}
}

func TestLoadConfigRejectsWeakOrWhitespaceToken(t *testing.T) {
	for _, token := range []string{"short", validTestToken + "\n"} {
		t.Setenv("USAGEWIDGET_TOKEN", token)
		if _, err := LoadConfig(); err == nil {
			t.Fatalf("expected token %q to be rejected", token)
		}
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("USAGEWIDGET_TOKEN", validTestToken)
	t.Setenv("CROSSUSAGE_URL", "")
	t.Setenv("CROSSUSAGE_BIN", "")
	t.Setenv("CROSSUSAGE_CMD", "")
	t.Setenv("COLLECTOR_SOCKET", "")
	t.Setenv("DB_PATH", "")
	t.Setenv("LISTEN_ADDR", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.CrossUsageURL != "" {
		t.Fatalf("unexpected default CrossUsageURL: %s", cfg.CrossUsageURL)
	}
	if cfg.CollectorSocket != "/run/usagewidget/collector.sock" {
		t.Fatalf("unexpected collector socket: %s", cfg.CollectorSocket)
	}
	if cfg.DBPath != "./usagewidget.db" {
		t.Fatalf("unexpected default DBPath: %s", cfg.DBPath)
	}
	if cfg.ListenAddr != "127.0.0.1:8377" {
		t.Fatalf("unexpected default ListenAddr: %s", cfg.ListenAddr)
	}
	if cfg.APNsEnabled() {
		t.Fatalf("expected APNs disabled when APNs env vars are unset")
	}
}

func TestLoadConfigPreservesCrossUsageBinaryPath(t *testing.T) {
	t.Setenv("USAGEWIDGET_TOKEN", validTestToken)
	t.Setenv("CROSSUSAGE_BIN", `C:\\Program Files\\CrossUsage\\crossusage-cli.exe`)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.CrossUsageBin != `C:\\Program Files\\CrossUsage\\crossusage-cli.exe` {
		t.Fatalf("CrossUsageBin was changed: %q", cfg.CrossUsageBin)
	}
}

func TestLoadConfigAPNsEnabledWhenAllVarsPresent(t *testing.T) {
	t.Setenv("USAGEWIDGET_TOKEN", validTestToken)
	t.Setenv("APNS_KEY_PATH", "/tmp/key.p8")
	t.Setenv("APNS_KEY_ID", "KEYID")
	t.Setenv("APNS_TEAM_ID", "TEAMID")
	t.Setenv("APNS_BUNDLE_ID", "systems.edmundlim.UsageWidget")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.APNsEnabled() {
		t.Fatalf("expected APNs enabled when all vars present")
	}
}

func TestNewUsageSourceFromConfig(t *testing.T) {
	cfg := Config{CollectorSocket: "/tmp/collector.sock"}
	src := NewUsageSourceFromConfig(cfg)
	if src.SourceName() != "crossusage-collector" {
		t.Fatalf("expected crossusage-collector, got %s", src.SourceName())
	}
	cfg.CrossUsageURL = "http://127.0.0.1:6736/v1/limits"
	src = NewUsageSourceFromConfig(cfg)
	if src.SourceName() != "crossusage-http" {
		t.Fatalf("expected crossusage-http when CROSSUSAGE_URL set, got %s", src.SourceName())
	}
	cfg = Config{CrossUsageBin: "/usr/bin/crossusage-cli"}
	src = NewUsageSourceFromConfig(cfg)
	if src.SourceName() != "crossusage-cli" {
		t.Fatalf("expected crossusage-cli, got %s", src.SourceName())
	}
	cfg = Config{CrossUsageCmd: "crossusage-cli limits cursor"}
	src = NewUsageSourceFromConfig(cfg)
	if src.SourceName() != "crossusage-command" {
		t.Fatalf("expected crossusage-command, got %s", src.SourceName())
	}
}

func TestWhitespaceCrossUsageCmdIsIgnored(t *testing.T) {
	t.Setenv("USAGEWIDGET_TOKEN", validTestToken)
	t.Setenv("CROSSUSAGE_CMD", "   ")
	t.Setenv("CROSSUSAGE_BIN", "")
	t.Setenv("CROSSUSAGE_URL", "")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CrossUsageCmd != "" {
		t.Fatalf("expected trimmed empty CrossUsageCmd, got %q", cfg.CrossUsageCmd)
	}
	src := NewUsageSourceFromConfig(cfg)
	if src.SourceName() != "crossusage-collector" {
		t.Fatalf("got %s", src.SourceName())
	}
}
