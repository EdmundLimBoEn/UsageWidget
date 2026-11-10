package server

import (
	"testing"
	"time"
)

func TestDeviceRegistrationWaitsForSnapshotTransaction(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		name := "new device"
		if refresh {
			name = "token refresh"
		}
		t.Run(name, func(t *testing.T) {
			s := openTestStore(t)
			if refresh {
				if err := s.UpsertDevice("phone", "old-alert", "old-widget"); err != nil {
					t.Fatal(err)
				}
			}

			tx, err := s.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec(`INSERT INTO snapshots(fetched_at, payload) VALUES(?, ?)`,
				time.Now().UTC().Format(time.RFC3339), `{}`); err != nil {
				t.Fatal(err)
			}

			waits := s.db.Stats().WaitCount
			done := make(chan error, 1)
			go func() {
				done <- s.UpsertDevice("phone", "new-alert", "new-widget")
			}()

			// Wait for the registration to request a connection, so a delayed
			// goroutine cannot make the test pass without actually contending.
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(time.Millisecond)
			defer tick.Stop()
			for s.db.Stats().WaitCount == waits {
				select {
				case err := <-done:
					t.Fatalf("registration returned before snapshot commit: %v", err)
				case <-deadline.C:
					t.Fatal("registration did not wait for the snapshot transaction")
				case <-tick.C:
				}
			}
			select {
			case err := <-done:
				t.Fatalf("registration returned before snapshot commit: %v", err)
			default:
			}

			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("registration after snapshot commit: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("registration did not finish after snapshot commit")
			}
			device, ok, err := s.GetDevice("phone")
			if err != nil || !ok {
				t.Fatalf("registered device: found=%v err=%v", ok, err)
			}
			if device.APNsToken != "new-alert" || device.WidgetToken != "new-widget" {
				t.Fatal("registration did not persist the new tokens")
			}
		})
	}
}
