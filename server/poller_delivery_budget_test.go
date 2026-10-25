package server

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"
	"time"
)

type budgetNotifier struct {
	alertKeys       []string
	widgetRemaining time.Duration
}

func (n *budgetNotifier) SendAlert(ctx context.Context, _ string, event Event) error {
	n.alertKeys = append(n.alertKeys, event.Key)
	<-ctx.Done()
	return ctx.Err()
}

func (n *budgetNotifier) SendWidgetRefresh(ctx context.Context, _ string) error {
	deadline, _ := ctx.Deadline()
	n.widgetRemaining = time.Until(deadline)
	// A real request must still have time to finish after the alert times out.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Second):
		return nil
	}
}

func TestDispatchReservesWidgetBudgetAndPreservesUnattemptedAlerts(t *testing.T) {
	for _, remaining := range []time.Duration{sharedPollTimeout, 51 * time.Second, 20 * time.Second, 0} {
		t.Run(remaining.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				store := openTestStore(t)
				if err := store.UpsertDevice("phone", "alert", "widget"); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 3; i++ {
					if err := store.enqueueAlert(Event{Key: fmt.Sprint(i), Type: "credits_increase", ProviderID: "codex"}); err != nil {
						t.Fatal(err)
					}
				}
				rowIDs := func() map[string]int64 {
					t.Helper()
					rows, err := store.db.Query("SELECT event_key, rowid FROM pending_alerts")
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					ids := map[string]int64{}
					for rows.Next() {
						var key string
						var id int64
						if err := rows.Scan(&key, &id); err != nil {
							t.Fatal(err)
						}
						ids[key] = id
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					return ids
				}
				before := rowIDs()
				ctx, cancel := context.WithTimeout(context.Background(), remaining)
				defer cancel()
				n := &budgetNotifier{}
				poller := NewPoller(store, nil, n, nil)
				result := poller.dispatch(ctx, true)
				wantAttempts := 0
				if remaining > (30 * time.Second) {
					wantAttempts = 1
				}
				if result.Delivery.Alerts.Attempted != wantAttempts || result.Delivery.Alerts.Failed != wantAttempts || len(n.alertKeys) != wantAttempts {
					t.Fatalf("alert attempts: %+v, calls=%v", result, n.alertKeys)
				}
				if remaining > 0 {
					wantRemaining := min(remaining, (30 * time.Second))
					if result.Delivery.WidgetRefresh.Succeeded != 1 || n.widgetRemaining != wantRemaining {
						t.Fatalf("widget lost reserved time: %+v, remaining=%v want=%v", result, n.widgetRemaining, wantRemaining)
					}
				} else if result.Delivery.WidgetRefresh.Attempted != 0 {
					t.Fatalf("attempted widget after cancellation: %+v", result)
				}
				after := rowIDs()
				if len(after) != len(before) {
					t.Fatalf("lost queued alerts: %v", after)
				}
				for key, id := range before {
					if wantAttempts == 1 && key == n.alertKeys[0] {
						if after[key] <= id {
							t.Fatal("attempted failure was not rotated")
						}
					} else if after[key] != id {
						t.Fatalf("unattempted alert %s was rotated", key)
					}
				}
				// All deferred records remain deliverable on a later poll.
				poller.notifier = &retryAlertNotifier{attempts: map[string]int{}}
				retry := poller.dispatch(context.Background(), false)
				if retry.Delivery.Alerts.Succeeded != 3 {
					t.Fatalf("retry lost alerts: %+v", retry)
				}
			})
		})
	}
}

type fairWidgetNotifier struct {
	send func(context.Context, string) error
}

func (n fairWidgetNotifier) SendAlert(context.Context, string, Event) error { return nil }

func (n fairWidgetNotifier) SendWidgetRefresh(ctx context.Context, token string) error {
	return n.send(ctx, token)
}

func TestDispatchSharesWidgetBudgetWithLaterDevices(t *testing.T) {
	for _, remaining := range []time.Duration{sharedPollTimeout, 20 * time.Second} {
		for _, allSlow := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/allSlow=%t", remaining, allSlow), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					store := openTestStore(t)
					for i := 0; i < 3; i++ {
						if err := store.UpsertDevice(fmt.Sprint(i), "", fmt.Sprint(i)); err != nil {
							t.Fatal(err)
						}
					}
					if err := store.UpsertDevice("no-widget", "alert-only", ""); err != nil {
						t.Fatal(err)
					}
					calls := 0
					n := fairWidgetNotifier{send: func(ctx context.Context, token string) error {
						calls++
						if token == "" || ctx.Err() != nil {
							t.Fatalf("invalid widget attempt: token=%q err=%v", token, ctx.Err())
						}
						if calls < 3 || allSlow {
							select {
							case <-ctx.Done():
								return ctx.Err()
							case <-time.After(15 * time.Second):
								return context.DeadlineExceeded
							}
						}
						select {
						case <-ctx.Done():
							return ctx.Err()
						case <-time.After(time.Second):
							return nil
						}
					}}
					ctx, cancel := context.WithTimeout(context.Background(), remaining)
					defer cancel()
					started := time.Now()
					result := NewPoller(store, nil, n, nil).dispatch(ctx, true)
					want := DeliveryCount{Attempted: 3, Failed: 2, Succeeded: 1}
					if allSlow {
						want = DeliveryCount{Attempted: 3, Failed: 3}
					}
					if calls != 3 || result.Delivery.WidgetRefresh != want {
						t.Fatalf("later widget starved: calls=%d delivery=%+v want=%+v", calls, result, want)
					}
					if elapsed := time.Since(started); elapsed > min(remaining, widgetDeliveryBudget) {
						t.Fatalf("widget batch exceeded budget: %v", elapsed)
					}
				})
			})
		}
	}
}
