package server

import (
	"strings"
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
		{"claude", "Tibo has struck again! Claude limits reset"},
		{"codex", "Saint Tibo has blessed you with tokens, Codex limits reset"},
		{"demo", "mini-tibo (me) has blessed you with pretend tokens"},
		{"grok", "Surprise reset"},
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

func TestProcessDetailedReturnsDeduplicatedOutcome(t *testing.T) {
	s := openTestStore(t)
	e := NewEventEngine(s)
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	reset := now.Add(5 * time.Hour)
	seedWindow(t, s, "demo.primary", 5, &reset)
	snap := oneWindowSnap("demo", "Demo", "primary", "Primary", 15, &reset, now)

	first, err := e.ProcessDetailed(snap, testSettings(), now)
	if err != nil {
		t.Fatalf("ProcessDetailed first: %v", err)
	}
	if len(first.Emitted) != 1 || len(first.Outcomes) != 1 || first.Outcomes[0].Deduplicated {
		t.Fatalf("expected one newly emitted outcome, got %+v", first)
	}
	if first.Outcomes[0].Before.UsedPercent == nil || *first.Outcomes[0].Before.UsedPercent != 5 {
		t.Fatalf("unexpected before value: %+v", first.Outcomes[0].Before)
	}
	if first.Outcomes[0].After.UsedPercent == nil || *first.Outcomes[0].After.UsedPercent != 15 {
		t.Fatalf("unexpected after value: %+v", first.Outcomes[0].After)
	}

	// Recreate the same candidate as after a restart that retained the event claim.
	seedWindow(t, s, "demo.primary", 5, &reset)
	second, err := e.ProcessDetailed(snap, testSettings(), now)
	if err != nil {
		t.Fatalf("ProcessDetailed second: %v", err)
	}
	if len(second.Emitted) != 0 || len(second.Outcomes) != 1 || !second.Outcomes[0].Deduplicated {
		t.Fatalf("expected one deduplicated outcome and no emitted event, got %+v", second)
	}
}

func TestStaleDemoProviderDoesNotEmitOrAdvanceBaseline(t *testing.T) {
	s := openTestStore(t)
	e := NewEventEngine(s)
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	seedWindow(t, s, "demo.primary", 5, nil)
	snap := oneWindowSnap("demo", "Demo", "primary", "Primary", 95, nil, now)
	snap.Providers[0].Stale = true

	result, err := e.ProcessDetailed(snap, testSettings(), now)
	if err != nil {
		t.Fatalf("ProcessDetailed: %v", err)
	}
	if len(result.Emitted) != 0 || len(result.Outcomes) != 0 {
		t.Fatalf("stale provider produced events: %+v", result)
	}
	ws, ok, err := s.GetWindowState("demo.primary")
	if err != nil || !ok || ws.UsedPercent != 5 {
		t.Fatalf("stale provider advanced baseline: ok=%v state=%+v err=%v", ok, ws, err)
	}
}

func TestErroredProviderDoesNotEmitOrAdvanceBaseline(t *testing.T) {
	s := openTestStore(t)
	e := NewEventEngine(s)
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	seedWindow(t, s, "demo.primary", 5, nil)
	snap := oneWindowSnap("demo", "Demo", "primary", "Primary", 95, nil, now)
	snap.Providers[0].Error = "demo provider unavailable"

	result, err := e.ProcessDetailed(snap, testSettings(), now)
	if err != nil {
		t.Fatalf("ProcessDetailed: %v", err)
	}
	if len(result.Emitted) != 0 || len(result.Outcomes) != 0 {
		t.Fatalf("errored provider produced events: %+v", result)
	}
	ws, ok, err := s.GetWindowState("demo.primary")
	if err != nil || !ok || ws.UsedPercent != 5 {
		t.Fatalf("errored provider advanced baseline: ok=%v state=%+v err=%v", ok, ws, err)
	}
}

func TestDemoEventKeysAreNamespaced(t *testing.T) {
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	reset := now.Add(5 * time.Hour)

	windowCases := []struct {
		name        string
		previous    float64
		current     float64
		previousAt  time.Time
		currentAt   time.Time
		wantKeyKind string
		wantCycle   time.Time
	}{
		{name: "early", previous: 5, current: 15, previousAt: reset, currentAt: reset, wantKeyKind: "early", wantCycle: reset},
		{name: "danger", previous: 50, current: 95, previousAt: reset, currentAt: reset, wantKeyKind: "danger", wantCycle: reset},
		{name: "reset", previous: 80, current: 5, previousAt: now.Add(-time.Hour), currentAt: reset, wantKeyKind: "reset", wantCycle: now.Add(-time.Hour)},
		{name: "tibo", previous: 60, current: 5, previousAt: reset, currentAt: reset, wantKeyKind: "tibo", wantCycle: reset},
	}
	for _, tc := range windowCases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			e := NewEventEngine(s)
			seedWindow(t, s, "demo.primary", tc.previous, &tc.previousAt)
			result, err := e.ProcessDetailed(oneWindowSnap("demo", "Demo", "primary", "Primary", tc.current, &tc.currentAt, now), testSettings(), now)
			if err != nil {
				t.Fatalf("ProcessDetailed: %v", err)
			}
			want := "demo.event." + tc.wantKeyKind + ":demo.primary:" + tc.wantCycle.Format(time.RFC3339)
			if len(result.Emitted) != 1 || result.Emitted[0].Key != want || !strings.HasPrefix(result.Emitted[0].Key, "demo.") {
				t.Fatalf("unexpected demo event key: %+v, want %q", result.Emitted, want)
			}
		})
	}

	t.Run("credits", func(t *testing.T) {
		s := openTestStore(t)
		e := NewEventEngine(s)
		if _, err := e.ProcessDetailed(creditsSnap("demo", 1, now), testSettings(), now); err != nil {
			t.Fatalf("baseline: %v", err)
		}
		if _, ok, err := s.GetWindowState("demo.credits"); err != nil || !ok {
			t.Fatalf("expected demo.credits baseline: ok=%v err=%v", ok, err)
		}
		if _, ok, err := s.GetWindowState("demo#credits"); err != nil || ok {
			t.Fatalf("unexpected legacy demo credits baseline: ok=%v err=%v", ok, err)
		}
		result, err := e.ProcessDetailed(creditsSnap("demo", 3, now), testSettings(), now)
		if err != nil {
			t.Fatalf("ProcessDetailed: %v", err)
		}
		if len(result.Emitted) != 1 || result.Emitted[0].Key != "demo.event.credits:3" {
			t.Fatalf("unexpected demo credits event: %+v", result.Emitted)
		}
		outcome := result.Outcomes[0]
		if outcome.Before.CreditsAvailable == nil || *outcome.Before.CreditsAvailable != 1 || outcome.After.CreditsAvailable == nil || *outcome.After.CreditsAvailable != 3 {
			t.Fatalf("unexpected credits outcome values: %+v", outcome)
		}
	})
}

func TestRealProviderKeysRemainUnchanged(t *testing.T) {
	s := openTestStore(t)
	e := NewEventEngine(s)
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	reset := now.Add(5 * time.Hour)
	seedWindow(t, s, "codex.primary", 5, &reset)

	result, err := e.ProcessDetailed(oneWindowSnap("codex", "Codex", "primary", "Primary", 15, &reset, now), testSettings(), now)
	if err != nil {
		t.Fatalf("ProcessDetailed: %v", err)
	}
	want := "early:codex.primary:" + reset.Format(time.RFC3339)
	if len(result.Emitted) != 1 || result.Emitted[0].Key != want {
		t.Fatalf("real-provider key changed: got %+v want %q", result.Emitted, want)
	}

	if _, err := e.ProcessDetailed(creditsSnap("codex", 1, now), testSettings(), now); err != nil {
		t.Fatalf("credits baseline: %v", err)
	}
	if _, ok, err := s.GetWindowState("codex#credits"); err != nil || !ok {
		t.Fatalf("real-provider credits key changed: ok=%v err=%v", ok, err)
	}
}
