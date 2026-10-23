package server

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

type terminalFailureNotifier struct {
	fail func(string) error
}

func (n terminalFailureNotifier) SendAlert(_ context.Context, token string, _ Event) error {
	return n.fail(token)
}
func (n terminalFailureNotifier) SendWidgetRefresh(_ context.Context, token string) error {
	return n.fail(token)
}

func TestDispatchTerminalFailurePreservesRotatedToken(t *testing.T) {
	for _, widget := range []bool{false, true} {
		name := "alert"
		if widget {
			name = "widget"
		}
		t.Run(name, func(t *testing.T) {
			for _, rotate := range []bool{false, true} {
				name := "unchanged"
				if rotate {
					name = "rotated"
				}
				t.Run(name, func(t *testing.T) {
					store := openTestStore(t)
					if err := store.UpsertDevice("phone", "old-alert", "old-widget"); err != nil {
						t.Fatal(err)
					}
					notifier := terminalFailureNotifier{fail: func(token string) error {
						want := "old-alert"
						if widget {
							want = "old-widget"
						}
						if token != want {
							t.Errorf("sent token=%q want=%q", token, want)
						}
						// Registration completes while the old token's APNs response is in flight.
						if rotate {
							if err := store.UpsertDevice("phone", "new-alert", "new-widget"); err != nil {
								t.Fatal(err)
							}
						}
						return &APNSError{Reason: "Unregistered"}
					}}
					// Verify cleanup after the delayed response.
					poller := NewPoller(store, nil, notifier, nil)
					if !widget {
						if err := store.enqueueAlert(Event{Key: "test"}); err != nil {
							t.Fatal(err)
						}
					}
					result := poller.dispatch(context.Background(), widget)
					if result.Status != DeliveryWarning {
						t.Fatalf("delivery=%+v", result)
					}
					var pending int
					if err := store.db.QueryRow(`SELECT count(*) FROM pending_alerts`).Scan(&pending); err != nil || pending != 0 {
						t.Fatalf("terminal delivery remains queued: count=%d err=%v", pending, err)
					}
					got, ok, err := store.GetDevice("phone")
					wantAlert, wantWidget := "old-alert", "old-widget"
					if widget {
						wantWidget = ""
					} else {
						wantAlert = ""
					}
					if rotate {
						wantAlert, wantWidget = "new-alert", "new-widget"
					}
					if err != nil || !ok || got.APNsToken != wantAlert || got.WidgetToken != wantWidget {
						t.Fatalf("phone=%+v found=%v err=%v; want alert=%q widget=%q", got, ok, err, wantAlert, wantWidget)
					}
				})
			}
		})
	}
}

func TestSharedPollAllowsFullCollectionBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := openTestStore(t)
		source := &contextBlockingSource{started: make(chan context.Context, 1), release: make(chan struct{})}
		poller := NewPoller(store, source, noopNotifier{}, nil)
		done := make(chan PollResult, 1)
		go func() { done <- poller.PollNow(context.Background()) }()
		ctx := <-source.started
		// A collection can consume almost the entire upstream request budget.
		time.Sleep(crossUsageFetchTimeout - time.Second)
		if err := ctx.Err(); err != nil {
			t.Errorf("shared poll canceled before collection completed: %v", err)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 50*time.Second || time.Until(deadline) > time.Minute {
			t.Errorf("expected bounded dispatch headroom, remaining=%v hasDeadline=%v", time.Until(deadline), ok)
		}
		close(source.release)
		if result := <-done; !result.Success {
			t.Errorf("long collection failed: %+v", result)
		}
	})
}
