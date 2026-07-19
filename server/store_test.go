package server

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenStoreMigratesLegacyDatabaseWithoutDataLoss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := `CREATE TABLE snapshots(id INTEGER PRIMARY KEY AUTOINCREMENT,fetched_at TEXT NOT NULL,payload TEXT NOT NULL);CREATE TABLE settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);CREATE TABLE devices(device_id TEXT PRIMARY KEY,apns_token TEXT NOT NULL DEFAULT '',widget_token TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL);CREATE TABLE events(event_key TEXT PRIMARY KEY,created_at TEXT NOT NULL);CREATE TABLE window_state(window_id TEXT PRIMARY KEY,used_percent REAL NOT NULL,resets_at TEXT,credits_available INTEGER,updated_at TEXT NOT NULL);CREATE TABLE poll_runs(id INTEGER PRIMARY KEY AUTOINCREMENT,polled_at TEXT NOT NULL,success INTEGER NOT NULL,snapshot_changed INTEGER NOT NULL,duration_ms INTEGER NOT NULL,error TEXT NOT NULL DEFAULT '');`
	if _, err = db.Exec(legacy); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, statement := range []string{
		`INSERT INTO snapshots(fetched_at,payload) VALUES('2026-07-19T00:00:00Z','{}')`,
		`INSERT INTO settings(key,value) VALUES('poll_interval_minutes','15')`,
		`INSERT INTO devices(device_id,apns_token,widget_token,updated_at) VALUES('phone','alert','widget','` + now + `')`,
		`INSERT INTO events(event_key,created_at) VALUES('danger:legacy','` + now + `')`,
		`INSERT INTO window_state(window_id,used_percent,updated_at) VALUES('codex.primary',42,'` + now + `')`,
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if version, _ := store.SchemaVersion(); version != CurrentSchemaVersion {
		t.Fatalf("schema=%d", version)
	}
	if value, _ := store.GetSetting("poll_interval_minutes"); value != "15" {
		t.Fatalf("setting=%q", value)
	}
	if _, ok, _ := store.GetDevice("phone"); !ok {
		t.Fatal("device lost")
	}
	if notified, _ := store.EventNotified("danger:legacy"); !notified {
		t.Fatal("event state lost")
	}
	if state, ok, _ := store.GetWindowState("codex.primary"); !ok || state.UsedPercent != 42 {
		t.Fatalf("window state=%+v ok=%v", state, ok)
	}
	if _, _, ok, _ := store.LatestSnapshot(); !ok {
		t.Fatal("snapshot lost")
	}
}

func TestOpenStoreUsesPrivateFilePermissions(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "private.db")
	if err := os.WriteFile(dbPath, nil, 0644); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("database mode=%#o want 0600", got)
	}
}

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

func TestDemoEventPruningDoesNotTouchDedupEvents(t *testing.T) {
	s := openTestStore(t)
	if err := s.RecordEvent("codex.primary.early"); err != nil {
		t.Fatal(err)
	}
	events := make([]DemoEvent, 501)
	for i := range events {
		events[i] = DemoEvent{
			EventKey:  "demo.primary.early",
			EventType: "early",
			CreatedAt: time.Date(2026, 7, 18, 12, 0, i, 0, time.UTC),
			Payload:   []byte(`{}`),
		}
	}
	if err := s.AppendDemoEvents(events); err != nil {
		t.Fatal(err)
	}
	notified, err := s.EventNotified("codex.primary.early")
	if err != nil {
		t.Fatal(err)
	}
	if !notified {
		t.Fatal("demo pruning removed an existing dedup event")
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

func TestStoreClearsTokensIndependently(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertDevice("dev-1", "apns", "widget"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearWidgetToken("dev-1"); err != nil {
		t.Fatal(err)
	}
	device, ok, err := s.GetDevice("dev-1")
	if err != nil || !ok || device.APNsToken != "apns" || device.WidgetToken != "" {
		t.Fatalf("unexpected device after widget clear: %+v ok=%v err=%v", device, ok, err)
	}
	if err := s.ClearAPNsToken("dev-1"); err != nil {
		t.Fatal(err)
	}
	device, _, _ = s.GetDevice("dev-1")
	if device.APNsToken != "" {
		t.Fatalf("APNs token was not cleared: %+v", device)
	}
}

func TestStoreRetainsBoundedPollHistory(t *testing.T) {
	s := openTestStore(t)
	for i := 0; i < 55; i++ {
		if err := s.SavePollOutcome(PollResult{
			PolledAt: time.Date(2026, 7, 18, 0, i, 0, 0, time.UTC),
			Success:  i%2 == 0, SnapshotChanged: i == 54, DurationMS: int64(i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	results, err := s.RecentPollOutcomes(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 50 || results[0].DurationMS != 5 || results[49].DurationMS != 54 {
		t.Fatalf("unexpected retained poll history: first=%+v last=%+v count=%d", results[0], results[len(results)-1], len(results))
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
