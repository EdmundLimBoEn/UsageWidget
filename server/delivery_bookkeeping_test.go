package server

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSuccessfulDeliveryUsesCurrentRegistration(t *testing.T) {
	for _, change := range []string{"unchanged", "refreshed", "widget-updated", "deleted", "reregistered", "rotated"} {
		t.Run(change, func(t *testing.T) {
			store := openTestStore(t)
			if err := store.UpsertDevice("phone", "old-alert", ""); err != nil {
				t.Fatal(err)
			}
			event := Event{Key: "danger", Type: "danger_threshold", ProviderID: "codex", WindowID: "codex.session", DangerResetEpoch: "epoch", DangerPolicyFingerprint: "policy"}
			if err := store.enqueueAlert(event); err != nil {
				t.Fatal(err)
			}
			notifier := terminalFailureNotifier{fail: func(string) error {
				switch change {
				case "deleted", "reregistered":
					if err := store.DeleteDevice("phone"); err != nil {
						t.Fatal(err)
					}
					if change == "reregistered" {
						// Even the same ID, token and event belong to a new registration.
						if err := store.UpsertDevice("phone", "old-alert", ""); err != nil {
							t.Fatal(err)
						}
						if err := store.enqueueAlert(event); err != nil {
							t.Fatal(err)
						}
					}
				case "refreshed":
					if err := store.UpsertDevice("phone", "old-alert", ""); err != nil {
						t.Fatal(err)
					}
				case "widget-updated":
					widget := "new-widget"
					if err := store.upsertDevice("phone", nil, &widget); err != nil {
						t.Fatal(err)
					}
				case "rotated":
					if err := store.UpsertDevice("phone", "new-alert", ""); err != nil {
						t.Fatal(err)
					}
				}
				return nil
			}}
			result := NewPoller(store, nil, notifier, nil).dispatch(context.Background(), false)
			if result.Delivery.Alerts.Succeeded != 1 {
				t.Fatalf("delivery=%+v", result)
			}
			wantCadence, wantPending := 0, 0
			if change == "unchanged" || change == "refreshed" || change == "widget-updated" {
				wantCadence = 1
			}
			if change == "reregistered" || change == "rotated" {
				wantPending = 1
			}
			assertDeliveryRows(t, store, wantCadence, wantPending)
			if wantPending == 1 {
				NewPoller(store, nil, terminalFailureNotifier{fail: func(string) error { return nil }}, nil).dispatch(context.Background(), false)
				assertDeliveryRows(t, store, 1, 0)
			}
		})
	}
}

func TestSuccessfulDeliveryBookkeepingRollsBack(t *testing.T) {
	for _, failure := range []string{"cadence", "acknowledgment"} {
		t.Run(failure, func(t *testing.T) {
			store := openTestStore(t)
			if err := store.UpsertDevice("phone", "alert", ""); err != nil {
				t.Fatal(err)
			}
			if err := store.enqueueAlert(Event{Key: "danger", ProviderID: "codex", WindowID: "codex.session", DangerPolicyFingerprint: "policy"}); err != nil {
				t.Fatal(err)
			}
			statement := `CREATE TRIGGER fail_delivery BEFORE INSERT ON alert_delivery_state BEGIN SELECT RAISE(ABORT, 'test failure'); END`
			if failure == "acknowledgment" {
				statement = `CREATE TRIGGER fail_delivery BEFORE DELETE ON pending_alerts BEGIN SELECT RAISE(ABORT, 'test failure'); END`
			}
			if _, err := store.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			poller := NewPoller(store, nil, terminalFailureNotifier{fail: func(string) error { return nil }}, nil)
			poller.dispatch(context.Background(), false)
			assertDeliveryRows(t, store, 0, 1)
			if _, err := store.db.Exec(`DROP TRIGGER fail_delivery`); err != nil {
				t.Fatal(err)
			}
			poller.dispatch(context.Background(), false)
			assertDeliveryRows(t, store, 1, 0)
		})
	}
}

func assertDeliveryRows(t *testing.T, store *Store, wantCadence, wantPending int) {
	t.Helper()
	var cadence, pending int
	if err := store.db.QueryRow(`SELECT count(*) FROM alert_delivery_state`).Scan(&cadence); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT count(*) FROM pending_alerts`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if cadence != wantCadence || pending != wantPending {
		t.Fatalf("cadence=%d pending=%d; want cadence=%d pending=%d", cadence, pending, wantCadence, wantPending)
	}
}

func TestRegistrationGenerationMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertDevice("phone", "alert", "widget"); err != nil {
		t.Fatal(err)
	}
	// Restore the version 2 device schema to exercise upgrading an existing device.
	if _, err := store.db.Exec(`ALTER TABLE devices DROP COLUMN registration_generation;
 DELETE FROM schema_migrations;
 INSERT INTO schema_migrations VALUES(2, '2026-01-01T00:00:00Z');`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var previous string
	for i := 0; i < 2; i++ {
		store, err := OpenStore(path)
		if err != nil {
			t.Fatal(err)
		}
		var generation, token, widget string
		if err := store.db.QueryRow(`SELECT registration_generation, apns_token, widget_token FROM devices WHERE device_id='phone'`).Scan(&generation, &token, &widget); err != nil {
			t.Fatal(err)
		}
		if generation == "" || (i > 0 && generation != previous) || token != "alert" || widget != "widget" {
			t.Fatalf("migration/reopen changed device identity or tokens: generation=%q previous=%q token=%q widget=%q", generation, previous, token, widget)
		}
		previous = generation
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
