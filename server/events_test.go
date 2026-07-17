package server

import (
	"testing"
	"time"
)

func testSettings() Settings {
	return Settings{
		PollIntervalMinutes:  5,
		NotificationsEnabled: true,
		EarlyThresholdPct:    10,
		DangerThresholdPct:   10,
	}
}

func oneWindowSnap(providerID, name, key, title string, used float64, resetsAt *time.Time, fetchedAt time.Time) Snapshot {
	return Snapshot{
		FetchedAt: fetchedAt,
		Providers: []Provider{{
			ID:   providerID,
			Name: name,
			Windows: []Window{{
				ID:               providerID + "." + key,
				Key:              key,
				Title:            title,
				UsedPercent:      used,
				RemainingPercent: 100 - used,
				ResetsAt:         resetsAt,
			}},
		}},
	}
}

func seedWindow(t *testing.T, s *Store, windowID string, used float64, resetsAt *time.Time) {
	t.Helper()
	if err := s.SetWindowState(WindowState{WindowID: windowID, UsedPercent: used, ResetsAt: resetsAt}); err != nil {
		t.Fatalf("seed window: %v", err)
	}
}

func eventTypes(evs []Event) map[string]Event {
	m := make(map[string]Event, len(evs))
	for _, e := range evs {
		m[e.Type] = e
	}
	return m
}

func TestBaselineSuppressionOnFirstSight(t *testing.T) {
	s := openTestStore(t)
	e := NewEventEngine(s)
	now := time.Now().UTC()

	snap := oneWindowSnap("codex", "Codex", "primary", "5h limit", 95, nil, now)
	evs, err := e.Process(snap, testSettings(), now)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(evs) != 0 {
		t.Fatalf("expected no events on first sight, got %+v", evs)
	}

	ws, ok, err := s.GetWindowState("codex.primary")
	if err != nil || !ok {
		t.Fatalf("expected baseline recorded: ok=%v err=%v", ok, err)
	}
	if ws.UsedPercent != 95 {
		t.Fatalf("expected baseline used=95, got %v", ws.UsedPercent)
	}
}

func TestEarlyThresholdCrossing(t *testing.T) {
	s := openTestStore(t)
	e := NewEventEngine(s)
	now := time.Now().UTC()
	seedWindow(t, s, "codex.primary", 5, nil)

	snap := oneWindowSnap("codex", "Codex", "primary", "5h limit", 15, nil, now)
	evs, err := e.Process(snap, testSettings(), now)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	m := eventTypes(evs)
	if _, ok := m["early_threshold"]; !ok {
		t.Fatalf("expected early_threshold, got %+v", evs)
	}
	if _, ok := m["danger_threshold"]; ok {
		t.Fatalf("did not expect danger_threshold, got %+v", evs)
	}
}

func TestEarlyThresholdDropIsNoop(t *testing.T) {
	s := openTestStore(t)
	e := NewEventEngine(s)
	now := time.Now().UTC()
	seedWindow(t, s, "codex.primary", 50, nil)

	snap := oneWindowSnap("codex", "Codex", "primary", "5h limit", 8, nil, now)
	evs, err := e.Process(snap, testSettings(), now)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(evs) != 0 {
		t.Fatalf("expected no events when usage drops below early with no reset, got %+v", evs)
	}
}

func TestDangerThresholdCrossing(t *testing.T) {
	s := openTestStore(t)
	e := NewEventEngine(s)
	now := time.Now().UTC()
	seedWindow(t, s, "codex.primary", 50, nil)

	snap := oneWindowSnap("codex", "Codex", "primary", "5h limit", 95, nil, now)
	evs, err := e.Process(snap, testSettings(), now)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	m := eventTypes(evs)
	if _, ok := m["danger_threshold"]; !ok {
		t.Fatalf("expected danger_threshold, got %+v", evs)
	}
	if _, ok := m["early_threshold"]; ok {
		t.Fatalf("did not expect early_threshold (already above), got %+v", evs)
	}
}

