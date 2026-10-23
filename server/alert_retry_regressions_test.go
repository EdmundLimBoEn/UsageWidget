package server

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestFailedDeviceDoesNotSuppressOtherDevicesReminders(t *testing.T) {
	store := openTestStore(t)
	for _, id := range []string{"ok-phone", "retry-phone"} {
		if err := store.UpsertDevice(id, id, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetSetting("default_repeat_interval_minutes", "60"); err != nil {
		t.Fatal(err)
	}
	settings, err := loadSettings(store)
	if err != nil {
		t.Fatal(err)
	}
	seedWindow(t, store, "codex.session", 50, nil)
	snap := Snapshot{Providers: []Provider{{ID: "codex", Windows: []Window{{ID: "codex.session", UsedPercent: 95, RemainingPercent: 5}}}}}
	engine := NewEventEngine(store)
	now := time.Now()
	if _, err := engine.Process(snap, settings, now); err != nil {
		t.Fatal(err)
	}
	n := &retryAlertNotifier{attempts: map[string]int{}, fail: &APNSError{Status: 429, Reason: "TooManyRequests"}}
	poller := NewPoller(store, nil, n, nil)
	poller.dispatch(context.Background(), false)
	if _, err := engine.Process(snap, settings, now.Add(61*time.Minute)); err != nil {
		t.Fatal(err)
	}
	pending, err := store.pendingAlerts()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, a := range pending {
		counts[a.DeviceID]++
	}
	if counts["ok-phone"] != 1 || counts["retry-phone"] != 1 {
		t.Fatalf("pending per device: %v", counts)
	}
	poller.dispatch(context.Background(), false)
	if n.attempts["ok-phone"] != 2 || n.attempts["retry-phone"] != 2 {
		t.Fatalf("attempts: %v", n.attempts)
	}
	// A newly successful retry starts its own full interval.
	n.fail = nil
	poller.dispatch(context.Background(), false)
	if events, err := engine.Process(snap, settings, time.Now().Add(59*time.Minute)); err != nil || len(events) != 0 {
		t.Fatalf("premature reminders: %d, %v", len(events), err)
	}
}

func TestQueuedThresholdsExpireWithWindowOrCondition(t *testing.T) {
	for _, typ := range []string{"early_threshold", "danger_threshold", "danger_reminder"} {
		for _, change := range []string{"epoch", "usage"} {
			t.Run(typ+"/"+change, func(t *testing.T) {
				store := openTestStore(t)
				if err := store.UpsertDevice("retry-phone", "retry-phone", ""); err != nil {
					t.Fatal(err)
				}
				if err := store.SetSetting("default_repeat_interval_minutes", "60"); err != nil {
					t.Fatal(err)
				}
				settings, err := loadSettings(store)
				if err != nil {
					t.Fatal(err)
				}
				reset := time.Now().Add(time.Hour)
				w := Window{ID: "codex.session", UsedPercent: 95, RemainingPercent: 5, ResetsAt: &reset}
				event := mkEvent(typ, "fixture", "obsolete", Provider{ID: "codex"}, w)
				// More than one batch proves invalidation covers the whole queue.
				for i := 0; i < pendingAlertBatchSize+1; i++ {
					event.Key = fmt.Sprintf("obsolete-%d", i)
					if err := store.enqueueAlert(event); err != nil {
						t.Fatal(err)
					}
				}
				n := &retryAlertNotifier{attempts: map[string]int{}, fail: context.DeadlineExceeded}
				poller := NewPoller(store, nil, n, nil)
				poller.dispatch(context.Background(), false)
				before := n.attempts["retry-phone"]
				if change == "epoch" {
					next := reset.Add(time.Hour)
					w.ResetsAt = &next
				} else {
					w.UsedPercent, w.RemainingPercent = 5, 95
				}
				// No baseline: only queued events are under test.
				if _, err := NewEventEngine(store).Process(Snapshot{Providers: []Provider{{ID: "codex", Windows: []Window{w}}}}, settings, time.Now()); err != nil {
					t.Fatal(err)
				}
				n.fail = nil
				poller.dispatch(context.Background(), false)
				if n.attempts["retry-phone"] != before {
					t.Fatal("obsolete usage replayed")
				}
				var count int
				if err := store.db.QueryRow("SELECT count(*) FROM pending_alerts").Scan(&count); err != nil || count != 0 {
					t.Fatalf("queue=%d err=%v", count, err)
				}
			})
		}
	}
}

func TestPendingAlertRetentionAndBoundedDispatch(t *testing.T) {
	store := openTestStore(t)
	if err := store.UpsertDevice("ok-phone", "ok-phone", "widget"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < pendingAlertBatchSize+2; i++ {
		if err := store.enqueueAlert(Event{Key: fmt.Sprint(i), Type: "credits_increase", ProviderID: "codex"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.Exec("UPDATE pending_alerts SET created_at = ? WHERE event_key = '0'", time.Now().Add(-pendingAlertRetention-time.Second).Unix()); err != nil {
		t.Fatal(err)
	}
	n := &retryAlertNotifier{attempts: map[string]int{}}
	poller := NewPoller(store, nil, n, nil)
	result := poller.dispatch(context.Background(), true)
	if n.attempts["ok-phone"] != pendingAlertBatchSize || result.Delivery.WidgetRefresh.Succeeded != 1 {
		t.Fatalf("unbounded dispatch or lost widget: %+v", result)
	}
	for _, event := range n.events {
		if event.Key == "0" {
			t.Fatal("expired event delivered")
		}
	}
	poller.dispatch(context.Background(), false)
	if n.attempts["ok-phone"] != pendingAlertBatchSize+1 {
		t.Fatalf("remaining batch not delivered: %v", n.attempts)
	}
}

func TestFailedAlertBatchDoesNotStarveLaterDeliveries(t *testing.T) {
	store := openTestStore(t)
	for _, id := range []string{"retry-phone", "ok-phone"} {
		if err := store.UpsertDevice(id, id, ""); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < pendingAlertBatchSize; i++ {
		if err := store.enqueueAlert(Event{Key: fmt.Sprint(i), Type: "credits_increase", ProviderID: "codex", recipientID: "retry-phone"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.enqueueAlert(Event{Key: "healthy", Type: "credits_increase", ProviderID: "codex", recipientID: "ok-phone"}); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().Add(-time.Hour).Unix()
	if _, err := store.db.Exec("UPDATE pending_alerts SET created_at = ?", createdAt); err != nil {
		t.Fatal(err)
	}
	n := &retryAlertNotifier{attempts: map[string]int{}, fail: &APNSError{Status: 503, Reason: "ServiceUnavailable"}}
	poller := NewPoller(store, nil, n, nil)
	first := poller.dispatch(context.Background(), false)
	if first.Delivery.Alerts.Attempted != pendingAlertBatchSize || first.Delivery.Alerts.Failed != pendingAlertBatchSize || n.attempts["ok-phone"] != 0 {
		t.Fatalf("initial batch: %+v, attempts: %v", first, n.attempts)
	}
	second := poller.dispatch(context.Background(), false)
	if second.Delivery.Alerts.Attempted != pendingAlertBatchSize || second.Delivery.Alerts.Succeeded != 1 || n.attempts["ok-phone"] != 1 {
		t.Fatalf("healthy delivery starved or batch unbounded: %+v, attempts: %v", second, n.attempts)
	}
	var count, oldest, newest int64
	if err := store.db.QueryRow("SELECT count(*), min(created_at), max(created_at) FROM pending_alerts").Scan(&count, &oldest, &newest); err != nil {
		t.Fatal(err)
	}
	if count != pendingAlertBatchSize || oldest != createdAt || newest != createdAt {
		t.Fatalf("retries lost or expiry changed: count=%d, created_at=%d..%d", count, oldest, newest)
	}
	// Rotation preserves every failed delivery for a later successful attempt.
	n.fail = nil
	last := poller.dispatch(context.Background(), false)
	if last.Delivery.Alerts.Succeeded != pendingAlertBatchSize || n.attempts["ok-phone"] != 1 {
		t.Fatalf("retry recovery: %+v, attempts: %v", last, n.attempts)
	}
	pending, err := store.pendingAlerts()
	if err != nil || len(pending) != 0 {
		t.Fatalf("queue after recovery: %d, %v", len(pending), err)
	}
}

func TestDelayedTerminalFailureRetriesReplacementToken(t *testing.T) {
	store := openTestStore(t)
	if err := store.UpsertDevice("phone", "old", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.enqueueAlert(Event{Key: "one-shot", Type: "credits_increase", ProviderID: "codex"}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	n := terminalFailureNotifier{fail: func(token string) error {
		calls++
		if calls == 1 {
			if token != "old" {
				t.Fatal("wrong initial token")
			}
			if err := store.UpsertDevice("phone", "replacement", ""); err != nil {
				t.Fatal(err)
			}
			return &APNSError{Reason: "Unregistered"}
		}
		if token != "replacement" {
			t.Fatal("did not retry replacement")
		}
		return nil
	}}
	poller := NewPoller(store, nil, n, nil)
	poller.dispatch(context.Background(), false)
	poller.dispatch(context.Background(), false)
	poller.dispatch(context.Background(), false)
	if calls != 2 {
		t.Fatalf("one-shot delivery calls=%d, want 2", calls)
	}
}

func TestAlertQueueUpgradePreservesCadenceAndExpiresUnknownAge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO schema_migrations VALUES(1, ?);
		INSERT INTO devices(device_id, apns_token, updated_at) VALUES('phone', 'fixture', ?);
		INSERT INTO alert_delivery_state VALUES('window', 'epoch', 'policy', ?);
		INSERT INTO pending_alerts VALUES('old', 'phone', '{"Key":"old"}')`,
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		store, err := OpenStore(path)
		if err != nil {
			t.Fatal(err)
		}
		due, err := store.DangerDeliveryDue("phone", "window", "epoch", "policy", now.Add(time.Minute), time.Hour)
		if err != nil || due {
			t.Fatalf("lost migrated cadence: due=%v err=%v", due, err)
		}
		alerts, err := store.pendingAlerts()
		if err != nil || len(alerts) != 0 {
			t.Fatalf("legacy queue replayed: count=%d err=%v", len(alerts), err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
