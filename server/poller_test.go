package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const codexBarBody = `{
  "providers": [
    {
      "id": "codex",
      "name": "Codex",
      "primary": {"title": "5h limit", "usedPercent": 42.0, "resetsAt": "2026-07-17T20:00:00Z"},
      "codexResetCredits": {"availableCount": 2}
    }
  ]
}`

func newPollerHarness(t *testing.T) (*Poller, *Store, *atomic.Bool) {
	t.Helper()
	store := openTestStore(t)
	healthy := &atomic.Bool{}
	healthy.Store(true)

	codexbar := NewCodexBarClient("http://codexbar.test")
	codexbar.httpClient = &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		if !healthy.Load() {
			return testHTTPResponse(http.StatusInternalServerError, "boom"), nil
		}
		return testHTTPResponse(http.StatusOK, codexBarBody), nil
	})}

	api := NewAPI(Config{Token: "x"}, store, codexbar)
	poller := NewPoller(store, codexbar, noopNotifier{}, api)
	return poller, store, healthy
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func testHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func latestSnap(t *testing.T, s *Store) Snapshot {
	t.Helper()
	_, payload, ok, err := s.LatestSnapshot()
	if err != nil || !ok {
		t.Fatalf("LatestSnapshot: ok=%v err=%v", ok, err)
	}
	var snap Snapshot
	if err := json.Unmarshal(payload, &snap); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	return snap
}

func pollDemoForTest(t *testing.T, poller *Poller) DemoPipelineResult {
	t.Helper()
	targets, err := poller.store.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	result, err := poller.PollDemoNow(context.Background(), 0, "", targets)
	if err != nil {
		t.Fatalf("PollDemoNow: %v", err)
	}
	return result
}

func TestPollerSavesSnapshot(t *testing.T) {
	poller, store, _ := newPollerHarness(t)
	poller.pollOnce(context.Background())

	snap := latestSnap(t, store)
	if snap.Stale {
		t.Fatalf("expected fresh snapshot, got stale")
	}
	if len(snap.Providers) != 1 || snap.Providers[0].ID != "codex" {
		t.Fatalf("unexpected providers: %+v", snap.Providers)
	}
}

func TestPollerIncludesDemoProviderWhenEnabled(t *testing.T) {
	poller, store, _ := newPollerHarness(t)
	if err := store.SetSetting("demo_provider_enabled", "true"); err != nil {
		t.Fatal(err)
	}

	result := poller.PollNow(context.Background())
	if !result.Success {
		t.Fatalf("PollNow failed: %+v", result)
	}
	snap := latestSnap(t, store)
	if len(snap.Providers) != 2 || snap.Providers[0].ID != "codex" {
		t.Fatalf("providers=%+v, want real provider plus demo", snap.Providers)
	}
	demoProvider(t, snap)

	if err := store.SetSetting("demo_provider_enabled", "false"); err != nil {
		t.Fatal(err)
	}
	result = poller.PollNow(context.Background())
	if !result.Success {
		t.Fatalf("PollNow after disabling demo failed: %+v", result)
	}
	snap = latestSnap(t, store)
	if len(snap.Providers) != 1 || snap.Providers[0].ID != "codex" {
		t.Fatalf("providers=%+v, want demo removed after disabling", snap.Providers)
	}
}

func TestPollerStaleFallback(t *testing.T) {
	poller, store, healthy := newPollerHarness(t)

	poller.pollOnce(context.Background())
	fresh := latestSnap(t, store)

	healthy.Store(false)
	poller.pollOnce(context.Background())

	stale := latestSnap(t, store)
	if !stale.Stale {
		t.Fatalf("expected snapshot marked stale after fetch failure")
	}
	if len(stale.Providers) != len(fresh.Providers) || stale.Providers[0].ID != fresh.Providers[0].ID {
		t.Fatalf("expected previous providers preserved, got %+v", stale.Providers)
	}
}

func TestPollDemoNowRevisionConflict(t *testing.T) {
	poller, store, _ := newPollerHarness(t)
	state, err := store.LoadDemoState()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := poller.PollDemoNow(context.Background(), state.Revision+1, "conflict", nil); !errors.Is(err, ErrDemoRevisionConflict) {
		t.Fatalf("err=%v", err)
	}
	if _, _, ok, err := store.LatestSnapshot(); err != nil || ok {
		t.Fatalf("conflict performed a poll: ok=%v err=%v", ok, err)
	}
}