func TestEarlyAndDangerSamePoll(t *testing.T) {
	s := openTestStore(t)
	e := NewEventEngine(s)
	now := time.Now().UTC()
	seedWindow(t, s, "codex.primary", 5, nil)

	snap := oneWindowSnap("codex", "Codex", "primary", "5h limit", 95, nil, now)
	evs, err := e.Process(snap, testSettings(), now)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	m := eventTypes(evs)
	if _, ok := m["early_threshold"]; !ok {
		t.Fatalf("expected early_threshold, got %+v", evs)
	}
	if _, ok := m["danger_threshold"]; !ok {
		t.Fatalf("expected danger_threshold, got %+v", evs)
	}
}

func TestResetCycleFiresOnceAndDedupAcrossRestart(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UTC()
	past := now.Add(-1 * time.Hour)
	future := now.Add(5 * time.Hour)

	seedWindow(t, s, "codex.primary", 80, &past)

	snap := oneWindowSnap("codex", "Codex", "primary", "5h limit", 5, &future, now)

	e1 := NewEventEngine(s)
	evs, err := e1.Process(snap, testSettings(), now)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if _, ok := eventTypes(evs)["reset"]; !ok {
		t.Fatalf("expected reset event, got %+v", evs)
	}

	// Simulate a restart that re-sees the old baseline; fresh engine, same store.
	seedWindow(t, s, "codex.primary", 80, &past)
	e2 := NewEventEngine(s)
	evs, err = e2.Process(snap, testSettings(), now)
	if err != nil {
		t.Fatalf("Process restart: %v", err)
	}
	if len(evs) != 0 {
		t.Fatalf("expected no repeat reset after restart (dedup), got %+v", evs)
	}
}

func TestTiboBoundaryCases(t *testing.T) {
	now := time.Now().UTC()
	future := now.Add(3 * time.Hour)

	cases := []struct {
		name     string
		prevUsed float64
		curUsed  float64
		want     bool
	}{
		{"50pp drop exactly", 60, 10, true},
		{"49pp drop no", 60, 11, false},
		{"20 to 5 yes", 20, 5, true},
		{"19 to 5 no", 19, 5, false},
		{"20 to 6 no", 20, 6, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			e := NewEventEngine(s)
			seedWindow(t, s, "codex.primary", tc.prevUsed, &future)
			snap := oneWindowSnap("codex", "Codex", "primary", "5h limit", tc.curUsed, &future, now)
			evs, err := e.Process(snap, testSettings(), now)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			_, fired := eventTypes(evs)["tibo_reset"]
			if fired != tc.want {
				t.Fatalf("tibo_reset fired=%v, want=%v (events %+v)", fired, tc.want, evs)
			}
		})
	}
}

func TestTiboTitleByProvider(t *testing.T) {
	now := time.Now().UTC()
	future := now.Add(3 * time.Hour)

	for _, tc := range []struct {
		providerID string
		wantTitle  string
	}{
		{"codex", "Tibo blessed"},
		{"claude", "Surprise reset"},
	} {
		t.Run(tc.providerID, func(t *testing.T) {
			s := openTestStore(t)
			e := NewEventEngine(s)
			seedWindow(t, s, tc.providerID+".primary", 60, &future)
			snap := oneWindowSnap(tc.providerID, "Name", "primary", "5h limit", 5, &future, now)
			evs, err := e.Process(snap, testSettings(), now)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			ev, ok := eventTypes(evs)["tibo_reset"]
			if !ok {
				t.Fatalf("expected tibo_reset, got %+v", evs)
			}
			if ev.Title != tc.wantTitle {
				t.Fatalf("title=%q, want %q", ev.Title, tc.wantTitle)
			}
		})
	}
}

