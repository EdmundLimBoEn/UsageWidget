package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func saveAlertSnapshot(t *testing.T, store *Store, provider Provider) {
	t.Helper()
	payload, err := json.Marshal(Snapshot{Providers: []Provider{provider}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSnapshot(time.Now(), payload); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredAlertsRemovedWhileProviderStale(t *testing.T) {
	for _, typ := range []string{"early_threshold", "danger_threshold", "danger_reminder"} {
		t.Run(typ, func(t *testing.T) {
			store := openTestStore(t)
			if err := store.UpsertDevice("retry-phone", "retry-phone", ""); err != nil {
				t.Fatal(err)
			}
			settings, err := loadSettings(store)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			reset := now.Add(time.Minute)
			provider := Provider{ID: "codex", Stale: true}
			w := Window{ID: "codex.session", UsedPercent: 95, RemainingPercent: 5, ResetsAt: &reset}
			provider.Windows = []Window{w}
			for i := 0; i < pendingAlertBatchSize+1; i++ {
				if err := store.enqueueAlert(mkEvent(typ, "fixture", fmt.Sprint(i), provider, w)); err != nil {
					t.Fatal(err)
				}
			}
			// The queue is younger than retention, but every reset has passed.
			if _, err := NewEventEngine(store).Process(Snapshot{Providers: []Provider{provider}}, settings, reset); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := store.db.QueryRow("SELECT count(*) FROM pending_alerts").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("expired alerts retained for stale provider: %d", count)
			}
		})
	}
}

func TestDispatchDefersUnavailableProviderThenValidatesRecovery(t *testing.T) {
	for _, state := range []string{"stale", "error", "absent"} {
		for _, recovery := range []string{"valid", "epoch", "usage", "missing-window"} {
			t.Run(state+"/"+recovery, func(t *testing.T) {
				store := openTestStore(t)
				if err := store.UpsertDevice("ok-phone", "ok-phone", ""); err != nil {
					t.Fatal(err)
				}
				reset := time.Now().Add(time.Hour)
				w := Window{ID: "codex.session", UsedPercent: 95, RemainingPercent: 5, ResetsAt: &reset}
				provider := Provider{ID: "codex", Windows: []Window{w}}
				if err := store.enqueueAlert(mkEvent("danger_threshold", "fixture", "queued", provider, w)); err != nil {
					t.Fatal(err)
				}
				switch state {
				case "stale":
					provider.Stale = true
				case "error":
					provider.Error = "collection failed"
				case "absent":
					provider.ID = "other"
				}
				saveAlertSnapshot(t, store, provider)
				n := &retryAlertNotifier{attempts: map[string]int{}}
				poller := NewPoller(store, nil, n, nil)
				result := poller.dispatch(context.Background(), false)
				pending, err := store.pendingAlerts()
				if err != nil || len(pending) != 1 || result.Delivery.Alerts.Attempted != 0 {
					t.Fatalf("did not defer: %+v, pending=%d err=%v", result, len(pending), err)
				}
				provider.ID, provider.Stale, provider.Error = "codex", false, ""
				switch recovery {
				case "epoch":
					next := reset.Add(time.Hour)
					provider.Windows[0].ResetsAt = &next
				case "usage":
					provider.Windows[0].UsedPercent, provider.Windows[0].RemainingPercent = 5, 95
				case "missing-window":
					provider.Windows = nil
				}
				saveAlertSnapshot(t, store, provider)
				result = poller.dispatch(context.Background(), false)
				want := 0
				if recovery == "valid" {
					want = 1
				}
				if result.Delivery.Alerts.Succeeded != want {
					t.Fatalf("recovery dispatch: %+v, want %d sends", result, want)
				}
				pending, err = store.pendingAlerts()
				if err != nil || len(pending) != 0 {
					t.Fatalf("queue after recovery=%d err=%v", len(pending), err)
				}
			})
		}
	}
}

func TestPendingSelectionExpiresResetButPreservesOtherAlerts(t *testing.T) {
	store := openTestStore(t)
	if err := store.UpsertDevice("phone", "fixture", ""); err != nil {
		t.Fatal(err)
	}
	reset := time.Now().Add(-time.Minute)
	for _, typ := range []string{"early_threshold", "danger_threshold", "danger_reminder", "reset", "tibo_reset", "credits_increase"} {
		if err := store.enqueueAlert(Event{Key: typ, Type: typ, ResetsAt: &reset}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.enqueueAlert(Event{Key: "unknown-reset", Type: "danger_threshold"}); err != nil {
		t.Fatal(err)
	}
	pending, err := store.pendingAlerts()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 4 {
		t.Fatalf("pending=%d, want reset/credit events and unknown reset", len(pending))
	}
	for _, alert := range pending {
		if alert.Event.Key == "early_threshold" || alert.Event.Key == "danger_threshold" || alert.Event.Key == "danger_reminder" {
			t.Fatal("expired threshold selected")
		}
	}
}

func TestDispatchRechecksResetAfterEarlierSend(t *testing.T) {
	store := openTestStore(t)
	if err := store.UpsertDevice("phone", "fixture", ""); err != nil {
		t.Fatal(err)
	}
	reset := time.Now().Add(250 * time.Millisecond)
	if err := store.enqueueAlert(Event{Key: "first", Type: "credits_increase", ProviderID: "codex"}); err != nil {
		t.Fatal(err)
	}
	if err := store.enqueueAlert(Event{Key: "expires-during-dispatch", Type: "danger_threshold", ProviderID: "codex", ResetsAt: &reset}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	n := terminalFailureNotifier{fail: func(string) error {
		calls++
		time.Sleep(time.Until(reset.Add(time.Millisecond)))
		return nil
	}}
	result := NewPoller(store, nil, n, nil).dispatch(context.Background(), false)
	if calls != 1 || result.Delivery.Alerts.Attempted != 1 {
		t.Fatalf("expired alert sent after earlier send: calls=%d result=%+v", calls, result)
	}
	pending, err := store.pendingAlerts()
	if err != nil || len(pending) != 0 {
		t.Fatalf("queue after dispatch=%d err=%v", len(pending), err)
	}
}
