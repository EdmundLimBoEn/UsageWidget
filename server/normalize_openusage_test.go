package server

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestNormalizeOpenUsageCursorAndFiltersNoise(t *testing.T) {
	fetchedAt := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	body := `{
		"schema_version": "1",
		"generated_at": "2026-08-27T12:00:00Z",
		"snapshots": [
			{
				"provider_id": "cursor",
				"account_id": "me",
				"timestamp": "2026-08-27T12:00:00Z",
				"status": "OK",
				"metrics": {
					"plan_percent_used": {"used": 45, "limit": 100, "unit": "%", "window": "30d"},
					"plan_auto_percent_used": {"used": 12.5, "limit": 100, "unit": "%", "window": "30d"},
					"plan_api_percent_used": {"used": 80, "limit": 100, "unit": "%", "window": "30d"},
					"requests_today": {"used": 40, "unit": "requests", "window": "1d"},
					"model_claude-4.5-opus_input_tokens": {"used": 1200, "unit": "tokens"}
				},
				"resets": {
					"billing_cycle_end": "2026-09-10T00:00:00Z"
				}
			},
			{
				"provider_id": "openai",
				"account_id": "org",
				"timestamp": "2026-08-27T12:00:00Z",
				"status": "OK",
				"metrics": {
					"plan_percent_used": {"used": 9, "limit": 100, "unit": "%", "window": "30d"}
				}
			},
			{
				"provider_id": "ollama",
				"account_id": "local",
				"timestamp": "2026-08-27T12:00:00Z",
				"status": "OK",
				"metrics": {
					"total_ai_requests": {"used": 9, "unit": "requests", "window": "all-time"}
				}
			},
			{
				"provider_id": "claude_code",
				"account_id": "me",
				"timestamp": "2026-08-27T12:00:00Z",
				"status": "OK",
				"metrics": {
					"usage_five_hour": {"used": 22, "limit": 100, "unit": "%", "window": "5h"},
					"usage_seven_day": {"used": 40, "limit": 100, "unit": "%", "window": "7d"}
				},
				"resets": {
					"usage_five_hour": "2026-08-27T16:00:00Z",
					"usage_seven_day": "2026-09-01T00:00:00Z"
				}
			}
		]
	}`

	snap, err := Normalize([]byte(body), 5, fetchedAt)
	if err != nil {
		t.Fatal(err)
	}
	if snap.SourceKind != "openusage" {
		t.Fatalf("source=%q", snap.SourceKind)
	}
	if len(snap.Providers) != 2 {
		t.Fatalf("expected cursor+claude_code only, got %+v", snap.Providers)
	}
	if snap.Providers[0].ID != "cursor" || snap.Providers[0].Name != "Cursor" {
		t.Fatalf("cursor provider: %+v", snap.Providers[0])
	}
	if len(snap.Providers[0].Windows) < 2 {
		t.Fatalf("cursor windows: %+v", snap.Providers[0].Windows)
	}
	plan := snap.Providers[0].Windows[0]
	if plan.Title != "Plan" || plan.UsedPercent != 45 || plan.ResetsAt == nil {
		t.Fatalf("plan window: %+v", plan)
	}
	for _, w := range snap.Providers[0].Windows {
		if w.Title == "API" || strings.Contains(strings.ToLower(w.Key), "api") {
			t.Fatalf("API window leaked through: %+v", w)
		}
	}
	if plan.WindowLabel != "30d" {
		t.Fatalf("window label=%q", plan.WindowLabel)
	}
	if plan.PaceForecast == nil || plan.PaceForecast.Source != "pace" {
		t.Fatalf("expected pace forecast: %+v", plan.PaceForecast)
	}
}

func TestPaceProjectionOvershootsWindow(t *testing.T) {
	now := time.Date(2026, 8, 27, 17, 18, 0, 0, time.UTC)
	reset := time.Date(2026, 8, 27, 21, 0, 0, 0, time.UTC)
	f := paceProjection(22, reset, "5h", now)
	if f == nil {
		t.Fatal("expected forecast")
	}
	if f.ExhaustsBeforeReset {
		t.Fatalf("should not exhaust before reset: %+v", f)
	}
	if f.ProjectedPercentAtReset == nil || math.Abs(*f.ProjectedPercentAtReset-85) > 1.5 {
		t.Fatalf("projected at reset=%v annotation=%q", f.ProjectedPercentAtReset, f.Annotation)
	}
	if !strings.Contains(f.Annotation, "~85% by reset") {
		t.Fatalf("annotation=%q", f.Annotation)
	}
}

func TestPaceProjectionFitsInWindow(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	reset := now.Add(4 * time.Hour)
	f := paceProjection(50, reset, "5h", now)
	if f == nil || !f.ExhaustsBeforeReset {
		t.Fatalf("expected exhaustion before reset: %+v", f)
	}
	if !strings.Contains(f.Annotation, "100% in") {
		t.Fatalf("annotation=%q", f.Annotation)
	}
}