func TestPollDemoActionAndPatchRacePreservesPatch(t *testing.T) {
	poller, store, _ := newPollerHarness(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	poller.codexbar.httpClient = &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		close(entered)
		<-release
		return testHTTPResponse(http.StatusOK, codexBarBody), nil
	})}
	pollDone := make(chan DemoPipelineResult, 1)
	go func() {
		result, _ := poller.PollDemoAction(context.Background(), 1, DemoAction{ID: "poll-action", Identity: "id", Route: "poll", CreatedAt: time.Now().UTC()}, nil)
		pollDone <- result
	}()
	<-entered
	patchDone := make(chan error, 1)
	value := true
	go func() {
		_, err := store.CommitDemoPatch(DemoAction{ID: "patch-action", Identity: "id", Route: "patch", CreatedAt: time.Now().UTC()}, DemoStatePatch{Stale: &value}, http.StatusOK, "ok")
		patchDone <- err
	}()
	select {
	case err := <-patchDone:
		t.Fatalf("patch committed before serialized poll: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if result := <-pollDone; result.DemoRunID != "poll-action" {
		t.Fatalf("poll result=%+v", result)
	}
	if err := <-patchDone; err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadDemoState()
	if err != nil || state.Revision != 2 || !state.Stale || state.LastDemoRunID != "patch-action" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func TestPollerNoRepeatEventsOnDuplicate(t *testing.T) {
	poller, store, _ := newPollerHarness(t)

	// Seed a baseline below early so the first real poll crosses it.
	seedWindow(t, store, "codex.primary", 5, nil)

	result := poller.PollNow(context.Background())
	if !result.Success || result.Events < 1 {
		t.Fatalf("expected successful poll with events, got %+v", result)
	}
	snap := latestSnap(t, store)
	w := snap.Providers[0].Windows[0]
	earlyKey := eventKey("early", w.ID, w.ResetsAt)
	first, err := store.EventNotified(earlyKey)
	if err != nil {
		t.Fatalf("EventNotified: %v", err)
	}
	if !first {
		t.Fatalf("expected early event recorded after first poll (key %q)", earlyKey)
	}

	// A second identical poll must not produce a fresh crossing.
	result2 := poller.PollNow(context.Background())
	if !result2.Success {
		t.Fatalf("second poll failed: %+v", result2)
	}
	if result2.Events != 0 {
		t.Fatalf("expected no new events on duplicate poll, got %d", result2.Events)
	}
	snap = latestSnap(t, store)
	if snap.Providers[0].Windows[0].UsedPercent != 42 {
		t.Fatalf("unexpected snapshot state: %+v", snap.Providers[0].Windows[0])
	}
}

type pollRecordingNotifier struct {
	mu            sync.Mutex
	alerts        []string
	widgets       []string
	failAlertFor  map[string]bool
	failWidgetFor map[string]bool
}

func (n *pollRecordingNotifier) SendAlert(_ context.Context, token string, _ Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.alerts = append(n.alerts, token)
	if n.failAlertFor[token] {
		return errors.New("alert rejected")
	}
	return nil
}

func (n *pollRecordingNotifier) SendWidgetRefresh(_ context.Context, token string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.widgets = append(n.widgets, token)
	if n.failWidgetFor[token] {
		return errors.New("widget rejected")
	}
	return nil
}

func demoProvider(t *testing.T, snap Snapshot) Provider {
	t.Helper()
	for _, provider := range snap.Providers {
		if provider.ID == "demo" {
			return provider
		}
	}
	t.Fatalf("demo provider not found in %+v", snap.Providers)
	return Provider{}
}

func stageByID(t *testing.T, result DemoPipelineResult, id string) DemoPipelineStage {
	t.Helper()
	for _, stage := range result.Stages {
		if stage.ID == id {
			return stage
		}
	}
	t.Fatalf("stage %q not found in %+v", id, result.Stages)
	return DemoPipelineStage{}
}

func TestPollerDemoPreservesRealProviderAndBaselines(t *testing.T) {
	poller, store, _ := newPollerHarness(t)
	now := time.Now().UTC()
	state := DefaultDemoState(now)
	state.Primary.UsedPercent = 73
	state.UpdatedAt = now
	if err := store.SaveDemoState(state); err != nil {
		t.Fatal(err)
	}
	seedWindow(t, store, "codex.primary", 5, nil)
	seedWindow(t, store, "demo.primary", 9, nil)

	result := pollDemoForTest(t, poller)
	if !result.Success {
		t.Fatalf("PollDemoNow failed: %+v", result)
	}
	snap := latestSnap(t, store)
	if len(snap.Providers) != 2 {
		t.Fatalf("providers=%+v, want one real and one demo", snap.Providers)
	}
	if got := snap.Providers[0]; got.ID != "codex" || got.Windows[0].UsedPercent != 42 || got.Credits.AvailableCount != 2 {
		t.Fatalf("real provider changed by demo injection: %+v", got)
	}
	demo := demoProvider(t, snap)
	if demo.Windows[0].UsedPercent != 73 || demo.Stale != state.Stale {
		t.Fatalf("demo provider was not normalized from persisted state: %+v", demo)
	}
	for _, tc := range []struct {
		id   string
		want float64
	}{{"codex.primary", 42}, {"demo.primary", 73}} {
		baseline, ok, err := store.GetWindowState(tc.id)
		if err != nil || !ok || baseline.UsedPercent != tc.want {
			t.Fatalf("baseline %s: got %+v ok=%v err=%v, want %.0f", tc.id, baseline, ok, err, tc.want)
		}
	}
	if result.EventsEmitted < 2 {
		t.Fatalf("expected independent real and demo threshold events, got %+v", result)
	}
	for _, id := range []string{"demo_state", "normalize", "snapshot_persisted", "event_engine", "apns"} {
		stage := stageByID(t, result, id)
		if id == "apns" {
			if stage.Status != DemoStageSkipped {
				t.Fatalf("noop APNs stage=%+v, want skipped", stage)
			}
		} else if stage.Status != DemoStageOK {
			t.Fatalf("stage %s=%+v, want ok", id, stage)
		}
	}
}

func TestPollerDemoDispatchesOnlyEmittedEventsAndPersistsOutcomes(t *testing.T) {
	poller, store, _ := newPollerHarness(t)
	notifier := &pollRecordingNotifier{}
	poller.notifier = notifier
	if err := store.UpsertDevice("phone", "alert-token", "widget-token"); err != nil {
		t.Fatal(err)
	}
	seedWindow(t, store, "demo.primary", 5, nil)

	first := pollDemoForTest(t, poller)
	if !first.Success || first.EventsEmitted == 0 {
		t.Fatalf("first PollDemoNow=%+v", first)
	}
	if first.Delivery.Alerts.Attempted != first.EventsEmitted || first.Delivery.Alerts.Failed != 0 {
		t.Fatalf("first delivery=%+v, emitted=%d", first.Delivery, first.EventsEmitted)
	}

	// Recreate the crossing while retaining its claimed event key. The outcome is
	// deduplicated and therefore must not be sent to APNs.
	seedWindow(t, store, "demo.primary", 5, nil)
	second := pollDemoForTest(t, poller)
	if !second.Success || second.EventsEmitted != 0 || second.EventsDeduplicated == 0 {
		t.Fatalf("second PollDemoNow=%+v", second)
	}
	if second.Delivery.Alerts.Attempted != 0 {
		t.Fatalf("deduplicated event was dispatched: %+v", second.Delivery)
	}

	stored, ok, err := store.LatestDemoRun()
	if err != nil || !ok {
		t.Fatalf("LatestDemoRun: ok=%v err=%v", ok, err)
	}
	if stored.ID != second.ID || stored.EventsDeduplicated != second.EventsDeduplicated || len(stored.Stages) != 5 {
		t.Fatalf("stored run=%+v, result=%+v", stored, second)
	}
	feed, err := store.ListDemoEvents(100)
	if err != nil {
		t.Fatal(err)
	}
	var sawDeduplicated, sawManual bool
	for _, event := range feed {
		if event.RunID == nil || *event.RunID == 0 {
			t.Fatalf("feed event missing run ID: %+v", event)
		}
		if event.Type == "early_threshold" && event.Deduplicated {
			sawDeduplicated = true
		}
		if event.Type == "manual_poll" {
			sawManual = true
		}
	}
	if !sawDeduplicated || !sawManual {
		t.Fatalf("feed missing persisted outcomes: %+v", feed)
	}
}

func TestPollDeliveryPartialFailureIsWarning(t *testing.T) {
	poller, store, _ := newPollerHarness(t)
	notifier := &pollRecordingNotifier{failAlertFor: map[string]bool{"bad-alert": true}}
	poller.notifier = notifier
	if err := store.UpsertDevice("good", "good-alert", "good-widget"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertDevice("bad", "bad-alert", "bad-widget"); err != nil {
		t.Fatal(err)
	}
	seedWindow(t, store, "demo.primary", 5, nil)

	result := pollDemoForTest(t, poller)
	if !result.Success {
		t.Fatalf("partial APNs failure failed pipeline: %+v", result)
	}
	if got := stageByID(t, result, "apns").Status; got != DemoStageWarning {
		t.Fatalf("APNs status=%q, want warning", got)
	}
	if result.Delivery.Alerts.Attempted != 2 || result.Delivery.Alerts.Succeeded != 1 || result.Delivery.Alerts.Failed != 1 {
		t.Fatalf("alert delivery counts=%+v", result.Delivery.Alerts)
	}
	if result.Delivery.WidgetRefresh.Attempted != 2 || result.Delivery.WidgetRefresh.Succeeded != 2 {
		t.Fatalf("widget delivery counts=%+v", result.Delivery.WidgetRefresh)
	}
}

func TestPollDeliveryDisabledOrNoTokenIsSkipped(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		poller, store, _ := newPollerHarness(t)
		if err := store.UpsertDevice("phone", "alert", "widget"); err != nil {
			t.Fatal(err)
		}
		seedWindow(t, store, "demo.primary", 5, nil)
		result := pollDemoForTest(t, poller)
		if stageByID(t, result, "apns").Status != DemoStageSkipped || result.Delivery != (DemoDeliveryResult{}) {
			t.Fatalf("disabled delivery=%+v stages=%+v", result.Delivery, result.Stages)
		}
	})

	t.Run("no tokens", func(t *testing.T) {
		poller, store, _ := newPollerHarness(t)
		poller.notifier = &pollRecordingNotifier{}
		if err := store.UpsertDevice("phone", "", ""); err != nil {
			t.Fatal(err)
		}
		seedWindow(t, store, "demo.primary", 5, nil)
		result := pollDemoForTest(t, poller)
		if stageByID(t, result, "apns").Status != DemoStageSkipped || result.Delivery != (DemoDeliveryResult{}) {
			t.Fatalf("tokenless delivery=%+v stages=%+v", result.Delivery, result.Stages)
		}
	})
}

