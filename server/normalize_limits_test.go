package server

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNormalizeLimitsV1CursorAndFiltersNoise(t *testing.T) {
	fetchedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	body := `{
		"schema": "crossusage.limits.v1",
		"generatedAt": "2026-09-01T12:00:00Z",
		"providers": {
			"cursor": {
				"displayName": "Cursor",
				"plan": "Pro",
				"stale": false,
				"resources": {
					"api-usage": {"unit":"percent","utilization":0.8,"used":80,"limit":100,"remaining":20,"resetsAt":"2026-10-01T00:00:00Z","windowSeconds":2592000,"label":"API usage"},
					"auto-usage": {"unit":"percent","utilization":0.125,"used":12.5,"limit":100,"remaining":87.5,"resetsAt":"2026-10-01T00:00:00Z","windowSeconds":2592000,"label":"Auto usage"},
					"total-usage": {"unit":"percent","utilization":0.45,"used":45,"limit":100,"remaining":55,"resetsAt":"2026-10-01T00:00:00Z","windowSeconds":2592000,"label":"Total usage"},
					"grokBot": {"unit":"percent","utilization":0.01,"used":1,"limit":100,"remaining":99,"windowSeconds":2592000,"label":"Grok bot"},
					"credits": {"kind":"balance","unit":"usd","available":10}
				}
			},
			"codex": {
				"displayName": "Codex",
				"resources": {
					"session": {"unit":"percent","utilization":0.2,"used":20,"limit":100,"remaining":80,"resetsAt":"2026-09-01T17:00:00Z","windowSeconds":18000,"label":"Session"},
					"weekly": {"unit":"percent","utilization":0.4,"used":40,"limit":100,"remaining":60,"resetsAt":"2026-09-08T00:00:00Z","windowSeconds":604800,"label":"Weekly"},
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
	if snap.SourceKind != "crossusage" {
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

func TestNormalizeDropsProvidersWithoutWindows(t *testing.T) {
	body := `{"schema":"crossusage.limits.v1","providers":{"codex":{"resources":{"session":{"unit":"percent","used":10,"limit":100,"utilization":0.1,"label":"Session"}}},"noise":{"resources":{}}}}`
	snap, err := Normalize([]byte(body), 5, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Providers) != 1 || snap.Providers[0].ID != "codex" {
		t.Fatalf("got %+v", snap.Providers)
	}
}

func TestNormalizeKeepsCursorPlanDropsAPI(t *testing.T) {
	body := `{
		"schema": "crossusage.limits.v1",
		"providers": {
			"cursor": {
				"resources": {
					"total-usage": {"unit":"percent","used":41,"limit":100,"utilization":0.41,"windowSeconds":2592000,"label":"Total usage","resetsAt":"2026-09-10T00:00:00Z"},
					"auto-usage": {"unit":"percent","used":12,"limit":100,"utilization":0.12,"windowSeconds":2592000,"label":"Auto usage","resetsAt":"2026-09-10T00:00:00Z"},
					"api-usage": {"unit":"percent","used":7,"limit":100,"utilization":0.07,"windowSeconds":2592000,"label":"API usage"}
				}
			},
			"openai": {
				"resources": {
					"session": {"unit":"percent","used":88,"limit":100,"utilization":0.88,"label":"API"}
				}
			}
		}
	}`
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

func TestCollectCrossUsageLimitsMergesExtras(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/crossusage-cli"
	script := `#!/bin/sh
if [ "$1" != "limits" ]; then
  echo 'crossusage: expected limits' >&2
  exit 2
fi
if [ $# -gt 2 ]; then
  echo 'Unknown plugin id: gemini' >&2
  exit 1
fi
case "$2" in
  cursor)
    printf '%s' '{"schema":"crossusage.limits.v1","providers":{"cursor":{"displayName":"Cursor","resources":{"total-usage":{"unit":"percent","utilization":0.1,"windowSeconds":2592000,"label":"Total usage"}}}},"errors":[]}'
    ;;
  copilot)
    printf '%s' '{"schema":"crossusage.limits.v1","providers":{"copilot":{"displayName":"Copilot","resources":{"premium-credits":{"unit":"percent","utilization":0.3,"windowSeconds":2592000,"label":"Premium"}}}},"errors":[]}'
    ;;
  claude)
    printf '%s' '{"schema":"crossusage.limits.v1","providers":{},"errors":[{"providerId":"claude","message":"Not logged in"}]}'
    exit 4
    ;;
  *)
    echo "Unknown plugin id: $2" >&2
    exit 2
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	body, err := CollectCrossUsageLimits(t.Context(), path)
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

func TestAntigravityMapsToGemini(t *testing.T) {
	body := `{"schema":"crossusage.limits.v1","providers":{"antigravity":{"displayName":"Antigravity","resources":{"session":{"unit":"percent","used":15,"limit":100,"utilization":0.15,"label":"Session"}}}}}`
	snap, err := Normalize([]byte(body), 5, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Providers) != 1 || snap.Providers[0].ID != "gemini_cli" || snap.Providers[0].Name != "Gemini" {
		t.Fatalf("got %+v", snap.Providers)
	}
}
