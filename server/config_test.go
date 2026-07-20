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
	t.Setenv("CODEXBAR_URL", "")
	t.Setenv("CODEXBAR_BIN", "")
	t.Setenv("DB_PATH", "")
	t.Setenv("LISTEN_ADDR", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.CodexBarURL != "" {
		t.Fatalf("unexpected default CodexBarURL: %s", cfg.CodexBarURL)
	}
	if cfg.CollectorSocket != "/run/usagewidget/codexbar.sock" {
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

func TestLoadConfigPreservesCodexBarBinaryPath(t *testing.T) {
	t.Setenv("USAGEWIDGET_TOKEN", validTestToken)
	t.Setenv("CODEXBAR_BIN", `C:\\Program Files\\CodexBar\\codexbar.exe`)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.CodexBarBin != `C:\\Program Files\\CodexBar\\codexbar.exe` {
		t.Fatalf("CodexBarBin was changed: %q", cfg.CodexBarBin)
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