func creditsSnap(providerID string, count int, fetchedAt time.Time) Snapshot {
	return Snapshot{
		FetchedAt: fetchedAt,
		Providers: []Provider{{
			ID:      providerID,
			Name:    "Codex",
			Credits: &Credits{AvailableCount: count},
		}},
	}
}

func TestCreditsIncreaseDecreaseSame(t *testing.T) {
	now := time.Now().UTC()

	t.Run("increase fires", func(t *testing.T) {
		s := openTestStore(t)
		e := NewEventEngine(s)
		if _, err := e.Process(creditsSnap("codex", 1, now), testSettings(), now); err != nil {
			t.Fatalf("baseline: %v", err)
		}
		evs, err := e.Process(creditsSnap("codex", 3, now), testSettings(), now)
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		if _, ok := eventTypes(evs)["credits_increase"]; !ok {
			t.Fatalf("expected credits_increase, got %+v", evs)
		}
	})

	t.Run("decrease is noop", func(t *testing.T) {
		s := openTestStore(t)
		e := NewEventEngine(s)
		e.Process(creditsSnap("codex", 3, now), testSettings(), now)
		evs, _ := e.Process(creditsSnap("codex", 1, now), testSettings(), now)
		if len(evs) != 0 {
			t.Fatalf("expected no event on decrease, got %+v", evs)
		}
	})

	t.Run("same is noop", func(t *testing.T) {
		s := openTestStore(t)
		e := NewEventEngine(s)
		e.Process(creditsSnap("codex", 2, now), testSettings(), now)
		evs, _ := e.Process(creditsSnap("codex", 2, now), testSettings(), now)
		if len(evs) != 0 {
			t.Fatalf("expected no event when unchanged, got %+v", evs)
		}
	})
}

func TestHiddenProviderSuppression(t *testing.T) {
	s := openTestStore(t)
	e := NewEventEngine(s)
	now := time.Now().UTC()
	seedWindow(t, s, "codex.primary", 5, nil)

	settings := testSettings()
	settings.HiddenProviders = []string{"codex"}

	snap := oneWindowSnap("codex", "Codex", "primary", "5h limit", 95, nil, now)
	evs, err := e.Process(snap, settings, now)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(evs) != 0 {
		t.Fatalf("expected no events for hidden provider, got %+v", evs)
	}
}

func TestDuplicatePollNoRepeat(t *testing.T) {
	s := openTestStore(t)
	e := NewEventEngine(s)
	now := time.Now().UTC()
	seedWindow(t, s, "codex.primary", 5, nil)

	snap := oneWindowSnap("codex", "Codex", "primary", "5h limit", 15, nil, now)
	evs, err := e.Process(snap, testSettings(), now)
	if err != nil {
		t.Fatalf("Process 1: %v", err)
	}
	if len(evs) == 0 {
		t.Fatalf("expected an event on first crossing")
	}
	evs, err = e.Process(snap, testSettings(), now)
	if err != nil {
		t.Fatalf("Process 2: %v", err)
	}
	if len(evs) != 0 {
		t.Fatalf("expected no events on duplicate poll, got %+v", evs)
	}
}

func TestNotificationsDisabledSuppressesButAdvancesBaseline(t *testing.T) {
	s := openTestStore(t)
	e := NewEventEngine(s)
	now := time.Now().UTC()
	seedWindow(t, s, "codex.primary", 5, nil)

	settings := testSettings()
	settings.NotificationsEnabled = false

	snap := oneWindowSnap("codex", "Codex", "primary", "5h limit", 15, nil, now)
	evs, err := e.Process(snap, settings, now)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(evs) != 0 {
		t.Fatalf("expected no events when notifications disabled, got %+v", evs)
	}
	ws, ok, _ := s.GetWindowState("codex.primary")
	if !ok || ws.UsedPercent != 15 {
		t.Fatalf("expected baseline advanced to 15, got ok=%v used=%v", ok, ws.UsedPercent)
	}
}
