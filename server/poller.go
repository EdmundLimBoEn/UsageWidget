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
	DemoRunID          string              `json:"demoRunID,omitempty"`
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
	p.PollNow(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(p.interval()):
			p.PollNow(ctx)
		}
	}
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
	return p.poll(ctx, false, nil, "", nil, nil).poll
}

// PollDemoNow runs persisted demo state through the same serialized poll
// pipeline as real and scheduled polls, recording the detailed outcome.
func (p *Poller) PollDemoNow(ctx context.Context, expectedRevision int64, demoRunID string, targets []Device) (DemoPipelineResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if targets == nil {
		targets = []Device{}
	}
	state, err := p.store.LoadDemoState()
	if err != nil {
		return p.poll(ctx, true, targets, demoRunID, nil, err).demo, nil
	}
	if expectedRevision != 0 && expectedRevision != state.Revision {
		return DemoPipelineResult{}, fmt.Errorf("%w: current %d", ErrDemoRevisionConflict, state.Revision)
	}
	return p.poll(ctx, true, targets, demoRunID, &state, nil).demo, nil
}

// pollOnce is retained for package-internal compatibility and participates in
// the same serialization as every other poll entry point.
func (p *Poller) pollOnce(ctx context.Context) PollResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.poll(ctx, false, nil, "", nil, nil).poll
}

type pollExecution struct {
	poll       PollResult
	demo       DemoPipelineResult
	outcomes   []EventOutcome
	dispatched dispatchResult
}

func (p *Poller) poll(ctx context.Context, recordDemo bool, targets []Device, demoRunID string, demoState *DemoState, demoStateErr error) pollExecution {
	now := time.Now().UTC()
	execution := pollExecution{poll: PollResult{PolledAt: now}}
	if recordDemo {
		execution.demo = newDemoPipelineResult(now)
		execution.demo.DemoRunID = demoRunID
		started := time.Now()
		if demoStateErr != nil {
			return p.failPoll(execution, "demo_state", started, demoStateErr, false)
		}
		setDemoStage(&execution.demo, "demo_state", DemoStageOK, "", started)
		execution = p.pollWithInputs(ctx, execution, now, demoState, targets)
	} else {
		execution = p.pollWithInputs(ctx, execution, now, nil, nil)
	}
	return execution
}

func (p *Poller) pollWithInputs(ctx context.Context, execution pollExecution, now time.Time, demoState *DemoState, targets []Device) pollExecution {
	recordDemo := demoState != nil
	settings, err := loadSettings(p.store)
	if err != nil {
		log.Printf("poller: load settings: %v", err)
		if recordDemo {
			return p.failPoll(execution, "normalize", time.Now(), err, false)
		}
		p.recordPollResult(now, false)
		execution.poll.Error = err.Error()
		return execution
	}

	normalizeStarted := time.Now()
	body, err := p.codexbar.Fetch(ctx)
	if err != nil {
		log.Printf("poller: fetch failed, keeping last snapshot: %v", err)
		if recordDemo {
			return p.failPoll(execution, "normalize", normalizeStarted, fmt.Errorf("fetch upstream: %w", err), false)
		}
		p.markStale()
		p.recordPollResult(now, false)
		execution.poll.Error = err.Error()
		return execution
	}
	if recordDemo {
		body, err = InjectDemoProvider(body, *demoState)
		if err != nil {
			log.Printf("poller: inject demo provider: %v", err)
			return p.failPoll(execution, "normalize", normalizeStarted, err, false)
		}
	}

	snap, err := Normalize(body, settings.PollIntervalMinutes, now)
	if err != nil {
		log.Printf("poller: normalize failed: %v", err)
		if recordDemo {
			return p.failPoll(execution, "normalize", normalizeStarted, err, false)
		}
		p.recordPollResult(now, false)
		execution.poll.Error = err.Error()
		return execution
	}
	if recordDemo {
		setDemoStage(&execution.demo, "normalize", DemoStageOK, "", normalizeStarted)
	}

	changed := p.snapshotChanged(snap)
	snapshotStarted := time.Now()
	payload, err := json.Marshal(snap)
	if err != nil {
		log.Printf("poller: marshal snapshot: %v", err)
		if recordDemo {
			return p.failPoll(execution, "snapshot_persisted", snapshotStarted, err, false)
		}
		p.recordPollResult(now, false)
		execution.poll.Error = err.Error()
		return execution
	}
	if err := p.store.SaveSnapshot(now, payload); err != nil {
		log.Printf("poller: save snapshot: %v", err)
		if recordDemo {
			return p.failPoll(execution, "snapshot_persisted", snapshotStarted, err, false)
		}
		p.recordPollResult(now, false)
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
			return p.failPoll(execution, "event_engine", eventStarted, err, changed)
		}
		p.recordPollResult(now, true)
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
	if recordDemo {
		execution.demo.Delivery = execution.dispatched.Delivery
		setDemoStage(&execution.demo, "apns", execution.dispatched.Status, execution.dispatched.Detail, dispatchStarted)
		execution.demo.Success = true
		execution.demo.CompletedAt = time.Now().UTC()
		p.persistDemoExecution(&execution)
	}
	p.recordPollResult(now, true)
	execution.poll.Success = true
	execution.poll.Events = len(processed.Emitted)
	execution.poll.SnapshotChanged = changed
	return execution
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
				count.Failed++
				result.Delivery.Alerts.Failed++
			} else {
				count.Succeeded++
				result.Delivery.Alerts.Succeeded++
			}
		}
		result.AlertsByEvent[ev.Key] = count
	}
	if changed {
		for _, d := range devices {
			if d.WidgetToken == "" {
				continue
			}
			result.Delivery.WidgetRefresh.Attempted++
			if err := p.notifier.SendWidgetRefresh(ctx, d.WidgetToken); err != nil {
				log.Printf("poller: widget refresh to %s: %v", d.DeviceID, err)
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

func (p *Poller) failPoll(execution pollExecution, stage string, started time.Time, err error, snapshotChanged bool) pollExecution {
	execution.poll.Error = err.Error()
	execution.poll.SnapshotChanged = snapshotChanged
	execution.demo.Success = false
	execution.demo.FailedStage = stage
	execution.demo.SnapshotChanged = snapshotChanged
	execution.demo.Error = err.Error()
	execution.demo.CompletedAt = time.Now().UTC()
	setDemoStage(&execution.demo, stage, DemoStageFailed, err.Error(), started)
	p.recordPollResult(execution.poll.PolledAt, false)
	p.persistDemoExecution(&execution)
	return execution
}

func (p *Poller) persistDemoExecution(execution *pollExecution) {
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

func (p *Poller) recordPollResult(at time.Time, success bool) {
	if p.api != nil {
		p.api.RecordPollResult(at, success)
	}
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
	updated, err := json.Marshal(snap)
	if err != nil {
		return
	}
	if err := p.store.SaveSnapshot(fetchedAt, updated); err != nil {
		log.Printf("poller: mark stale: %v", err)
	}
}
