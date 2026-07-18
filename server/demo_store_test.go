package server

import (
	"path/filepath"
	"strings"
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

func TestDemoStateSaveRejectsInvalidState(t *testing.T) {
	store := openTestStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	valid := DefaultDemoState(now)
	if err := store.SaveDemoState(valid); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*DemoState)
	}{
		{name: "zero updated timestamp", mutate: func(state *DemoState) { state.UpdatedAt = time.Time{} }},
		{name: "zero primary reset timestamp", mutate: func(state *DemoState) { state.Primary.ResetsAt = time.Time{} }},
		{name: "zero secondary reset timestamp", mutate: func(state *DemoState) { state.Secondary.ResetsAt = time.Time{} }},
		{name: "primary percentage above one hundred", mutate: func(state *DemoState) { state.Primary.UsedPercent = 100.1 }},
		{name: "secondary percentage below zero", mutate: func(state *DemoState) { state.Secondary.UsedPercent = -0.1 }},
		{name: "negative credits", mutate: func(state *DemoState) { state.CreditsAvailable = -1 }},
		{name: "primary reset too old", mutate: func(state *DemoState) { state.Primary.ResetsAt = now.Add(-24*time.Hour - time.Second) }},
		{name: "secondary reset too new", mutate: func(state *DemoState) { state.Secondary.ResetsAt = now.Add(31*24*time.Hour + time.Second) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invalid := valid
			tt.mutate(&invalid)
			if err := store.SaveDemoState(invalid); err == nil {
				t.Fatal("expected invalid state to be rejected")
			}
			got, err := store.LoadDemoState()
			if err != nil {
				t.Fatal(err)
			}
			if got != valid {
				t.Fatalf("invalid save changed durable state:\n got %#v\nwant %#v", got, valid)
			}
		})
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
			Stages:      []DemoPipelineStage{{ID: "normalize", Status: DemoStageOK}},
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

func TestDemoRunStorePersistsPipelineContract(t *testing.T) {
	store := openTestStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	want := DemoRun{
		StartedAt: now, CompletedAt: now.Add(time.Second), Success: true,
		SnapshotChanged: true, EventsEmitted: 2, EventsDeduplicated: 1,
		Stages:   []DemoPipelineStage{{ID: "apns", Status: DemoStageWarning, Detail: "one failure", DurationMS: 3}},
		Delivery: DemoDeliveryResult{Alerts: DeliveryCount{Attempted: 2, Succeeded: 1, Failed: 1}},
	}
	id, err := store.SaveDemoRun(want)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.LatestDemoRun()
	if err != nil || !ok {
		t.Fatalf("LatestDemoRun: ok=%v err=%v", ok, err)
	}
	if got.ID != id || got.EventsEmitted != want.EventsEmitted || got.Delivery != want.Delivery || len(got.Stages) != 1 || got.Stages[0] != want.Stages[0] {
		t.Fatalf("pipeline result did not round trip:\n got %+v\nwant %+v", got, want)
	}
}

func TestDemoEventStoreDefaultsCapsAndRetains(t *testing.T) {
	store := openTestStore(t)
	base := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	events := make([]DemoEvent, 0, 510)
	for i := 0; i < 510; i++ {
		events = append(events, DemoEvent{
			Key:       "demo.window.threshold",
			Type:      "early_threshold",
			CreatedAt: base.Add(time.Duration(i) * time.Second),
			Before:    &EventValue{},
			After:     &EventValue{},
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

func TestDemoEventStoreRejectsInvalidContract(t *testing.T) {
	store := openTestStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	for _, event := range []DemoEvent{
		{Key: "real.test", Type: "test_alert", CreatedAt: now},
		{Key: "demo.test", Type: "unknown", CreatedAt: now},
		{Key: "demo.test", Type: "test_alert"},
	} {
		err := store.AppendDemoEvents([]DemoEvent{event})
		if err == nil {
			t.Fatalf("expected invalid event %+v to be rejected", event)
		}
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM demo_event_log`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected no durable events, got %d", count)
	}
}

func TestDemoEventStoreRejectsMixedBatchAtomically(t *testing.T) {
	store := openTestStore(t)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	events := []DemoEvent{
		{Key: "demo.first", Type: "test_alert", CreatedAt: now},
		{Key: "not-demo.invalid", Type: "test_alert", CreatedAt: now},
		{Key: "demo.last", Type: "test_alert", CreatedAt: now},
	}
	if err := store.AppendDemoEvents(events); err == nil {
		t.Fatal("expected mixed batch to be rejected")
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM demo_event_log`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected mixed batch to write nothing, got %d events", count)
	}
}

func TestDemoExecutionStoreRollsBackRunEventsAndPruning(t *testing.T) {
	store := openTestStore(t)
	base := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	var previousRunID int64
	for i := 0; i < 20; i++ {
		run := DemoRun{
			StartedAt:   base.Add(time.Duration(i) * time.Minute),
			CompletedAt: base.Add(time.Duration(i)*time.Minute + time.Second),
			Success:     true,
		}
		id, err := store.SaveDemoRun(run)
		if err != nil {
			t.Fatalf("seed run %d: %v", i, err)
		}
		previousRunID = id
	}
	events := make([]DemoEvent, 500)
	for i := range events {
		events[i] = DemoEvent{
			Key:       "demo.seed",
			Type:      "test_alert",
			CreatedAt: base.Add(time.Duration(i) * time.Second),
		}
	}
	if err := store.AppendDemoEvents(events); err != nil {
		t.Fatalf("seed events: %v", err)
	}
	if _, err := store.db.Exec(`
		CREATE TRIGGER fail_manual_poll
		BEFORE INSERT ON demo_event_log
		WHEN NEW.event_type = 'manual_poll'
		BEGIN
			SELECT RAISE(ABORT, 'injected event persistence failure');
		END
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	run := DemoRun{
		StartedAt:   base.Add(21 * time.Minute),
		CompletedAt: base.Add(21*time.Minute + time.Second),
		Success:     true,
	}
	_, err := store.SaveDemoExecution(run, []DemoEvent{{
		Key:       "demo.manual_poll",
		Type:      "manual_poll",
		CreatedAt: run.CompletedAt,
	}})
	if err == nil || !strings.Contains(err.Error(), "injected event persistence failure") {
		t.Fatalf("SaveDemoExecution error=%v, want injected event failure", err)
	}

	latest, ok, err := store.LatestDemoRun()
	if err != nil || !ok {
		t.Fatalf("LatestDemoRun: ok=%v err=%v", ok, err)
	}
	if latest.ID != previousRunID {
		t.Fatalf("failed transaction stored run %d; previous latest was %d", latest.ID, previousRunID)
	}
	var runCount, eventCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM demo_runs`).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM demo_event_log`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 20 || eventCount != 500 {
		t.Fatalf("failed transaction changed retained rows: runs=%d events=%d", runCount, eventCount)
	}
}
