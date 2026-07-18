package server

import (
	"strings"
	"testing"
)

func TestLoadConfigRequiresToken(t *testing.T) {
	t.Setenv("USAGEWIDGET_TOKEN", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatalf("expected error when USAGEWIDGET_TOKEN is unset")
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("USAGEWIDGET_TOKEN", "secret")
	t.Setenv("CODEXBAR_URL", "")
	t.Setenv("DB_PATH", "")
	t.Setenv("LISTEN_ADDR", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.CodexBarURL != "http://127.0.0.1:8765/usage" {
		t.Fatalf("unexpected default CodexBarURL: %s", cfg.CodexBarURL)
	}
	if cfg.DBPath != "./usagewidget.db" {
		t.Fatalf("unexpected default DBPath: %s", cfg.DBPath)
	}
	if cfg.ListenAddr != ":8377" {
		t.Fatalf("unexpected default ListenAddr: %s", cfg.ListenAddr)
	}
	if cfg.APNsEnabled() {
		t.Fatalf("expected APNs disabled when APNs env vars are unset")
	}
}

func TestLoadConfigAPNsEnabledWhenAllVarsPresent(t *testing.T) {
	t.Setenv("USAGEWIDGET_TOKEN", "secret")
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

func TestLoadConfigDemoDeviceIDsAndAccessIdentityHeader(t *testing.T) {
	t.Setenv("USAGEWIDGET_TOKEN", "secret")
	t.Setenv("DEMO_DEVICE_IDS", " phone-a, phone-b,phone-a ")
	t.Setenv("ACCESS_IDENTITY_HEADER", " X-Operator ")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(cfg.DemoDeviceIDs, ","), "phone-a,phone-b"; got != want {
		t.Fatalf("device ids=%q want %q", got, want)
	}
	if cfg.AccessIdentityHeader != "X-Operator" {
		t.Fatalf("identity header=%q", cfg.AccessIdentityHeader)
	}
	t.Setenv("DEMO_DEVICE_IDS", "phone-a,")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected empty device ID rejection")
	}
	t.Setenv("DEMO_DEVICE_IDS", "")
	t.Setenv("ACCESS_IDENTITY_HEADER", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected blank identity header rejection")
	}
}

func TestLoadConfigDemoListener(t *testing.T) {
	t.Setenv("USAGEWIDGET_TOKEN", "secret")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DemoEnabled {
		t.Fatal("demo must be disabled by default")
	}
	if cfg.DemoListenAddr != "127.0.0.1:8378" {
		t.Fatalf("demo listen addr=%q", cfg.DemoListenAddr)
	}

	t.Setenv("USAGEWIDGET_DEMO_ENABLED", "true")
	for _, addr := range []string{"0.0.0.0:8378", ":8378", "not-an-address"} {
		t.Setenv("DEMO_LISTEN_ADDR", addr)
		if _, err := LoadConfig(); err == nil {
			t.Fatalf("expected %q to be rejected", addr)
		}
	}

	t.Setenv("DEMO_LISTEN_ADDR", "127.0.0.2:9000")
	cfg, err = LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DemoEnabled || cfg.DemoListenAddr != "127.0.0.2:9000" {
		t.Fatalf("unexpected demo config: %+v", cfg)
	}
}
