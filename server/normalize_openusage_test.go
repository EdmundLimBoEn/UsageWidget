package server

import (
	"math"
	"os"
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

func TestNormalizeOpenUsageLimitsV1(t *testing.T) {
	fetchedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	body := `{
		"schema": "openusage.limits.v1",
		"generatedAt": "2026-09-01T12:00:00Z",
		"providers": {
			"cursor": {
				"displayName": "Cursor",
				"plan": "Pro",
				"stale": false,
				"resources": {
					"apiUsage": {"kind":"consumption","unit":"percent","utilization":0.8,"used":80,"limit":100,"remaining":20,"resetsAt":"2026-10-01T00:00:00Z","windowSeconds":2592000},
					"autoUsage": {"kind":"consumption","unit":"percent","utilization":0.125,"used":12.5,"limit":100,"remaining":87.5,"resetsAt":"2026-10-01T00:00:00Z","windowSeconds":2592000},
					"totalUsage": {"kind":"consumption","unit":"percent","utilization":0.45,"used":45,"limit":100,"remaining":55,"resetsAt":"2026-10-01T00:00:00Z","windowSeconds":2592000},
					"grokBot": {"kind":"consumption","unit":"percent","utilization":0.01,"used":1,"limit":100,"remaining":99,"windowSeconds":2592000},
					"credits": {"kind":"balance","unit":"usd","available":10}
				}
			},
			"codex": {
				"displayName": "Codex",
				"resources": {
					"session": {"kind":"consumption","unit":"percent","utilization":0.2,"used":0.2,"limit":100,"remaining":80,"resetsAt":"2026-09-01T17:00:00Z","windowSeconds":18000},
					"weekly": {"kind":"consumption","unit":"percent","utilization":0.4,"used":0.4,"limit":100,"remaining":60,"resetsAt":"2026-09-08T00:00:00Z","windowSeconds":604800},
					"credits": {"kind":"balance","unit":"credits","available":7}
				}
			}
		},
		"errors": [{"providerId":"claude","message":"Not logged in"}]
	}`
	snap, err := Normalize([]byte(body), 5, fetchedAt)
	if err != nil {
		t.Fatal(err)
	}
	if snap.SourceKind != "openusage" {
		t.Fatalf("source=%q", snap.SourceKind)
	}
	byID := map[string]Provider{}
	for _, p := range snap.Providers {
		byID[p.ID] = p
	}
	cursor, ok := byID["cursor"]
	if !ok {
		t.Fatalf("missing cursor: %+v", snap.Providers)
	}
	if len(cursor.Windows) != 2 {
		t.Fatalf("cursor windows: %+v", cursor.Windows)
	}
	if cursor.Windows[0].Title != "Plan" || math.Abs(cursor.Windows[0].UsedPercent-45) > 0.01 {
		t.Fatalf("plan: %+v", cursor.Windows[0])
	}
	if cursor.Windows[1].Title != "Auto" || math.Abs(cursor.Windows[1].UsedPercent-12.5) > 0.01 {
		t.Fatalf("auto: %+v", cursor.Windows[1])
	}
	for _, w := range cursor.Windows {
		if strings.Contains(strings.ToLower(w.Key), "api") || w.Key == "grokBot" || w.Title == "API" {
			t.Fatalf("noise leaked: %+v", w)
		}
	}
	codex, ok := byID["codex"]
	if !ok {
		t.Fatalf("missing codex")
	}
	if len(codex.Windows) != 2 || codex.Windows[0].Title != "5h" || math.Abs(codex.Windows[0].UsedPercent-20) > 0.01 {
		t.Fatalf("codex windows: %+v", codex.Windows)
	}
	if codex.Credits == nil || codex.Credits.AvailableCount != 7 {
		t.Fatalf("codex credits: %+v", codex.Credits)
	}
	claude, ok := byID["claude_code"]
	if !ok || claude.Error != "Not logged in" {
		t.Fatalf("claude: %+v ok=%v", claude, ok)
	}
}

func TestCollectOpenUsageLimitsMergesExtras(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/openusage"
	script := `#!/bin/sh
if [ $# -eq 0 ]; then
  printf '%s' '{"schema":"openusage.limits.v1","providers":{"cursor":{"displayName":"Cursor","resources":{"totalUsage":{"kind":"consumption","unit":"percent","utilization":0.1,"windowSeconds":2592000}}}},"errors":[]}'
  exit 0
fi
if [ "$1" = "copilot" ]; then
  printf '%s' '{"schema":"openusage.limits.v1","providers":{"copilot":{"displayName":"Copilot","resources":{"premiumCredits":{"kind":"consumption","unit":"percent","utilization":0.3,"windowSeconds":2592000}}}},"errors":[]}'
  exit 0
fi
if [ "$1" = "claude" ]; then
  printf '%s' '{"schema":"openusage.limits.v1","providers":{},"errors":[{"providerId":"claude","message":"Not logged in"}]}'
  exit 4
fi
echo 'openusage: Unknown provider' >&2
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	body, err := CollectOpenUsageLimits(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := Normalize(body, 5, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(snap.Providers))
	for _, p := range snap.Providers {
		ids = append(ids, p.ID)
	}
	joined := strings.Join(ids, ",")
	if !strings.Contains(joined, "cursor") || !strings.Contains(joined, "copilot") || !strings.Contains(joined, "claude_code") {
		t.Fatalf("merged ids=%v body=%s", ids, body)
	}
}