func TestPollerDemoFailuresKeepLastSnapshot(t *testing.T) {
	tests := []struct {
		name       string
		breakInput func(*testing.T, *Store, *atomic.Bool, **CodexBarClient)
		failed     string
	}{
		{
			name: "demo state load",
			breakInput: func(t *testing.T, store *Store, _ *atomic.Bool, _ **CodexBarClient) {
				if _, err := store.db.Exec(`UPDATE demo_state SET payload = '{' WHERE key = 'demo.state'`); err != nil {
					t.Fatal(err)
				}
			},
			failed: "demo_state",
		},
		{
			name: "normalize",
			breakInput: func(t *testing.T, _ *Store, _ *atomic.Bool, client **CodexBarClient) {
				broken := NewCodexBarClient("http://codexbar.test")
				broken.httpClient = &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
					return testHTTPResponse(http.StatusOK, `{"providers":[{"name":"missing id"}]}`), nil
				})}
				*client = broken
			},
			failed: "normalize",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			poller, store, healthy := newPollerHarness(t)
			if got := poller.PollNow(context.Background()); !got.Success {
				t.Fatalf("seed poll failed: %+v", got)
			}
			_, before, _, err := store.LatestSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			client := poller.codexbar
			tt.breakInput(t, store, healthy, &client)
			poller.codexbar = client

			result := pollDemoForTest(t, poller)
			if result.Success || result.FailedStage != tt.failed || stageByID(t, result, tt.failed).Status != DemoStageFailed {
				t.Fatalf("failure result=%+v", result)
			}
			_, after, _, err := store.LatestSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatalf("failed demo poll overwrote snapshot:\nbefore=%s\nafter=%s", before, after)
			}
			feed, err := store.ListDemoEvents(10)
			if err != nil {
				t.Fatal(err)
			}
			if len(feed) == 0 || feed[0].Type != "pipeline_error" {
				t.Fatalf("pipeline failure was not persisted: %+v", feed)
			}
		})
	}
}

