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

const codexBarBody = `{
  "providers": [
    {
      "id": "codex",
      "name": "Codex",
      "primary": {"title": "5h limit", "usedPercent": 42.0, "resetsAt": "2026-07-17T20:00:00Z"},
      "codexResetCredits": {"availableCount": 2}
    }
  ]
}`

func newPollerHarness(t *testing.T) (*Poller, *Store, *atomic.Bool) {
	t.Helper()
	store := openTestStore(t)
	healthy := &atomic.Bool{}
	healthy.Store(true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !healthy.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(codexBarBody))
	}))
	t.Cleanup(server.Close)

	codexbar := NewCodexBarClient(server.URL)
	api := NewAPI(Config{Token: "x"}, store, codexbar)
	poller := NewPoller(store, codexbar, noopNotifier{}, api)
	return poller, store, healthy
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
	poller, store, _ := newPollerHarness(t)
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
	poller, store, healthy := newPollerHarness(t)
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
	poller, store, _ := newPollerHarness(t)
	responses := []string{
		`[{"provider":"claude","usage":{"primary":{"usedPercent":25,"windowMinutes":300}}},{"provider":"codex","usage":{"primary":{"usedPercent":10,"windowMinutes":300}}}]`,
		`[{"provider":"claude","error":{"message":"rate limited"}},{"provider":"codex","usage":{"primary":{"usedPercent":20,"windowMinutes":300}}}]`,
	}
	request := 0
	poller.codexbar.httpClient = &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
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
	claude := snapshot.Providers[0]
	if !claude.Stale || claude.Error != "" || len(claude.Windows) != 1 || claude.Windows[0].UsedPercent != 25 {
		t.Fatalf("Claude last-known usage was not preserved: %+v", claude)
	}
	codex := snapshot.Providers[1]
	if codex.Stale || len(codex.Windows) != 1 || codex.Windows[0].UsedPercent != 20 {
		t.Fatalf("fresh Codex usage was not saved: %+v", codex)
	}
}

func TestPollerNoRepeatEventsOnDuplicate(t *testing.T) {
	poller, store, _ := newPollerHarness(t)
	seedWindow(t, store, "codex.primary", 5, nil)

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