func TestCodexBarDropsProvidersWithoutWindows(t *testing.T) {
	body := `[{"provider":"codex","usage":{"primary":{"usedPercent":10,"windowMinutes":300}}},{"provider":"noise"}]`
	snap, err := Normalize([]byte(body), 5, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Providers) != 1 || snap.Providers[0].ID != "codex" {
		t.Fatalf("got %+v", snap.Providers)
	}
}

func TestCodexBarKeepsCursorPlanDropsAPI(t *testing.T) {
	body := `[
		{
			"provider": "cursor",
			"usage": {
				"primary": {"usedPercent": 41, "windowMinutes": 43200, "resetsAt": "2026-09-10T00:00:00Z"},
				"secondary": {"usedPercent": 12, "windowMinutes": 43200, "resetsAt": "2026-09-10T00:00:00Z"},
				"tertiary": {"title": "Third Party", "usedPercent": 7, "windowMinutes": 43200, "resetsAt": "2026-09-10T00:00:00Z"}
			}
		},
		{
			"provider": "openai",
			"usage": {
				"primary": {"usedPercent": 88, "resetDescription": "$12 available"}
			}
		}
	]`
	snap, err := Normalize([]byte(body), 5, time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Providers) != 1 {
		t.Fatalf("expected cursor only, got %+v", snap.Providers)
	}
	p := snap.Providers[0]
	if p.ID != "cursor" || p.Name != "Cursor" {
		t.Fatalf("cursor: %+v", p)
	}
	if len(p.Windows) != 2 {
		t.Fatalf("expected Plan+Auto, got %+v", p.Windows)
	}
	if p.Windows[0].Title != "Plan" || p.Windows[0].UsedPercent != 41 {
		t.Fatalf("plan: %+v", p.Windows[0])
	}
	if p.Windows[1].Title != "Auto" || p.Windows[1].UsedPercent != 12 {
		t.Fatalf("auto: %+v", p.Windows[1])
	}
}

func TestOpenUsageLaterAuthDoesNotPoisonHealthyAccount(t *testing.T) {
	body := `{
		"schema_version":"1",
		"snapshots":[
			{
				"provider_id":"cursor",
				"account_id":"good",
				"status":"OK",
				"metrics":{"plan_percent_used":{"used":10,"limit":100,"unit":"%","window":"30d"}},
				"resets":{"billing_cycle_end":"2026-09-10T00:00:00Z"}
			},
			{
				"provider_id":"cursor",
				"account_id":"bad",
				"status":"AUTH_REQUIRED",
				"message":"login again"
			}
		]
	}`
	snap, err := Normalize([]byte(body), 5, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Providers) != 1 {
		t.Fatalf("providers=%+v", snap.Providers)
	}
	if snap.Providers[0].Error != "" || len(snap.Providers[0].Windows) == 0 {
		t.Fatalf("healthy cursor poisoned: %+v", snap.Providers[0])
	}
}

func TestBillingResetFallbackDoesNotApplyToUsageWindows(t *testing.T) {
	body := `{
		"schema_version":"1",
		"snapshots":[{
			"provider_id":"claude_code",
			"status":"OK",
			"metrics":{
				"usage_five_hour":{"used":22,"limit":100,"unit":"%","window":"5h"},
				"plan_percent_used":{"used":40,"limit":100,"unit":"%","window":"30d"}
			},
			"resets":{"billing_cycle_end":"2026-09-10T00:00:00Z"}
		}]
	}`
	snap, err := Normalize([]byte(body), 5, time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	p := snap.Providers[0]
	var fiveHour, plan Window
	for _, w := range p.Windows {
		switch w.Title {
		case "5h":
			fiveHour = w
		case "Plan":
			plan = w
		}
	}
	if fiveHour.ResetsAt != nil {
		t.Fatalf("5h window incorrectly inherited billing reset: %+v", fiveHour)
	}
	if plan.ResetsAt == nil {
		t.Fatalf("plan window missing billing reset: %+v", plan)
	}
}

func TestWhitespaceOpenUsageCmdIsIgnored(t *testing.T) {
	t.Setenv("USAGEWIDGET_TOKEN", validTestToken)
	t.Setenv("OPENUSAGE_CMD", "   ")
	t.Setenv("OPENUSAGE_BIN", "")
	t.Setenv("OPENUSAGE_URL", "")
	t.Setenv("OPENUSAGE_SOCKET", "")
	t.Setenv("CODEXBAR_URL", "")
	t.Setenv("CODEXBAR_BIN", "")
	t.Setenv("CODEXBAR_CMD", "")
	t.Setenv("USAGE_SOURCE", "auto")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OpenUsageCmd != "" {
		t.Fatalf("expected trimmed empty OpenUsageCmd, got %q", cfg.OpenUsageCmd)
	}
	src := NewUsageSourceFromConfig(cfg)
	if src.SourceName() != "openusage-collector" {
		t.Fatalf("got %s", src.SourceName())
	}
}
