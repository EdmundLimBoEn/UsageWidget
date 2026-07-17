package server

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStoreDefaultSettings(t *testing.T) {
	s := openTestStore(t)

	got, err := s.GetSetting("poll_interval_minutes")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if got != "5" {
		t.Fatalf("expected default poll_interval_minutes=5, got %q", got)
	}
}

func TestStoreSetSettingOverridesDefault(t *testing.T) {
	s := openTestStore(t)

	if err := s.SetSetting("poll_interval_minutes", "15"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	got, err := s.GetSetting("poll_interval_minutes")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if got != "15" {
		t.Fatalf("expected overridden value 15, got %q", got)
	}
}

func TestStoreSnapshotKeepsOnlyLatest(t *testing.T) {
	s := openTestStore(t)
	t1 := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 7, 17, 11, 0, 0, 0, time.UTC)

	if err := s.SaveSnapshot(t1, []byte(`{"n":1}`)); err != nil {
		t.Fatalf("SaveSnapshot 1: %v", err)
	}
	if err := s.SaveSnapshot(t2, []byte(`{"n":2}`)); err != nil {
		t.Fatalf("SaveSnapshot 2: %v", err)
	}

	fetchedAt, payload, ok, err := s.LatestSnapshot()
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}
	if !ok {
		t.Fatalf("expected a snapshot to exist")
	}
	if !fetchedAt.Equal(t2) {
		t.Fatalf("expected latest snapshot fetchedAt %v, got %v", t2, fetchedAt)
	}
	if string(payload) != `{"n":2}` {
		t.Fatalf("unexpected payload: %s", payload)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM snapshots`).Scan(&count); err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected only 1 snapshot retained, got %d", count)
	}
}

func TestStoreEventDedup(t *testing.T) {
	s := openTestStore(t)

	notified, err := s.EventNotified("codex.primary.early")
	if err != nil {
		t.Fatalf("EventNotified: %v", err)
	}
	if notified {
		t.Fatalf("expected event not notified yet")
	}

	if err := s.RecordEvent("codex.primary.early"); err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}
	if err := s.RecordEvent("codex.primary.early"); err != nil {
		t.Fatalf("RecordEvent (duplicate insert): %v", err)
	}

	notified, err = s.EventNotified("codex.primary.early")
	if err != nil {
		t.Fatalf("EventNotified: %v", err)
	}
	if !notified {
		t.Fatalf("expected event to be marked notified")
	}
}

func TestStoreDeviceUpsertAndDelete(t *testing.T) {
	s := openTestStore(t)

	if err := s.UpsertDevice("dev-1", "apns-a", "widget-a"); err != nil {
		t.Fatalf("UpsertDevice: %v", err)
	}
	if err := s.UpsertDevice("dev-1", "apns-b", "widget-b"); err != nil {
		t.Fatalf("UpsertDevice (rotation): %v", err)
	}

	devices, err := s.ListDevices()
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devices) != 1 || devices[0].APNsToken != "apns-b" || devices[0].WidgetToken != "widget-b" {
		t.Fatalf("expected rotated tokens for dev-1, got %+v", devices)
	}

	if err := s.DeleteDevice("dev-1"); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	devices, err = s.ListDevices()
	if err != nil {
		t.Fatalf("ListDevices after delete: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("expected no devices after delete, got %+v", devices)
	}

	if err := s.DeleteDevice("does-not-exist"); err != nil {
		t.Fatalf("DeleteDevice missing device should be a no-op: %v", err)
	}
}

func TestStoreWindowStateRoundTrip(t *testing.T) {
	s := openTestStore(t)
	resetsAt := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	credits := 2

	ws := WindowState{
		WindowID:         "codex.primary",
		UsedPercent:      42.0,
		ResetsAt:         &resetsAt,
		CreditsAvailable: &credits,
	}
	if err := s.SetWindowState(ws); err != nil {
		t.Fatalf("SetWindowState: %v", err)
	}

	got, ok, err := s.GetWindowState("codex.primary")
	if err != nil {
		t.Fatalf("GetWindowState: %v", err)
	}
	if !ok {
		t.Fatalf("expected window state to exist")
	}
	if got.UsedPercent != 42.0 || got.ResetsAt == nil || !got.ResetsAt.Equal(resetsAt) {
		t.Fatalf("unexpected window state: %+v", got)
	}
	if got.CreditsAvailable == nil || *got.CreditsAvailable != 2 {
		t.Fatalf("unexpected credits: %+v", got.CreditsAvailable)
	}

	_, ok, err = s.GetWindowState("does-not-exist")
	if err != nil {
		t.Fatalf("GetWindowState missing: %v", err)
	}
	if ok {
		t.Fatalf("expected missing window state to report ok=false")
	}
}