func TestPollerDemoPersistenceFailureReturnsAndStoresFailure(t *testing.T) {
	poller, store, _ := newPollerHarness(t)
	if _, err := store.db.Exec(`
		CREATE TRIGGER fail_manual_poll
		BEFORE INSERT ON demo_event_log
		WHEN NEW.event_type = 'manual_poll'
		BEGIN
			SELECT RAISE(ABORT, 'injected event persistence failure');
		END
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	result := pollDemoForTest(t, poller)
	if result.Success {
		t.Fatalf("persistence failure returned success: %+v", result)
	}
	if result.FailedStage != "snapshot_persisted" {
		t.Fatalf("FailedStage=%q, want snapshot_persisted: %+v", result.FailedStage, result)
	}
	persistedStage := stageByID(t, result, "snapshot_persisted")
	if persistedStage.Status != DemoStageFailed || !strings.Contains(persistedStage.Detail, "injected event persistence failure") {
		t.Fatalf("persistence stage does not report injected failure: %+v", persistedStage)
	}
	if result.ID == 0 {
		t.Fatalf("failed pipeline result was not durably recorded: %+v", result)
	}

	stored, ok, err := store.LatestDemoRun()
	if err != nil || !ok {
		t.Fatalf("LatestDemoRun: ok=%v err=%v", ok, err)
	}
	if stored.ID != result.ID || stored.Success || stored.FailedStage != result.FailedStage || stored.Error != result.Error {
		t.Fatalf("returned and stored failures differ:\nresult=%+v\nstored=%+v", result, stored)
	}
	storedStage := stageByID(t, stored, "snapshot_persisted")
	if storedStage.Status != DemoStageFailed || storedStage.Detail != persistedStage.Detail {
		t.Fatalf("returned and stored persistence stages differ: result=%+v stored=%+v", persistedStage, storedStage)
	}

	feed, err := store.ListDemoEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed) != 1 || feed[0].RunID == nil || *feed[0].RunID != result.ID || feed[0].Type != "pipeline_error" {
		t.Fatalf("failed run was not stored atomically with its pipeline error: %+v", feed)
	}
	var runCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM demo_runs`).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 1 {
		t.Fatalf("expected only the failed retry run after rollback, got %d runs", runCount)
	}
}

