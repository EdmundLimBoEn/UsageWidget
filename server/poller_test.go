package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func testHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

const crossUsageBody = `{
  "schema": "crossusage.limits.v1",
  "providers": {
    "codex": {
      "displayName": "Codex",
      "resources": {
        "session": {"unit":"percent","used":42,"limit":100,"utilization":0.42,"resetsAt":"2026-07-17T20:00:00Z","label":"Session","windowSeconds":18000}
      }
    }
  },
  "errors": []
}`

func newPollerHarness(t *testing.T) (*Poller, *Store, *atomic.Bool, *CrossUsageClient) {
	t.Helper()
	store := openTestStore(t)
	healthy := &atomic.Bool{}
	healthy.Store(true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !healthy.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(crossUsageBody))
	}))
	t.Cleanup(server.Close)

	client := NewCrossUsageHTTPClient(server.URL)
	api := NewAPI(Config{Token: "x"}, store, client)
	poller := NewPoller(store, client, noopNotifier{}, api)
	return poller, store, healthy, client
}

func latestSnap(t *testing.T, store *Store) Snapshot {
	t.Helper()
	_, payload, ok, err := store.LatestSnapshot()
	if err != nil || !ok {
		t.Fatalf("LatestSnapshot: ok=%v err=%v", ok, err)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	return snapshot
}

func TestPollerSavesSnapshot(t *testing.T) {
	poller, store, _, _ := newPollerHarness(t)
	result := poller.PollNow(context.Background())
	if !result.Success {
		t.Fatalf("poll failed: %+v", result)
	}
	snapshot := latestSnap(t, store)
	if snapshot.Stale || len(snapshot.Providers) != 1 || snapshot.Providers[0].ID != "codex" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestPollerStaleFallback(t *testing.T) {
	poller, store, healthy, _ := newPollerHarness(t)
	if result := poller.PollNow(context.Background()); !result.Success {
		t.Fatalf("seed poll failed: %+v", result)
	}
	fresh := latestSnap(t, store)

	healthy.Store(false)
	result := poller.PollNow(context.Background())
	if result.Success {
		t.Fatalf("failed upstream poll reported success: %+v", result)
	}
	stale := latestSnap(t, store)
	if !stale.Stale || len(stale.Providers) != len(fresh.Providers) || stale.Providers[0].ID != fresh.Providers[0].ID {
		t.Fatalf("expected stale previous snapshot, got %+v", stale)
	}
}

func TestPollerPreservesLastKnownUsageForErroredProvider(t *testing.T) {
	poller, store, _, client := newPollerHarness(t)
	responses := []string{
		`{"schema":"crossusage.limits.v1","providers":{"claude":{"resources":{"session":{"unit":"percent","used":25,"limit":100,"utilization":0.25,"windowSeconds":18000,"label":"Session"}}},"codex":{"resources":{"session":{"unit":"percent","used":10,"limit":100,"utilization":0.1,"windowSeconds":18000,"label":"Session"}}}}}`,
		`{"schema":"crossusage.limits.v1","providers":{"claude":{"resources":{}},"codex":{"resources":{"session":{"unit":"percent","used":20,"limit":100,"utilization":0.2,"windowSeconds":18000,"label":"Session"}}}},"errors":[{"providerId":"claude","message":"rate limited"}]}`,
	}
	request := 0
	client.httpClient = &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		body := responses[request]
		request++
		return testHTTPResponse(http.StatusOK, body), nil
	})}

	if result := poller.PollNow(context.Background()); !result.Success {
		t.Fatalf("first poll failed: %+v", result)
	}
	if result := poller.PollNow(context.Background()); !result.Success {
		t.Fatalf("partial poll failed: %+v", result)
	}

	snapshot := latestSnap(t, store)
	byID := map[string]Provider{}
	for _, p := range snapshot.Providers {
		byID[p.ID] = p
	}
	claude := byID["claude_code"]
	if claude.ID != "claude_code" || !claude.Stale || claude.Error != "" || len(claude.Windows) != 1 || claude.Windows[0].UsedPercent != 25 {
		t.Fatalf("Claude last-known usage was not preserved: %+v", claude)
	}
	codex := byID["codex"]
	if codex.Stale || len(codex.Windows) != 1 || codex.Windows[0].UsedPercent != 20 {
		t.Fatalf("fresh Codex usage was not saved: %+v", codex)
	}
}

func TestPollerDoesNotRestoreDroppedAPIProviders(t *testing.T) {
	poller, store, _, client := newPollerHarness(t)
	responses := []string{
		`{"schema":"crossusage.limits.v1","providers":{"cursor":{"resources":{"total-usage":{"unit":"percent","used":10,"limit":100,"utilization":0.1,"windowSeconds":2592000,"label":"Total usage"}}},"openai":{"resources":{"session":{"unit":"percent","used":99,"limit":100,"utilization":0.99,"label":"API"}}}}}`,
		`{"schema":"crossusage.limits.v1","providers":{"cursor":{"resources":{"total-usage":{"unit":"percent","used":11,"limit":100,"utilization":0.11,"windowSeconds":2592000,"label":"Total usage"}}}},"errors":[{"providerId":"openai","message":"No available fetch strategy for openai."}]}`,
	}
	request := 0
	client.httpClient = &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		body := responses[request]
		request++
		return testHTTPResponse(http.StatusOK, body), nil
	})}

	if result := poller.PollNow(context.Background()); !result.Success {
		t.Fatalf("first poll failed: %+v", result)
	}
	if got := latestSnap(t, store); len(got.Providers) != 1 || got.Providers[0].ID != "cursor" {
		t.Fatalf("first snapshot leaked API provider: %+v", got.Providers)
	}
	if result := poller.PollNow(context.Background()); !result.Success {
		t.Fatalf("second poll failed: %+v", result)
	}
	got := latestSnap(t, store)
	if len(got.Providers) != 1 || got.Providers[0].ID != "cursor" || got.Providers[0].Windows[0].UsedPercent != 11 {
		t.Fatalf("openai came back or cursor broke: %+v", got.Providers)
	}
}

func TestPollerNoRepeatEventsOnDuplicate(t *testing.T) {
	poller, store, _, _ := newPollerHarness(t)
	seedWindow(t, store, "codex.session", 5, nil)

	first := poller.PollNow(context.Background())
	if !first.Success || first.Events < 1 {
		t.Fatalf("expected successful poll with events, got %+v", first)
	}
	snapshot := latestSnap(t, store)
	window := snapshot.Providers[0].Windows[0]
	key := eventKey("early", window.ID, window.ResetsAt)
	notified, err := store.EventNotified(key)
	if err != nil || !notified {
		t.Fatalf("event was not recorded: notified=%v err=%v", notified, err)
	}

	second := poller.PollNow(context.Background())
	if !second.Success || second.Events != 0 {
		t.Fatalf("duplicate poll emitted events: %+v", second)
	}
}
