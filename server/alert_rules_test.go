package server

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAlertRuleInheritance(t *testing.T) {
	wid := "codex.primary"
	s := Settings{NotificationsEnabled: true, EarlyThresholdPct: 10, DangerThresholdPct: 10, DefaultRepeatIntervalMinutes: 0, AlertOverrides: []AlertOverride{
		{ProviderID: "codex", Rule: AlertRule{Enabled: true, EarlyThresholdPct: 20, DangerThresholdPct: 15, RepeatIntervalMinutes: 60}},
		{ProviderID: "codex", WindowID: &wid, Rule: AlertRule{Enabled: false, EarlyThresholdPct: 30, DangerThresholdPct: 5, RepeatIntervalMinutes: 180}},
	}}
	if got := s.EffectiveRule("codex", "codex.secondary"); got.EarlyThresholdPct != 20 {
		t.Fatalf("provider rule=%+v", got)
	}
	if got := s.EffectiveRule("codex", wid); got.Enabled || got.RepeatIntervalMinutes != 180 {
		t.Fatalf("window rule=%+v", got)
	}
	if got := s.EffectiveRule("claude", "claude.primary"); got.EarlyThresholdPct != 10 {
		t.Fatalf("global rule=%+v", got)
	}
}

func TestQuietHoursOvernightAndSameDay(t *testing.T) {
	q := QuietHours{Enabled: true, StartMinute: 22 * 60, EndMinute: 7 * 60, TimeZone: "Asia/Singapore"}
	if !q.Contains(time.Date(2026, 7, 19, 15, 0, 0, 0, time.UTC)) {
		t.Fatal("23:00 should be quiet")
	}
	if q.Contains(time.Date(2026, 7, 19, 5, 0, 0, 0, time.UTC)) {
		t.Fatal("13:00 should not be quiet")
	}
	q.StartMinute = 9 * 60
	q.EndMinute = 17 * 60
	if !q.Contains(time.Date(2026, 7, 19, 4, 0, 0, 0, time.UTC)) {
		t.Fatal("12:00 should be quiet")
	}
}

func TestQuietAlertPayloadHasNoSound(t *testing.T) {
	payload := alertPayload(Event{Silent: true, Title: "Automatic"})
	aps := payload["aps"].(map[string]any)
	if _, ok := aps["sound"]; ok {
		t.Fatal("silent alert contains sound")
	}
	if aps["interruption-level"] != "passive" {
		t.Fatalf("level=%v", aps["interruption-level"])
	}
	audible := alertPayload(Event{Title: "Test"})["aps"].(map[string]any)
	if audible["sound"] != "default" {
		t.Fatal("test is not audible")
	}
}

func TestDangerReminderStateSurvivesRestartAndPolicyChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usagewidget.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	due, err := store.DangerDeliveryDue("codex.primary", "epoch-1", "policy-a", now, time.Hour)
	if err != nil || !due {
		t.Fatalf("initial due=%v err=%v", due, err)
	}
	if err := store.RecordDangerDelivery("codex.primary", "epoch-1", "policy-a", now); err != nil {
		t.Fatal(err)
	}
	store.Close()
	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	due, _ = store.DangerDeliveryDue("codex.primary", "epoch-1", "policy-a", now.Add(59*time.Minute), time.Hour)
	if due {
		t.Fatal("reminder repeated early")
	}
	due, _ = store.DangerDeliveryDue("codex.primary", "epoch-1", "policy-a", now.Add(time.Hour), time.Hour)
	if !due {
		t.Fatal("reminder not due at cadence")
	}
	due, _ = store.DangerDeliveryDue("codex.primary", "epoch-1", "policy-b", now.Add(time.Minute), time.Hour)
	if !due {
		t.Fatal("policy edit did not establish new cadence")
	}
}
