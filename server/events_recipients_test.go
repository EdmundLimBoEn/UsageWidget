package server

import (
	"database/sql/driver"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

func TestEventRecipientsLoadedOncePerTransaction(t *testing.T) {
	for _, alertToken := range []string{"test-alert-token", ""} {
		t.Run(fmt.Sprintf("hasAlertToken=%t", alertToken != ""), func(t *testing.T) {
			var reads atomic.Int64
			function := fmt.Sprintf("count_recipient_read_%d", time.Now().UnixNano())
			if err := sqlite.RegisterScalarFunction(function, 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
				reads.Add(1)
				return args[0], nil
			}); err != nil {
				t.Fatal(err)
			}
			s := openTestStore(t)
			if err := s.UpsertDevice("phone", alertToken, "test-widget-token"); err != nil {
				t.Fatal(err)
			}
			// Count actual device-list row reads. Other device lookups for cadence
			// and queueing do not select widget_token, so they are not counted.
			if _, err := s.db.Exec(`ALTER TABLE devices RENAME TO recipient_devices;
				CREATE VIEW devices AS SELECT device_id, apns_token, ` + function + `(widget_token) AS widget_token, updated_at FROM recipient_devices`); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			snap := oneWindowSnap("codex", "Codex", "primary", "Primary", 95, nil, now)
			snap.Providers = append(snap.Providers, oneWindowSnap("claude", "Claude", "primary", "Primary", 96, nil, now).Providers...)
			for _, p := range snap.Providers {
				for _, w := range p.Windows {
					seedWindow(t, s, w.ID, w.UsedPercent, nil)
				}
			}
			settings := testSettings()
			settings.DefaultRepeatIntervalMinutes = 60
			engine := NewEventEngine(s)
			for transaction := 0; transaction < 2; transaction++ {
				reads.Store(0)
				result, err := engine.ProcessDetailed(snap, settings, now.Add(time.Duration(transaction)*time.Minute))
				if err != nil {
					t.Fatal(err)
				}
				if got := reads.Load(); got != 1 {
					t.Errorf("transaction %d: device row reads = %d, want 1", transaction, got)
				}
				wantEvents := 0
				if transaction == 0 && alertToken != "" {
					wantEvents = 2
				}
				if len(result.Emitted) != wantEvents {
					t.Fatalf("transaction %d: emitted %d events, want %d", transaction, len(result.Emitted), wantEvents)
				}
				for i, event := range result.Emitted {
					if event.Type != "danger_reminder" || event.recipientID != "phone" || event.WindowID != snap.Providers[i].Windows[0].ID {
						t.Fatalf("unexpected reminder: %+v", event)
					}
				}
				// The next transaction must reload eligibility, even on this engine.
				if _, err := s.db.Exec(`UPDATE recipient_devices SET apns_token = ''`); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
