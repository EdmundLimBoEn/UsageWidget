package server

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSaveSnapshotWithForecastsComputesRegression(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	reset := base.Add(5 * time.Hour)
	for i, used := range []float64{10, 11, 12} {
		snap := Snapshot{FetchedAt: base.Add(time.Duration(i) * 15 * time.Minute), Providers: []Provider{{ID: "codex", Name: "Codex", Windows: []Window{{ID: "codex.primary", Key: "primary", Title: "5h", UsedPercent: used, RemainingPercent: 100 - used, ResetsAt: &reset}}}}}
		if err := store.SaveSnapshotWithForecasts(&snap); err != nil {
			t.Fatal(err)
		}
	}
	_, payload, ok, err := store.LatestSnapshot()
	if err != nil || !ok {
		t.Fatalf("latest: %v %v", ok, err)
	}
	var got Snapshot
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	f := got.Providers[0].Windows[0].Forecast
	if f == nil {
		t.Fatal("forecast missing")
	}
	if f.SampleCount != 3 {
		t.Fatalf("sample count=%d", f.SampleCount)
	}
	if f.BurnRatePercentPerHour < 3.99 || f.BurnRatePercentPerHour > 4.01 {
		t.Fatalf("burn rate=%v", f.BurnRatePercentPerHour)
	}
	if f.ExhaustsBeforeReset {
		t.Fatal("slow burn should last until reset")
	}
}

func TestForecastSkipsStaleProviders(t *testing.T) {
	store, _ := OpenStore(":memory:")
	defer store.Close()
	now := time.Now().UTC()
	reset := now.Add(time.Hour)
	snap := Snapshot{FetchedAt: now, Providers: []Provider{{ID: "codex", Stale: true, Windows: []Window{{ID: "codex.primary", ResetsAt: &reset, UsedPercent: 50}}}}}
	if err := store.SaveSnapshotWithForecasts(&snap); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM window_samples`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("recorded %d excluded samples", count)
	}
}

func TestForecastUsesCurrentMonotonicSegment(t *testing.T) {
	store, _ := OpenStore(":memory:")
	defer store.Close()
	base := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	reset := base.Add(10 * time.Hour)
	for i, used := range []float64{10, 15, 5, 6, 7} {
		snap := Snapshot{FetchedAt: base.Add(time.Duration(i) * 15 * time.Minute), Providers: []Provider{{ID: "codex", Windows: []Window{{ID: "codex.primary", UsedPercent: used, RemainingPercent: 100 - used, ResetsAt: &reset}}}}}
		if err := store.SaveSnapshotWithForecasts(&snap); err != nil {
			t.Fatal(err)
		}
	}
	_, payload, _, _ := store.LatestSnapshot()
	var got Snapshot
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	f := got.Providers[0].Windows[0].Forecast
	if f == nil || f.SampleCount != 3 {
		t.Fatalf("forecast=%+v", f)
	}
}

func TestSnapshotAndSamplesRollbackTogether(t *testing.T) {
	store, _ := OpenStore(":memory:")
	defer store.Close()
	if _, err := store.db.Exec(`CREATE TRIGGER reject_snapshot BEFORE INSERT ON snapshots BEGIN SELECT RAISE(ABORT, 'reject'); END`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	reset := now.Add(time.Hour)
	snap := Snapshot{FetchedAt: now, Providers: []Provider{{ID: "codex", Windows: []Window{{ID: "codex.primary", UsedPercent: 20, RemainingPercent: 80, ResetsAt: &reset}}}}}
	if err := store.SaveSnapshotWithForecasts(&snap); err == nil {
		t.Fatal("expected snapshot failure")
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM window_samples`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("samples committed after snapshot rollback: %d", count)
	}
}
