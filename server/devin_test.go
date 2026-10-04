package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const docsTeamCreditBalance = `{
  "promptCreditsPerSeat": 500,
  "numSeats": 50,
  "addOnCreditsAvailable": 10000,
  "addOnCreditsUsed": 3500,
  "billingCycleStart": "2026-01-01T00:00:00Z",
  "billingCycleEnd": "2026-02-01T00:00:00Z"
}`

func TestCreditBalanceFixtureBecomesDevinWindow(t *testing.T) {
	var bal teamCreditBalance
	if err := json.Unmarshal([]byte(docsTeamCreditBalance), &bal); err != nil {
		t.Fatal(err)
	}
	provider, errMsg := creditBalanceToLimitsProvider(bal)
	if provider == nil {
		t.Fatalf("expected provider, err=%q", errMsg)
	}
	env := limitsV1Envelope{
		Schema:    limitsSchemaV1,
		Providers: map[string]limitsV1Provider{"devin": *provider},
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	snap, err := Normalize(body, 5, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Providers) != 1 {
		t.Fatalf("providers=%+v", snap.Providers)
	}
	p := snap.Providers[0]
	if p.ID != "devin" || p.Name != "Devin" {
		t.Fatalf("canonical Devin: %+v", p)
	}
	if len(p.Windows) != 1 {
		t.Fatalf("windows=%+v", p.Windows)
	}
	w := p.Windows[0]
	if w.Title != "Credits" {
		t.Fatalf("title=%q", w.Title)
	}
	if w.UsedPercent < 25.9 || w.UsedPercent > 26.0 {
		t.Fatalf("used percent=%v", w.UsedPercent)
	}
	if w.RemainingPercent < 74.0 || w.RemainingPercent > 74.1 {
		t.Fatalf("remaining percent=%v", w.RemainingPercent)
	}
	if w.ResetsAt == nil || !w.ResetsAt.Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("resetsAt=%v", w.ResetsAt)
	}
	if p.Credits == nil || p.Credits.AvailableCount != 10000 {
		t.Fatalf("credits=%+v", p.Credits)
	}
}

func TestMockedDevinCollectorMergesIntoCrossUsagePayload(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method=%s", r.Method)
		}
		raw, _ := io.ReadAll(r.Body)
		var req map[string]string
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("request json: %v", err)
		}
		gotKey = req["service_key"]
		_, _ = w.Write([]byte(docsTeamCreditBalance))
	}))
	t.Cleanup(srv.Close)

	base := `{"schema":"crossusage.limits.v1","providers":{"cursor":{"displayName":"Cursor","resources":{"total-usage":{"unit":"percent","used":10,"limit":100,"utilization":0.1,"label":"Total usage"}}}},"errors":[]}`
	col := newDevinCollector("team-service-key", srv.URL, srv.Client())
	merged := attachDevinLimitsWith(context.Background(), []byte(base), col)
	if gotKey != "team-service-key" {
		t.Fatalf("service_key=%q", gotKey)
	}
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	snap, err := Normalize(merged, 5, now)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Provider{}
	for _, p := range snap.Providers {
		byID[p.ID] = p
	}
	if _, ok := byID["cursor"]; !ok {
		t.Fatalf("cursor dropped: %+v", snap.Providers)
	}
	devin, ok := byID["devin"]
	if !ok || len(devin.Windows) != 1 {
		t.Fatalf("devin=%+v ok=%v", devin, ok)
	}
}

func TestMissingDevinKeyLeavesOtherProvidersAlone(t *testing.T) {
	base := `{"schema":"crossusage.limits.v1","providers":{"codex":{"displayName":"Codex","resources":{"session":{"unit":"percent","used":10,"limit":100,"utilization":0.1,"label":"Session"}}}},"errors":[]}`
	t.Setenv(devinServiceKeyEnv, "")
	got := attachDevinLimits(context.Background(), []byte(base))
	if string(got) != base {
		t.Fatalf("missing key must be a no-op, got %s", got)
	}
	snap, err := Normalize(got, 5, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Providers) != 1 || snap.Providers[0].ID != "codex" {
		t.Fatalf("got %+v", snap.Providers)
	}
}

func TestDevinAuthFailureDoesNotFailCollection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid service key"}`, http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	base := `{"schema":"crossusage.limits.v1","providers":{"codex":{"displayName":"Codex","resources":{"session":{"unit":"percent","used":10,"limit":100,"utilization":0.1,"label":"Session"}}}},"errors":[]}`
	col := newDevinCollector("bad-key", srv.URL, srv.Client())
	merged := attachDevinLimitsWith(context.Background(), []byte(base), col)
	snap, err := Normalize(merged, 5, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Provider{}
	for _, p := range snap.Providers {
		byID[p.ID] = p
	}
	if byID["codex"].ID != "codex" {
		t.Fatalf("codex missing: %+v", snap.Providers)
	}
	devin, ok := byID["devin"]
	if !ok || !strings.Contains(strings.ToLower(devin.Error), "authentication") {
		t.Fatalf("expected Devin auth error, got %+v", devin)
	}
	if len(devin.Windows) != 0 {
		t.Fatalf("error-only Devin should have no windows: %+v", devin)
	}
}

func TestCognitionAliasNormalizesToDevin(t *testing.T) {
	body := `{"schema":"crossusage.limits.v1","providers":{"cognition":{"displayName":"Cognition","resources":{"credits":{"unit":"percent","used":40,"limit":100,"utilization":0.4,"label":"Credits"}}}}}`
	snap, err := Normalize([]byte(body), 5, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Providers) != 1 || snap.Providers[0].ID != "devin" || snap.Providers[0].Name != "Devin" {
		t.Fatalf("got %+v", snap.Providers)
	}
	if len(snap.Providers[0].Windows) != 1 {
		t.Fatalf("windows=%+v", snap.Providers[0].Windows)
	}
}
