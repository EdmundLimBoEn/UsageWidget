package server

import (
	"context"
	"testing"
)

func TestFailedAlertRotationsRunAfterDeliveryAndCommitTogether(t *testing.T) {
	for _, abortRotation := range []bool{false, true} {
		name := "commit"
		if abortRotation {
			name = "rollback"
		}
		t.Run(name, func(t *testing.T) {
			store := openTestStore(t)
			if err := store.UpsertDevice("phone", "fixture", ""); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"first", "second"} {
				if err := store.enqueueAlert(Event{Key: key, Type: "credits_increase", ProviderID: "codex"}); err != nil {
					t.Fatal(err)
				}
			}
			rowID := func(key string) int64 {
				t.Helper()
				var id int64
				if err := store.db.QueryRow("SELECT rowid FROM pending_alerts WHERE event_key = ?", key).Scan(&id); err != nil {
					t.Fatal(err)
				}
				return id
			}
			first, second := rowID("first"), rowID("second")
			if abortRotation {
				if _, err := store.db.Exec(`CREATE TRIGGER fail_second_rotation BEFORE UPDATE ON pending_alerts
					WHEN OLD.event_key = 'second' BEGIN SELECT RAISE(ABORT, 'rotation failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			notifier := terminalFailureNotifier{fail: func(string) error {
				calls++
				// Reads and writes must remain possible during network delivery, and
				// no failed delivery should rotate until all sends have finished.
				if got := rowID("first"); got != first {
					t.Fatalf("queue rotated during network delivery: rowid=%d, want %d", got, first)
				}
				if err := store.UpsertDevice("other", "", ""); err != nil {
					t.Fatal(err)
				}
				return context.DeadlineExceeded
			}}
			result := NewPoller(store, nil, notifier, nil).dispatch(context.Background(), false)
			if calls != 2 || result.Delivery.Alerts.Failed != 2 {
				t.Fatalf("expected two failed sends: calls=%d, result=%+v", calls, result)
			}
			gotFirst, gotSecond := rowID("first"), rowID("second")
			if abortRotation {
				if gotFirst != first || gotSecond != second {
					t.Fatalf("partial rotation committed: rowids=%d,%d, want %d,%d", gotFirst, gotSecond, first, second)
				}
			} else if gotFirst <= second || gotSecond <= gotFirst {
				t.Fatalf("failed deliveries not rotated in order: rowids=%d,%d", gotFirst, gotSecond)
			}
		})
	}
}
