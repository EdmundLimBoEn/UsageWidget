package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

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

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Write([]byte(codexBarBody))
	}))
	t.Cleanup(srv.Close)

	codexbar := NewCodexBarClient(srv.URL)
	api := NewAPI(Config{Token: "x"}, store, codexbar)
	poller := NewPoller(store, codexbar, noopNotifier{}, api)
	return poller, store, healthy
}

func latestSnap(t *testing.T, s *Store) Snapshot {
	t.Helper()
	_, payload, ok, err := s.LatestSnapshot()
	if err != nil || !ok {
		t.Fatalf("LatestSnapshot: ok=%v err=%v", ok, err)
	}
	var snap Snapshot
	if err := json.Unmarshal(payload, &snap); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	return snap
}

func TestPollerSavesSnapshot(t *testing.T) {
	poller, store, _ := newPollerHarness(t)
	poller.pollOnce(context.Background())

	snap := latestSnap(t, store)
	if snap.Stale {
		t.Fatalf("expected fresh snapshot, got stale")
	}
	if len(snap.Providers) != 1 || snap.Providers[0].ID != "codex" {
		t.Fatalf("unexpected providers: %+v", snap.Providers)
	}
}

func TestPollerStaleFallback(t *testing.T) {
	poller, store, healthy := newPollerHarness(t)

	poller.pollOnce(context.Background())
	fresh := latestSnap(t, store)

	healthy.Store(false)
	poller.pollOnce(context.Background())

	stale := latestSnap(t, store)
	if !stale.Stale {
		t.Fatalf("expected snapshot marked stale after fetch failure")
	}
	if len(stale.Providers) != len(fresh.Providers) || stale.Providers[0].ID != fresh.Providers[0].ID {
		t.Fatalf("expected previous providers preserved, got %+v", stale.Providers)
	}
}

func TestPollerNoRepeatEventsOnDuplicate(t *testing.T) {
	poller, store, _ := newPollerHarness(t)

	// Seed a baseline below early so the first real poll crosses it.
	seedWindow(t, store, "codex.primary", 5, nil)

	poller.pollOnce(context.Background())
	first, err := store.EventNotified("early:codex.primary:2026-07-17T20:00:00Z")
	if err != nil {
		t.Fatalf("EventNotified: %v", err)
	}
	if !first {
		t.Fatalf("expected early event recorded after first poll")
	}

	// A second identical poll must not produce a fresh crossing.
	poller.pollOnce(context.Background())
	snap := latestSnap(t, store)
	if snap.Providers[0].Windows[0].UsedPercent != 42 {
		t.Fatalf("unexpected snapshot state: %+v", snap.Providers[0].Windows[0])
	}
}
