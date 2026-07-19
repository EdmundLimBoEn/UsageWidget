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

type DeliveryStatus string

const (
	DeliveryOK      DeliveryStatus = "ok"
	DeliveryWarning DeliveryStatus = "warning"
	DeliverySkipped DeliveryStatus = "skipped"
)

type DeliveryCount struct {
	Attempted int `json:"attempted"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}

type DeliveryResult struct {
	Alerts        DeliveryCount `json:"alerts"`
	WidgetRefresh DeliveryCount `json:"widgetRefresh"`
}

type Poller struct {
	store    *Store
	codexbar *CodexBarClient
	engine   *EventEngine
	notifier Notifier
	api      *API

	mu sync.Mutex
}

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
	settings, err := loadSettings(p.store)
	if err != nil || !validPollIntervals[settings.PollIntervalMinutes] {
		return 5 * time.Minute
	}
	return time.Duration(settings.PollIntervalMinutes) * time.Minute
}

// PollNow runs one poll cycle. Concurrent callers serialize on p.mu.
func (p *Poller) PollNow(ctx context.Context) PollResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pollOnceUnlocked(ctx)
}

// pollOnce is retained for package-internal tests and shares PollNow's lock.
func (p *Poller) pollOnce(ctx context.Context) PollResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pollOnceUnlocked(ctx)
}

func (p *Poller) pollOnceUnlocked(ctx context.Context) (result PollResult) {
	started := time.Now()
	result.PolledAt = time.Now().UTC()
	defer func() {
		result.DurationMS = time.Since(started).Milliseconds()
		if err := p.store.SavePollOutcome(result); err != nil {
			log.Printf("poller: persist poll outcome: %v", err)
		}
		if p.api != nil {
			p.api.RecordPollOutcome(result)
		}
	}()

	settings, err := loadSettings(p.store)
	if err != nil {
		log.Printf("poller: load settings: %v", err)
		result.Error = err.Error()
		return result
	}

	body, err := p.codexbar.Fetch(ctx)
	if err != nil {
		log.Printf("poller: fetch failed, keeping last snapshot: %v", err)
		p.markStale()
		result.Error = err.Error()
		return result
	}

	snapshot, err := Normalize(body, settings.PollIntervalMinutes, result.PolledAt)
	if err != nil {
		log.Printf("poller: normalize failed: %v", err)
		result.Error = err.Error()
		return result
	}
	p.preserveLastKnownProviderUsage(&snapshot)

	changed := p.snapshotChanged(snapshot)
	if err := p.store.SaveSnapshotWithForecasts(&snapshot); err != nil {
		log.Printf("poller: save snapshot: %v", err)
		result.Error = err.Error()
		return result
	}

	processed, err := p.engine.ProcessDetailed(snapshot, settings, result.PolledAt)
	if err != nil {
		log.Printf("poller: process events: %v", err)
		result.Success = true
		result.SnapshotChanged = changed
		result.Error = err.Error()
		return result
	}

	delivery := p.dispatch(ctx, processed.Emitted, changed)
	if p.api != nil && changed {
		p.api.RecordWidgetDelivery(delivery.Delivery.WidgetRefresh, delivery.Status, delivery.Detail)
	}
	result.Success = true
	result.Events = len(processed.Emitted)
	result.SnapshotChanged = changed
	return result
}

func (p *Poller) setNextPollAt(at time.Time) {
	if p.api != nil {
		p.api.SetNextPollAt(at.UTC())
	}
}

// preserveLastKnownProviderUsage keeps one transient provider failure from
// erasing fresh results from other providers or the failed provider's last
// useful windows. The provider remains stale so cached values cannot alert.
func (p *Poller) preserveLastKnownProviderUsage(snapshot *Snapshot) {
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
	for i := range snapshot.Providers {
		current := &snapshot.Providers[i]
		if current.Error == "" || len(current.Windows) != 0 {
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
	Delivery DeliveryResult
	Status   DeliveryStatus
	Detail   string
}

func (p *Poller) dispatch(ctx context.Context, events []Event, changed bool) dispatchResult {
	result := dispatchResult{Status: DeliverySkipped}
	if status, ok := p.notifier.(notifierStatus); ok && !status.Enabled() {
		result.Detail = "APNs disabled"
		return result
	}
	devices, err := p.store.ListDevices()
	if err != nil {
		log.Printf("poller: list devices: %v", err)
		result.Status = DeliveryWarning
		result.Detail = err.Error()
		return result
	}

	for _, event := range events {
		var succeeded int
		for _, device := range devices {
			if device.APNsToken == "" {
				continue
			}
			result.Delivery.Alerts.Attempted++
			if err := p.notifier.SendAlert(ctx, device.APNsToken, event); err != nil {
				log.Printf("poller: send alert to %s: %v", device.DeviceID, err)
				var apnsErr *APNSError
				if errors.As(err, &apnsErr) && apnsErr.TerminalDeviceToken() {
					if clearErr := p.store.ClearAPNsToken(device.DeviceID); clearErr != nil {
						log.Printf("poller: clear APNs token for %s: %v", device.DeviceID, clearErr)
					}
				}
				result.Delivery.Alerts.Failed++
				continue
			}
			result.Delivery.Alerts.Succeeded++
			succeeded++
		}
		if succeeded > 0 && event.DangerPolicyFingerprint != "" {
			if err := p.store.RecordDangerDelivery(event.WindowID, event.DangerResetEpoch, event.DangerPolicyFingerprint, time.Now().UTC()); err != nil {
				log.Printf("poller: persist danger delivery: %v", err)
			}
		}
	}

	if changed {
		for _, device := range devices {
			if device.WidgetToken == "" {
				continue
			}
			result.Delivery.WidgetRefresh.Attempted++
			if err := p.notifier.SendWidgetRefresh(ctx, device.WidgetToken); err != nil {
				log.Printf("poller: widget refresh to %s: %v", device.DeviceID, err)
				var apnsErr *APNSError
				if errors.As(err, &apnsErr) && apnsErr.TerminalDeviceToken() {
					if clearErr := p.store.ClearWidgetToken(device.DeviceID); clearErr != nil {
						log.Printf("poller: clear widget token for %s: %v", device.DeviceID, clearErr)
					}
				}
				result.Delivery.WidgetRefresh.Failed++
				continue
			}
			result.Delivery.WidgetRefresh.Succeeded++
		}
	}

	attempted := result.Delivery.Alerts.Attempted + result.Delivery.WidgetRefresh.Attempted
	failed := result.Delivery.Alerts.Failed + result.Delivery.WidgetRefresh.Failed
	if attempted == 0 {
		result.Detail = "no eligible APNs tokens"
		return result
	}
	if failed > 0 {
		result.Status = DeliveryWarning
		result.Detail = fmt.Sprintf("%d of %d deliveries failed", failed, attempted)
		return result
	}
	result.Status = DeliveryOK
	return result
}

func (p *Poller) snapshotChanged(snapshot Snapshot) bool {
	_, payload, ok, err := p.store.LatestSnapshot()
	if err != nil || !ok {
		return true
	}
	var previous Snapshot
	if err := json.Unmarshal(payload, &previous); err != nil {
		return true
	}
	a, _ := json.Marshal(previous.Providers)
	b, _ := json.Marshal(snapshot.Providers)
	return string(a) != string(b)
}

func (p *Poller) markStale() {
	fetchedAt, payload, ok, err := p.store.LatestSnapshot()
	if err != nil || !ok {
		return
	}
	var snapshot Snapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil || snapshot.Stale {
		return
	}
	snapshot.Stale = true
	clearForecasts(&snapshot)
	updated, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	if err := p.store.SaveSnapshot(fetchedAt, updated); err != nil {
		log.Printf("poller: mark stale: %v", err)
	}
}
