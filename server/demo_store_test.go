package server

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestDemoStateSeedAndPersistence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "demo.db")
	before := time.Now().UTC()
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := store.LoadDemoState()
	if err != nil {
		t.Fatal(err)
	}
	if seeded.Primary.UsedPercent != 62 || seeded.Secondary.UsedPercent != 34 || seeded.CreditsAvailable != 2 || seeded.UpdatedAt.Before(before) {
		t.Fatalf("unexpected seeded state: %#v", seeded)
	}

	want := DefaultDemoState(time.Date(2026, 7, 18, 12, 0, 0, 123456000, time.UTC))
	want.Stale = true
	if err := store.SaveDemoState(want); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	got, err := store.LoadDemoState()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("state was not preserved across reopen:\n got %#v\nwant %#v", got, want)
	}
}

func TestDemoRunStoreRetainsLatestTwenty(t *testing.T) {
	store := openTestStore(t)
	base := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	var lastID int64
	for i := 0; i < 25; i++ {
		run := DemoRun{
			StartedAt:   base.Add(time.Duration(i) * time.Minute),
			CompletedAt: base.Add(time.Duration(i)*time.Minute + time.Second),
			Success:     i%2 == 0,
			Payload:     json.RawMessage(`{"sequence":` + string(rune('0'+i%10)) + `}`),
		}
		id, err := store.SaveDemoRun(run)
		if err != nil {
			t.Fatalf("SaveDemoRun %d: %v", i, err)
		}
		lastID = id
	}

	got, ok, err := store.LatestDemoRun()
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.ID != lastID || !got.StartedAt.Equal(base.Add(24*time.Minute)) || !got.Success {
		t.Fatalf("unexpected latest run: %#v, ok=%v", got, ok)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM demo_runs`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 20 {
		t.Fatalf("expected 20 retained runs, got %d", count)
	}
}

func TestDemoEventStoreDefaultsCapsAndRetains(t *testing.T) {
	store := openTestStore(t)
	base := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	events := make([]DemoEvent, 0, 510)
	for i := 0; i < 510; i++ {
		events = append(events, DemoEvent{
			EventKey:  "demo.window.threshold",
			EventType: "threshold",
			CreatedAt: base.Add(time.Duration(i) * time.Second),
			Payload:   json.RawMessage(`{"ok":true}`),
		})
	}
	if err := store.AppendDemoEvents(events); err != nil {
		t.Fatal(err)
	}

	defaults, err := store.ListDemoEvents(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(defaults) != 50 {
		t.Fatalf("expected default limit 50, got %d", len(defaults))
	}
	capped, err := store.ListDemoEvents(1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 100 {
		t.Fatalf("expected capped limit 100, got %d", len(capped))
	}
	if !capped[0].CreatedAt.Equal(base.Add(509 * time.Second)) {
		t.Fatalf("expected newest event first, got %#v", capped[0])
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM demo_event_log`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 500 {
		t.Fatalf("expected 500 retained demo events, got %d", count)
	}
}
