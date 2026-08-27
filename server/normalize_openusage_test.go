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
					"requests_today": {"used": 40, "unit": "requests", "window": "1d"},
					"model_claude-4.5-opus_input_tokens": {"used": 1200, "unit": "tokens"}
				},
				"resets": {
					"billing_cycle_end": "2026-09-10T00:00:00Z"
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
