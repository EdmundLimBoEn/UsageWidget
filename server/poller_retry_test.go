package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

type retryAlertNotifier struct {
	attempts map[string]int
	fail     error
	events   []Event
}

func (n *retryAlertNotifier) SendAlert(_ context.Context, token string, event Event) error {
	n.attempts[token]++
	n.events = append(n.events, event)
	if token == "retry-phone" {
		return n.fail
	}
	return nil
}
func (*retryAlertNotifier) SendWidgetRefresh(context.Context, string) error { return nil }

func TestPollerRetriesOneShotAlertsAfterRestart(t *testing.T) {
	for _, used := range []int{42, 95} {
		for _, failure := range []error{&APNSError{Status: 503, Reason: "ServiceUnavailable"}, &APNSError{Status: 429, Reason: "TooManyRequests"}, context.DeadlineExceeded} {
			t.Run(fmt.Sprintf("usage-%d/%v", used, failure), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "alerts.db")
				store, err := OpenStore(path)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { store.Close() }()
				for _, id := range []string{"retry-phone", "ok-phone"} {
					if err := store.UpsertDevice(id, id, ""); err != nil {
						t.Fatal(err)
					}
				}
				// 42 crosses only early; 95 crosses only danger, whose repeat interval defaults to zero.
				baseline := 5.0
				if used == 95 {
					baseline = 50
				}
				seedWindow(t, store, "codex.session", baseline, nil)
				client := NewCrossUsageHTTPClient("http://unused")
				client.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return testHTTPResponse(200, fmt.Sprintf(`{"schema":"crossusage.limits.v1","providers":{"codex":{"resources":{"session":{"unit":"percent","used":%d,"limit":100,"utilization":%f,"label":"Session","windowSeconds":18000}}}}}`, used, float64(used)/100)), nil
				})}
				notifier := &retryAlertNotifier{attempts: map[string]int{}, fail: failure}
				poller := NewPoller(store, client, notifier, nil)
				if result := poller.PollNow(context.Background()); !result.Success || result.Events != 1 {
					t.Fatalf("crossing: %+v", result)
				}
				if notifier.attempts["ok-phone"] != 1 || notifier.attempts["retry-phone"] != 1 {
					t.Fatalf("initial attempts: %v", notifier.attempts)
				}
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				store, err = OpenStore(path)
				if err != nil {
					t.Fatal(err)
				}
				notifier.fail = nil
				poller = NewPoller(store, client, notifier, nil)
				for i := 0; i < 2; i++ {
					if result := poller.PollNow(context.Background()); !result.Success || result.Events != 0 {
						t.Fatalf("unchanged poll: %+v", result)
					}
				}
				if notifier.attempts["ok-phone"] != 1 || notifier.attempts["retry-phone"] != 2 {
					t.Fatalf("retry/acknowledgement attempts: %v", notifier.attempts)
				}
			})
		}
	}
}

