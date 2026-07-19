package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

type PollResult struct {
	PolledAt        time.Time `json:"polledAt"`
	Success         bool      `json:"success"`
	Events          int       `json:"events"`
	SnapshotChanged bool      `json:"snapshotChanged"`
	Error           string    `json:"error,omitempty"`
	DurationMS      int64     `json:"durationMs"`
}

type DemoStageStatus string

const (
	DemoStageOK      DemoStageStatus = "ok"
	DemoStageWarning DemoStageStatus = "warning"
	DemoStageFailed  DemoStageStatus = "failed"
	DemoStageSkipped DemoStageStatus = "skipped"
)

type DemoPipelineStage struct {
	ID         string          `json:"id"`
	Status     DemoStageStatus `json:"status"`
	Detail     string          `json:"detail,omitempty"`
	DurationMS int64           `json:"durationMs"`
}

type DeliveryCount struct {
	Attempted int `json:"attempted"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}

type DemoDeliveryResult struct {
	Alerts        DeliveryCount `json:"alerts"`
	WidgetRefresh DeliveryCount `json:"widgetRefresh"`
}

type DemoPipelineResult struct {
	ID                 int64               `json:"id"`
	StartedAt          time.Time           `json:"startedAt"`
	CompletedAt        time.Time           `json:"completedAt"`
	Success            bool                `json:"success"`
	FailedStage        string              `json:"failedStage,omitempty"`
	SnapshotChanged    bool                `json:"snapshotChanged"`
	EventsEmitted      int                 `json:"eventsEmitted"`
	EventsDeduplicated int                 `json:"eventsDeduplicated"`
	Stages             []DemoPipelineStage `json:"stages"`
	Delivery           DemoDeliveryResult  `json:"delivery"`
	Error              string              `json:"error,omitempty"`
	DemoRunID          string              `json:"-"`
}

var demoPipelineStageIDs = []string{"demo_state", "normalize", "snapshot_persisted", "event_engine", "apns"}

type Poller struct {
	store    *Store
	codexbar *CodexBarClient
	engine   *EventEngine
	notifier Notifier
	api      *API

	mu sync.Mutex
}

var ErrDemoRevisionConflict = errors.New("demo revision conflict")

func NewPoller(store *Store, codexbar *CodexBarClient, notifier Notifier, api *API) *Poller {
	return &Poller{
		store:    store,
		codexbar: codexbar,
		engine:   NewEventEngine(store),
		notifier: notifier,
		api:      api,
	}
}

func (p *Poller) Run(ctx context.Context) {
	for {
		cycleStarted := time.Now()
		result := p.PollNow(ctx)
		if !result.Success && retryablePollError(result.Error) {
			retryAt := time.Now().Add(30 * time.Second)
			p.setNextPollAt(retryAt)
			if !waitUntil(ctx, retryAt) {
				return
			}
			p.PollNow(ctx)
		}

		next := cycleStarted.Add(p.interval())
		if !next.After(time.Now()) {
			next = time.Now().Add(p.interval())
		}
		p.setNextPollAt(next)
		if !waitUntil(ctx, next) {
			return
		}
	}
}

func waitUntil(ctx context.Context, at time.Time) bool {
	timer := time.NewTimer(time.Until(at))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func retryablePollError(detail string) bool {
	lower := strings.ToLower(detail)
	if strings.Contains(lower, "rate limit") || strings.Contains(lower, "overload") || strings.Contains(lower, "authentication") {
		return false
	}
	return strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline") || strings.Contains(lower, "eof") || strings.Contains(lower, "connect")
}

func (p *Poller) interval() time.Duration {
	s, err := loadSettings(p.store)
	if err != nil || !validPollIntervals[s.PollIntervalMinutes] {
		return 5 * time.Minute
	}
	return time.Duration(s.PollIntervalMinutes) * time.Minute
}

// PollNow runs one poll cycle. Concurrent callers serialize on p.mu.
func (p *Poller) PollNow(ctx context.Context) PollResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.poll(ctx, false, nil, "", nil, nil, nil).poll
}

// PollDemoNow runs persisted demo state through the same serialized poll
// pipeline as real and scheduled polls, recording the detailed outcome.
func (p *Poller) PollDemoNow(ctx context.Context, expectedRevision int64, demoRunID string, targets []Device) (DemoPipelineResult, error) {
	return p.pollDemo(ctx, expectedRevision, demoRunID, targets, nil)
}

// PollDemoAction is used by the embedded API. The action's audit row is
// committed with its state/run/events after the serialized demo poll.
func (p *Poller) PollDemoAction(ctx context.Context, expectedRevision int64, action DemoAction, targets []Device) (DemoPipelineResult, error) {
	return p.pollDemo(ctx, expectedRevision, action.ID, targets, &action)
}

func (p *Poller) pollDemo(ctx context.Context, expectedRevision int64, demoRunID string, targets []Device, action *DemoAction) (DemoPipelineResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Keep a demo state snapshot from being committed over a concurrent PATCH.
	demoActionMu.Lock()
	defer demoActionMu.Unlock()
	if targets == nil {
		targets = []Device{}
	}
	state, err := p.store.LoadDemoState()
	if err != nil {
		return p.poll(ctx, true, targets, demoRunID, nil, err, action).demo, nil
	}
	if expectedRevision != 0 && expectedRevision != state.Revision {
		return DemoPipelineResult{}, fmt.Errorf("%w: current %d", ErrDemoRevisionConflict, state.Revision)
	}
	return p.poll(ctx, true, targets, demoRunID, &state, nil, action).demo, nil
}

// pollOnce is retained for package-internal compatibility and participates in
// the same serialization as every other poll entry point.
func (p *Poller) pollOnce(ctx context.Context) PollResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.poll(ctx, false, nil, "", nil, nil, nil).poll
}

type pollExecution struct {
	poll       PollResult
	demo       DemoPipelineResult
	outcomes   []EventOutcome
	dispatched dispatchResult
}

func (p *Poller) poll(ctx context.Context, recordDemo bool, targets []Device, demoRunID string, demoState *DemoState, demoStateErr error, action *DemoAction) (result pollExecution) {
	started := time.Now()
	defer func() {
		result.poll.DurationMS = time.Since(started).Milliseconds()
		if err := p.store.SavePollOutcome(result.poll); err != nil {
			log.Printf("poller: persist poll outcome: %v", err)
		}
		if p.api != nil {
			p.api.RecordPollOutcome(result.poll)
		}
	}()
	now := time.Now().UTC()
	execution := pollExecution{poll: PollResult{PolledAt: now}}
	if recordDemo {
		execution.demo = newDemoPipelineResult(now)
		execution.demo.DemoRunID = demoRunID
		started := time.Now()
		if demoStateErr != nil {
			return p.failPoll(execution, "demo_state", started, demoStateErr, false, action, nil)
		}
		setDemoStage(&execution.demo, "demo_state", DemoStageOK, "", started)
		execution = p.pollWithInputs(ctx, execution, now, demoState, targets, action)
	} else {
		execution = p.pollWithInputs(ctx, execution, now, nil, nil, nil)
	}
	return execution
}

func (p *Poller) setNextPollAt(at time.Time) {
	if p.api != nil {
		p.api.SetNextPollAt(at.UTC())
	}
}

func (p *Poller) pollWithInputs(ctx context.Context, execution pollExecution, now time.Time, demoState *DemoState, targets []Device, action *DemoAction) pollExecution {
	recordDemo := demoState != nil
	settings, err := loadSettings(p.store)
	if err != nil {
		log.Printf("poller: load settings: %v", err)
		if recordDemo {
			return p.failPoll(execution, "normalize", time.Now(), err, false, action, demoState)
		}
		execution.poll.Error = err.Error()
		return execution
	}
	injectedDemoState := demoState
	if !recordDemo && settings.DemoProviderEnabled {
		state, loadErr := p.store.LoadDemoState()
		if loadErr != nil {
			log.Printf("poller: load enabled demo provider: %v", loadErr)
			execution.poll.Error = loadErr.Error()
			return execution
		}
		injectedDemoState = &state
	}

	normalizeStarted := time.Now()
	body, err := p.codexbar.Fetch(ctx)
	normalizeWarning := ""
	if err != nil {
		log.Printf("poller: fetch failed, keeping last snapshot: %v", err)
		if recordDemo {
			// The demo console must remain usable when the real collector is
			// temporarily unavailable. Rehydrate cached raw providers as stale and
			// run the synthetic provider through the normal downstream pipeline.
			body = p.cachedProviderBody()
			normalizeWarning = "upstream unavailable; used cached providers"
		} else {
			p.markStale()
			execution.poll.Error = err.Error()
			return execution
		}
	}
	if injectedDemoState != nil {
		body, err = InjectDemoProvider(body, *injectedDemoState)
		if err != nil && recordDemo && normalizeWarning == "" {
			body = p.cachedProviderBody()
			body, err = InjectDemoProvider(body, *injectedDemoState)
			normalizeWarning = "upstream payload invalid; used cached providers"
		}
		if err != nil {
			log.Printf("poller: inject demo provider: %v", err)
			if recordDemo {
				return p.failPoll(execution, "normalize", normalizeStarted, err, false, action, demoState)
			}
			execution.poll.Error = err.Error()
			return execution
		}
	}

	snap, err := Normalize(body, settings.PollIntervalMinutes, now)
	if err != nil && recordDemo && normalizeWarning == "" {
		body = p.cachedProviderBody()
		body, injectErr := InjectDemoProvider(body, *injectedDemoState)
		if injectErr == nil {
			snap, err = Normalize(body, settings.PollIntervalMinutes, now)
		}
		normalizeWarning = "upstream payload invalid; used cached providers"
	}
	if err != nil {
		log.Printf("poller: normalize failed: %v", err)
		if recordDemo {
			return p.failPoll(execution, "normalize", normalizeStarted, err, false, action, demoState)
		}
		execution.poll.Error = err.Error()
		return execution
	}
	p.preserveLastKnownProviderUsage(&snap)
	if recordDemo {
		status := DemoStageOK
		if normalizeWarning != "" {
			status = DemoStageWarning
		}
		setDemoStage(&execution.demo, "normalize", status, normalizeWarning, normalizeStarted)
	}

	changed := p.snapshotChanged(snap)
	snapshotStarted := time.Now()
	if err := p.store.SaveSnapshotWithForecasts(&snap); err != nil {
		log.Printf("poller: save snapshot: %v", err)
		if recordDemo {
			return p.failPoll(execution, "snapshot_persisted", snapshotStarted, err, false, action, demoState)
		}
		execution.poll.Error = err.Error()
		return execution
	}
	if recordDemo {
		execution.demo.SnapshotChanged = changed
		setDemoStage(&execution.demo, "snapshot_persisted", DemoStageOK, "", snapshotStarted)
	}

	eventStarted := time.Now()
	processed, err := p.engine.ProcessDetailed(snap, settings, now)
	if err != nil {
		log.Printf("poller: process events: %v", err)
		if recordDemo {
			return p.failPoll(execution, "event_engine", eventStarted, err, changed, action, demoState)
		}
		execution.poll.Success = true
		execution.poll.SnapshotChanged = changed
		execution.poll.Error = err.Error()
		return execution
	}
	execution.outcomes = processed.Outcomes
	if recordDemo {
		execution.demo.EventsEmitted = len(processed.Emitted)
		for _, outcome := range processed.Outcomes {
			if outcome.Deduplicated {
				execution.demo.EventsDeduplicated++
			}
		}
		setDemoStage(&execution.demo, "event_engine", DemoStageOK, "", eventStarted)
	}

	dispatchStarted := time.Now()
	execution.dispatched = p.dispatch(ctx, processed.Emitted, changed, targets)
	if p.api != nil && changed {
		p.api.RecordWidgetDelivery(
			execution.dispatched.Delivery.WidgetRefresh,
			execution.dispatched.Status,
			execution.dispatched.Detail,
		)
	}
	if recordDemo {
		execution.demo.Delivery = execution.dispatched.Delivery
		setDemoStage(&execution.demo, "apns", execution.dispatched.Status, execution.dispatched.Detail, dispatchStarted)
		execution.demo.Success = true
		execution.demo.CompletedAt = time.Now().UTC()
		p.persistDemoExecution(&execution, action, demoState)
	}
	execution.poll.Success = true
	execution.poll.Events = len(processed.Emitted)
	execution.poll.SnapshotChanged = changed
	return execution
}

func (p *Poller) cachedProviderBody() []byte {
	_, payload, ok, err := p.store.LatestSnapshot()
	if err != nil || !ok {
		return []byte(`[]`)
	}
	var previous Snapshot
	if json.Unmarshal(payload, &previous) != nil {
		return []byte(`[]`)
	}
	rawProviders := make([]json.RawMessage, 0, len(previous.Providers))
	for _, provider := range previous.Providers {
		if provider.ID == "demo" || len(provider.Raw) == 0 {
			continue
		}
		var raw map[string]any
		if json.Unmarshal(provider.Raw, &raw) != nil {
			continue
		}
		raw["stale"] = true
		encoded, err := json.Marshal(raw)
		if err == nil {
			rawProviders = append(rawProviders, encoded)
		}
	}
	if len(rawProviders) == 0 {
		return []byte(`[]`)
	}
	body, err := json.Marshal(struct {
		Providers []json.RawMessage `json:"providers"`
	}{Providers: rawProviders})
	if err != nil {
		return []byte(`[]`)
	}
	return body
}

// preserveLastKnownProviderUsage keeps one transient provider failure from
// erasing the other providers' fresh results or the failed provider's last
// useful windows. The provider remains stale so the event engine will not emit
// alerts from cached values, while the raw payload retains the diagnostic.
func (p *Poller) preserveLastKnownProviderUsage(snap *Snapshot) {
	_, payload, ok, err := p.store.LatestSnapshot()
	if err != nil || !ok {
		return
	}
	var previous Snapshot
	if err := json.Unmarshal(payload, &previous); err != nil {
		return
	}
	byID := make(map[string]Provider, len(previous.Providers))
	for _, provider := range previous.Providers {
		byID[provider.ID] = provider
	}
	for i := range snap.Providers {
		current := &snap.Providers[i]
		if current.ID == "demo" || current.Error == "" || len(current.Windows) != 0 {
			continue
		}
		prior, found := byID[current.ID]
		if !found || len(prior.Windows) == 0 {
			continue
		}
		current.Windows = prior.Windows
		current.Credits = prior.Credits
		current.Stale = true
		current.Error = ""
	}
}

type dispatchResult struct {
	Delivery      DemoDeliveryResult
	AlertsByEvent map[string]DeliveryCount
	Status        DemoStageStatus
	Detail        string
}

func (p *Poller) dispatch(ctx context.Context, events []Event, changed bool, targets []Device) dispatchResult {
	result := dispatchResult{AlertsByEvent: make(map[string]DeliveryCount), Status: DemoStageSkipped}
	if status, ok := p.notifier.(notifierStatus); ok && !status.Enabled() {
		result.Detail = "APNs disabled"
		return result
	}
	devices := targets
	if devices == nil {
		var err error
		devices, err = p.store.ListDevices()
		if err != nil {
			log.Printf("poller: list devices: %v", err)
			result.Status = DemoStageWarning
			result.Detail = err.Error()
			return result
		}
	}
	for _, ev := range events {
		count := result.AlertsByEvent[ev.Key]
		for _, d := range devices {
			if d.APNsToken == "" {
				continue
			}
			count.Attempted++
			result.Delivery.Alerts.Attempted++
			if err := p.notifier.SendAlert(ctx, d.APNsToken, ev); err != nil {
				log.Printf("poller: send alert to %s: %v", d.DeviceID, err)
				var apnsErr *APNSError
				if errors.As(err, &apnsErr) && apnsErr.TerminalDeviceToken() {
					if clearErr := p.store.ClearAPNsToken(d.DeviceID); clearErr != nil {
						log.Printf("poller: clear APNs token for %s: %v", d.DeviceID, clearErr)
					}
				}
				count.Failed++
				result.Delivery.Alerts.Failed++
			} else {
				count.Succeeded++
				result.Delivery.Alerts.Succeeded++
			}
		}
		result.AlertsByEvent[ev.Key] = count
		if count.Succeeded > 0 && ev.DangerPolicyFingerprint != "" {
			if err := p.store.RecordDangerDelivery(ev.WindowID, ev.DangerResetEpoch, ev.DangerPolicyFingerprint, time.Now().UTC()); err != nil {
				log.Printf("poller: persist danger delivery: %v", err)
			}
		}
	}
	if changed {
		for _, d := range devices {
			if d.WidgetToken == "" {
				continue
			}
			result.Delivery.WidgetRefresh.Attempted++
			if err := p.notifier.SendWidgetRefresh(ctx, d.WidgetToken); err != nil {
				log.Printf("poller: widget refresh to %s: %v", d.DeviceID, err)
				var apnsErr *APNSError
				if errors.As(err, &apnsErr) && apnsErr.TerminalDeviceToken() {
					if clearErr := p.store.ClearWidgetToken(d.DeviceID); clearErr != nil {
						log.Printf("poller: clear widget token for %s: %v", d.DeviceID, clearErr)
					}
				}
				result.Delivery.WidgetRefresh.Failed++
			} else {
				result.Delivery.WidgetRefresh.Succeeded++
			}
		}
	}
	attempted := result.Delivery.Alerts.Attempted + result.Delivery.WidgetRefresh.Attempted
	failed := result.Delivery.Alerts.Failed + result.Delivery.WidgetRefresh.Failed
	if attempted == 0 {
		result.Detail = "no eligible APNs tokens"
		return result
	}
	if failed > 0 {
		result.Status = DemoStageWarning
		result.Detail = fmt.Sprintf("%d of %d deliveries failed", failed, attempted)
		return result
	}
	result.Status = DemoStageOK
	return result
}

func newDemoPipelineResult(startedAt time.Time) DemoPipelineResult {
	result := DemoPipelineResult{StartedAt: startedAt, Stages: make([]DemoPipelineStage, len(demoPipelineStageIDs))}
	for i, id := range demoPipelineStageIDs {
		result.Stages[i] = DemoPipelineStage{ID: id, Status: DemoStageSkipped}
	}
	return result
}

func setDemoStage(result *DemoPipelineResult, id string, status DemoStageStatus, detail string, started time.Time) {
	for i := range result.Stages {
		if result.Stages[i].ID == id {
			result.Stages[i].Status = status
			result.Stages[i].Detail = detail
			result.Stages[i].DurationMS = time.Since(started).Milliseconds()
			return
		}
	}
}

func (p *Poller) failPoll(execution pollExecution, stage string, started time.Time, err error, snapshotChanged bool, action *DemoAction, state *DemoState) pollExecution {
	execution.poll.Error = err.Error()
	execution.poll.SnapshotChanged = snapshotChanged
	execution.demo.Success = false
	execution.demo.FailedStage = stage
	execution.demo.SnapshotChanged = snapshotChanged
	execution.demo.Error = err.Error()
	execution.demo.CompletedAt = time.Now().UTC()
	setDemoStage(&execution.demo, stage, DemoStageFailed, err.Error(), started)
	p.persistDemoExecution(&execution, action, state)
	return execution
}

func (p *Poller) persistDemoExecution(execution *pollExecution, action *DemoAction, state *DemoState) {
	if action != nil {
		if state != nil {
			copy := *state
			copy.LastDemoRunID = action.ID
			state = &copy
		}
		id, err := p.store.CommitDemoAction(DemoActionCommit{
			State: state, Run: &execution.demo, Events: demoEventRecords(execution, true),
			Audit: DemoAuditEntry{DemoRunID: action.ID, Identity: action.Identity, Route: action.Route, Action: action.Route, Result: demoActionResult(execution.demo), Status: httpStatusForDemo(execution.demo), CreatedAt: action.CreatedAt},
		})
		if err != nil {
			log.Printf("poller: commit demo action: %v", err)
			execution.demo.Success = false
			execution.demo.Error = joinErrors(execution.demo.Error, "persist demo action")
		} else {
			execution.demo.ID = id
		}
		return
	}
	persistStarted := time.Now()
	records := demoEventRecords(execution, true)
	id, err := p.store.SaveDemoExecution(execution.demo, records)
	if err == nil {
		execution.demo.ID = id
		return
	}

	log.Printf("poller: save demo execution: %v", err)
	persistErr := fmt.Errorf("persist demo execution: %w", err)
	if execution.demo.Success {
		execution.demo.Success = false
		execution.demo.FailedStage = "snapshot_persisted"
		setDemoStage(&execution.demo, "snapshot_persisted", DemoStageFailed, persistErr.Error(), persistStarted)
	}
	execution.demo.Error = joinErrors(execution.demo.Error, persistErr.Error())

	// The successful run and its feed rolled back together. Make one atomic
	// best-effort attempt to durably record the persistence failure itself.
	id, retryErr := p.store.SaveDemoExecution(execution.demo, demoEventRecords(execution, false))
	if retryErr == nil {
		execution.demo.ID = id
		return
	}
	log.Printf("poller: save demo persistence failure: %v", retryErr)
	execution.demo.Error = joinErrors(execution.demo.Error, fmt.Sprintf("persist demo failure record: %v", retryErr))
}

func demoActionResult(run DemoPipelineResult) string {
	if run.Success {
		return "ok"
	}
	return "failed"
}
func httpStatusForDemo(run DemoPipelineResult) int {
	if run.Success {
		return 200
	}
	return 502
}

func demoEventRecords(execution *pollExecution, includeOutcomes bool) []DemoEvent {
	records := make([]DemoEvent, 0, len(execution.outcomes)+1)
	if includeOutcomes {
		for _, outcome := range execution.outcomes {
			if outcome.Event.ProviderID != "demo" && !strings.HasPrefix(outcome.Event.Key, "demo.") {
				continue
			}
			before, after := outcome.Before, outcome.After
			records = append(records, DemoEventRecord{
				Key:          outcome.Event.Key,
				Type:         outcome.Event.Type,
				CreatedAt:    execution.demo.CompletedAt,
				WindowID:     outcome.Event.WindowID,
				Before:       &before,
				After:        &after,
				Deduplicated: outcome.Deduplicated,
				Delivery: DemoDeliveryResult{
					Alerts: execution.dispatched.AlertsByEvent[outcome.Event.Key],
				},
			})
		}
	}
	if execution.demo.Success {
		records = append(records, DemoEventRecord{
			Key:       fmt.Sprintf("demo.manual_poll:%d", execution.demo.StartedAt.UnixNano()),
			Type:      "manual_poll",
			CreatedAt: execution.demo.CompletedAt,
			Delivery:  execution.demo.Delivery,
		})
	} else {
		records = append(records, DemoEventRecord{
			Key:       fmt.Sprintf("demo.pipeline_error:%d", execution.demo.StartedAt.UnixNano()),
			Type:      "pipeline_error",
			CreatedAt: execution.demo.CompletedAt,
		})
	}
	return records
}

func joinErrors(current, next string) string {
	if current == "" {
		return next
	}
	return current + "; " + next
}

func (p *Poller) snapshotChanged(snap Snapshot) bool {
	_, payload, ok, err := p.store.LatestSnapshot()
	if err != nil || !ok {
		return true
	}
	var prev Snapshot
	if err := json.Unmarshal(payload, &prev); err != nil {
		return true
	}
	a, _ := json.Marshal(prev.Providers)
	b, _ := json.Marshal(snap.Providers)
	return string(a) != string(b)
}

func (p *Poller) markStale() {
	fetchedAt, payload, ok, err := p.store.LatestSnapshot()
	if err != nil || !ok {
		return
	}
	var snap Snapshot
	if err := json.Unmarshal(payload, &snap); err != nil {
		return
	}
	if snap.Stale {
		return
	}
	snap.Stale = true
	clearForecasts(&snap)
	updated, err := json.Marshal(snap)
	if err != nil {
		return
	}
	if err := p.store.SaveSnapshot(fetchedAt, updated); err != nil {
		log.Printf("poller: mark stale: %v", err)
	}
}