func TestPollerAllPollEntriesShareDemoMutex(t *testing.T) {
	entries := []struct {
		name string
		run  func(context.Context, *Poller) <-chan PollResult
	}{
		{
			name: "PollNow",
			run: func(ctx context.Context, poller *Poller) <-chan PollResult {
				done := make(chan PollResult, 1)
				go func() { done <- poller.PollNow(ctx) }()
				return done
			},
		},
		{
			name: "scheduled Run",
			run: func(ctx context.Context, poller *Poller) <-chan PollResult {
				done := make(chan PollResult, 1)
				go func() {
					poller.Run(ctx)
					done <- PollResult{Success: true}
				}()
				return done
			},
		},
		{
			name: "internal pollOnce",
			run: func(ctx context.Context, poller *Poller) <-chan PollResult {
				done := make(chan PollResult, 1)
				go func() { done <- poller.pollOnce(ctx) }()
				return done
			},
		},
	}

	for _, entry := range entries {
		t.Run(entry.name, func(t *testing.T) {
			store := openTestStore(t)
			firstEntered := make(chan struct{})
			releaseFirst := make(chan struct{})
			var requests atomic.Int32
			client := NewCodexBarClient("http://codexbar.test")
			client.httpClient = &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				if requests.Add(1) == 1 {
					close(firstEntered)
					<-releaseFirst
				}
				return testHTTPResponse(http.StatusOK, codexBarBody), nil
			})}
			api := NewAPI(Config{Token: "x"}, store, client)
			poller := NewPoller(store, client, noopNotifier{}, api)

			demoDone := make(chan DemoPipelineResult, 1)
			go func() { result, _ := poller.PollDemoNow(context.Background(), 0, "", nil); demoDone <- result }()
			<-firstEntered
			ctx, cancel := context.WithCancel(context.Background())
			entryDone := entry.run(ctx, poller)

			select {
			case <-time.After(75 * time.Millisecond):
				if requests.Load() != 1 {
					t.Fatalf("%s entered upstream while PollDemoNow held mutex; requests=%d", entry.name, requests.Load())
				}
			case <-entryDone:
				t.Fatalf("%s completed while PollDemoNow still held the shared mutex", entry.name)
			}
			close(releaseFirst)
			if result := <-demoDone; !result.Success {
				t.Fatalf("demo poll failed: %+v", result)
			}
			cancel()
			if result := <-entryDone; !result.Success {
				t.Fatalf("%s failed: %+v", entry.name, result)
			}
			if requests.Load() != 2 {
				t.Fatalf("requests=%d, want 2 serialized requests", requests.Load())
			}
		})
	}
}