func TestPendingAlertFailureRollsBackClaimAndBaseline(t *testing.T) {
	store := openTestStore(t)
	if err := store.UpsertDevice("phone", "fixture-token", ""); err != nil {
		t.Fatal(err)
	}
	seedWindow(t, store, "codex.session", 5, nil)
	snap := Snapshot{Providers: []Provider{{ID: "codex", Windows: []Window{{ID: "codex.session", UsedPercent: 42, RemainingPercent: 58}}}}}
	settings, err := loadSettings(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_pending BEFORE INSERT ON pending_alerts BEGIN SELECT RAISE(FAIL, 'queue unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	engine := NewEventEngine(store)
	if _, err := engine.ProcessDetailed(snap, settings, time.Now()); err == nil {
		t.Fatal("expected queue failure")
	}
	state, _, err := store.GetWindowState("codex.session")
	if err != nil || state.UsedPercent != 5 {
		t.Fatalf("baseline advanced: %+v, %v", state, err)
	}
	claimed, err := store.EventNotified(eventKey("early", "codex.session", nil))
	if err != nil || claimed {
		t.Fatalf("claim survived: %v, %v", claimed, err)
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_pending`); err != nil {
		t.Fatal(err)
	}
	result, err := engine.ProcessDetailed(snap, settings, time.Now())
	if err != nil || len(result.Emitted) != 1 {
		t.Fatalf("crossing lost: %+v, %v", result, err)
	}
	pending, err := store.pendingAlerts()
	if err != nil || len(pending) != 1 {
		t.Fatalf("delivery not queued: %d, %v", len(pending), err)
	}
}

func TestDeleteDeviceDiscardsPendingAlerts(t *testing.T) {
	store := openTestStore(t)
	if err := store.UpsertDevice("phone", "fixture-token", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.enqueueAlert(Event{Key: "pending"}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteDevice("phone"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertDevice("phone", "replacement-token", ""); err != nil {
		t.Fatal(err)
	}
	pending, err := store.pendingAlerts()
	if err != nil || len(pending) != 0 {
		t.Fatalf("deleted device inherited pending alerts: count=%d err=%v", len(pending), err)
	}
}

func TestPendingDangerAlertsSuppressReminderBacklog(t *testing.T) {
	for _, baseline := range []float64{50, 95} {
		t.Run(fmt.Sprint(baseline), func(t *testing.T) {
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
			seedWindow(t, store, "codex.session", baseline, nil)
			snap := Snapshot{Providers: []Provider{{ID: "codex", Windows: []Window{{ID: "codex.session", UsedPercent: 95, RemainingPercent: 5}}}}}
			engine := NewEventEngine(store)
			notifier := &retryAlertNotifier{attempts: map[string]int{}, fail: context.DeadlineExceeded}
			poller := NewPoller(store, nil, notifier, nil)
			now := time.Now()
			for i := 0; i < 4; i++ {
				result, err := engine.ProcessDetailed(snap, settings, now.Add(time.Duration(i)*time.Minute))
				want := 0
				if i == 0 {
					want = 1
				}
				if err != nil || len(result.Emitted) != want {
					t.Fatalf("poll %d: %+v, %v", i, result, err)
				}
				poller.dispatch(context.Background(), false)
				pending, err := store.pendingAlerts()
				if err != nil || len(pending) != 1 {
					t.Fatalf("pending=%v err=%v", pending, err)
				}
			}
			notifier.fail = nil
			result := poller.dispatch(context.Background(), false)
			if result.Delivery.Alerts.Succeeded != 1 {
				t.Fatalf("recovery: %+v", result)
			}
			due, err := store.DangerDeliveryDue("codex.session", "epoch", policyFingerprint(settings.GlobalRule()), time.Now().Add(59*time.Minute), time.Hour)
			if err != nil || due {
				t.Fatalf("repeated before interval: due=%v err=%v", due, err)
			}
			due, err = store.DangerDeliveryDue("codex.session", "epoch", policyFingerprint(settings.GlobalRule()), time.Now().Add(61*time.Minute), time.Hour)
			if err != nil || !due {
				t.Fatalf("not due after interval: due=%v err=%v", due, err)
			}
		})
	}
}

func TestPendingAlertsRespectCurrentPreferences(t *testing.T) {
	for _, mode := range []string{"notifications", "provider rule", "window rule", "hidden provider", "repeat disabled", "enter quiet hours", "leave quiet hours"} {
		t.Run(mode, func(t *testing.T) {
			store := openTestStore(t)
			if err := store.UpsertDevice("retry-phone", "retry-phone", ""); err != nil {
				t.Fatal(err)
			}
			event := Event{Key: "queued", Type: "early_threshold", ProviderID: "codex", WindowID: "codex.session"}
			if mode == "repeat disabled" {
				event.Type = "danger_reminder"
				if err := store.SetSetting("default_repeat_interval_minutes", "60"); err != nil {
					t.Fatal(err)
				}
			}
			setQuiet := func(enabled bool) {
				// All-day quiet hours avoid dependence on the test's wall-clock time.
				payload, err := json.Marshal(QuietHours{Enabled: enabled, TimeZone: "UTC", StartMinute: 0, EndMinute: 0})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.SetSetting("quiet_hours", string(payload)); err != nil {
					t.Fatal(err)
				}
			}
			setQuiet(mode == "leave quiet hours")
			event.Silent = mode == "leave quiet hours"
			if err := store.enqueueAlert(event); err != nil {
				t.Fatal(err)
			}
			notifier := &retryAlertNotifier{attempts: map[string]int{}, fail: context.DeadlineExceeded}
			poller := NewPoller(store, nil, notifier, nil)
			poller.dispatch(context.Background(), false)
			if notifier.attempts["retry-phone"] != 1 {
				t.Fatal("initial delivery not attempted")
			}
			var err error
			switch mode {
			case "notifications":
				err = store.SetSetting("notifications_enabled", "false")
			case "provider rule", "window rule":
				override := AlertOverride{ProviderID: "codex", Rule: AlertRule{Enabled: false}}
				if mode == "window rule" {
					override.WindowID = &event.WindowID
				}
				err = store.ReplaceAlertOverrides([]AlertOverride{override})
			case "hidden provider":
				err = store.SetSetting("hidden_providers", `["codex"]`)
			case "repeat disabled":
				err = store.SetSetting("default_repeat_interval_minutes", "0")
			case "enter quiet hours":
				setQuiet(true)
			case "leave quiet hours":
				setQuiet(false)
			}
			if err != nil {
				t.Fatal(err)
			}
			notifier.fail = nil
			poller.dispatch(context.Background(), false)
			want := 1
			if mode == "enter quiet hours" || mode == "leave quiet hours" {
				want = 2
				if got := notifier.events[len(notifier.events)-1].Silent; got != (mode == "enter quiet hours") {
					t.Fatalf("retry silent=%v", got)
				}
			}
			if notifier.attempts["retry-phone"] != want {
				t.Fatalf("attempts=%v", notifier.attempts)
			}
			pending, err := store.pendingAlerts()
			if err != nil || len(pending) != 0 {
				t.Fatalf("pending=%v err=%v", pending, err)
			}
		})
	}
}
