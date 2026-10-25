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

// Allow the full collection budget plus time to persist and dispatch the result.
const sharedPollTimeout = crossUsageFetchTimeout + 50*time.Second

const widgetDeliveryBudget = 30 * time.Second

type PollResult struct {
	retryable       bool      // Internal scheduling metadata, never a public diagnostic.
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
	source   UsageSource
	engine   *EventEngine
	notifier Notifier
	api      *API

	mu       sync.Mutex
	inFlight *pollCall
}

func NewPoller(store *Store, source UsageSource, notifier Notifier, api *API) *Poller {
	return &Poller{
		store:    store,
		source:   source,
		engine:   NewEventEngine(store),
		notifier: notifier,
		api:      api,
	}
}

func (p *Poller) Run(ctx context.Context) {
	for {
		cycleStarted := time.Now()
		result := p.PollNow(ctx)
		if !result.Success && result.retryable {
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

// pollCall publishes an immutable result when done is closed.
type pollCall struct {
	done   chan struct{}
	result PollResult
}

func (c *pollCall) wait(ctx context.Context) PollResult {
	select {
	case <-c.done:
		return c.result
	case <-ctx.Done():
		return PollResult{Error: ctx.Err().Error()}
	}
}

// PollNow runs one poll cycle. Concurrent callers share the in-flight result.
func (p *Poller) PollNow(ctx context.Context) PollResult {
	p.mu.Lock()
	if call := p.inFlight; call != nil {
		p.mu.Unlock()
		return call.wait(ctx)
	}
	call := &pollCall{done: make(chan struct{})}
	p.inFlight = call
	p.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), sharedPollTimeout)
		defer cancel()
		call.result = p.pollOnceUnlocked(ctx)
		p.mu.Lock()
		p.inFlight = nil
		close(call.done)
		p.mu.Unlock()
	}()
	return call.wait(ctx)
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

	body, err := p.source.Fetch(ctx)
	if err != nil {
		log.Printf("poller: fetch failed, keeping last snapshot: %v", err)
		p.markStale()
		result.retryable = isRetryablePollError(err)
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

	delivery := p.dispatch(ctx, changed)
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
	snapshot.Providers = projectCatalogProviders(snapshot.Providers)
	previous.Providers = projectCatalogProviders(previous.Providers)
	byID := make(map[string]Provider, len(previous.Providers))
	for _, provider := range previous.Providers {
		byID[provider.ID] = provider
	}
	seen := make(map[string]bool, len(snapshot.Providers))
	for i := range snapshot.Providers {
		current := &snapshot.Providers[i]
		seen[current.ID] = true
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
	for _, prior := range previous.Providers {
		if seen[prior.ID] || len(prior.Windows) == 0 || !keepPlanProvider(prior) {
			continue
		}
		prior.Stale = true
		prior.Error = ""
		snapshot.Providers = append(snapshot.Providers, prior)
	}
}

type dispatchResult struct {
	Delivery DeliveryResult
	Status   DeliveryStatus
	Detail   string
}

func (p *Poller) dispatch(ctx context.Context, changed bool) dispatchResult {
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

	pending, err := p.store.pendingAlerts()
	if err != nil {
		result.Status = DeliveryWarning
		result.Detail = "could not load pending alerts"
		return result
	}
	settings, err := loadSettings(p.store)
	if err != nil {
		result.Status = DeliveryWarning
		result.Detail = "could not load notification settings"
		return result
	}
	_, payload, hasSnapshot, err := p.store.LatestSnapshot()
	var snapshot Snapshot
	if err != nil || (hasSnapshot && json.Unmarshal(payload, &snapshot) != nil) {
		result.Status = DeliveryWarning
		result.Detail = "could not load snapshot for alert validation"
		return result
	}
	hidden := make(map[string]bool, len(settings.HiddenProviders))
	for _, id := range settings.HiddenProviders {
		hidden[canonicalProviderID(id)] = true
	}
	alertDeadline := time.Now().Add(sharedPollTimeout)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(alertDeadline) {
		alertDeadline = deadline
	}
	if changed {
		// Preserve widget headroom even after a slow collection. If that is
		// all the time left, leave the entire alert batch for the next poll.
		alertDeadline = alertDeadline.Add(-widgetDeliveryBudget)
	}
	alertCtx, cancelAlerts := context.WithDeadline(ctx, alertDeadline)
	defer cancelAlerts()
	var deferredAlerts []pendingAlert
	for _, alert := range pending {
		if alertCtx.Err() != nil {
			break
		}
		event := alert.Event
		rule := settings.EffectiveRule(event.ProviderID, event.WindowID)
		if !settings.NotificationsEnabled || !rule.Enabled || hidden[canonicalProviderID(event.ProviderID)] ||
			(event.Type == "danger_reminder" && rule.RepeatIntervalMinutes <= 0) {
			// Discard alerts made ineligible by current preferences so they cannot
			// reappear later when notifications are enabled again.
			if err := p.store.acknowledgeAlert(event.Key, alert.DeviceID); err != nil {
				log.Printf("poller: discard ineligible alert: %v", err)
			}
			continue
		}
		discard, deferDelivery := validateQueuedAlert(event, snapshot, hasSnapshot, rule, time.Now())
		if discard {
			if err := p.store.acknowledgeAlert(event.Key, alert.DeviceID); err != nil {
				log.Printf("poller: discard obsolete alert: %v", err)
			}
			continue
		}
		if deferDelivery {
			deferredAlerts = append(deferredAlerts, alert)
			continue
		}
		event.Silent = settings.QuietHours.Contains(time.Now())
		result.Delivery.Alerts.Attempted++
		err := p.notifier.SendAlert(alertCtx, alert.Token, event)
		if err != nil {
			result.Delivery.Alerts.Failed++
			var apnsErr *APNSError
			if errors.As(err, &apnsErr) && apnsErr.TerminalDeviceToken() {
				if err := p.store.rejectAlert(alert.DeviceID, alert.Token); err != nil {
					log.Printf("poller: reject alert: %v", err)
				}
			}
			// Rotate any surviving delivery, including one inherited by a
			// replacement token, so persistent failures cannot monopolize a batch.
			deferredAlerts = append(deferredAlerts, alert)
			continue
		}
		result.Delivery.Alerts.Succeeded++
		if event.DangerPolicyFingerprint != "" {
			if err := p.store.RecordDangerDelivery(alert.DeviceID, event.WindowID, event.DangerResetEpoch, event.DangerPolicyFingerprint, time.Now().UTC()); err != nil {
				log.Printf("poller: persist danger delivery: %v", err)
			}
		}
		if err := p.store.acknowledgeAlert(event.Key, alert.DeviceID); err != nil {
			log.Printf("poller: acknowledge alert: %v", err)
		}
	}

	if err := p.store.deferAlerts(deferredAlerts); err != nil {
		log.Printf("poller: defer alerts: %v", err)
	}

	if changed {
		widgetCtx, cancelWidgets := context.WithTimeout(ctx, widgetDeliveryBudget)
		defer cancelWidgets()
		remainingWidgets := 0
		for _, device := range devices {
			if device.WidgetToken != "" {
				remainingWidgets++
			}
		}
		widgetDeadline, _ := widgetCtx.Deadline()
		for _, device := range devices {
			if widgetCtx.Err() != nil {
				break
			}
			if device.WidgetToken == "" {
				continue
			}
			// Share the remaining batch time fairly. Slow requests must leave
			// time for later devices, whose refreshes are not queued for retry.
			deviceCtx, cancelDevice := context.WithTimeout(widgetCtx, time.Until(widgetDeadline)/time.Duration(remainingWidgets))
			remainingWidgets--
			result.Delivery.WidgetRefresh.Attempted++
			err := p.notifier.SendWidgetRefresh(deviceCtx, device.WidgetToken)
			cancelDevice()
			if err != nil {
				log.Printf("poller: widget refresh to %s: %v", device.DeviceID, err)
				var apnsErr *APNSError
				if errors.As(err, &apnsErr) && apnsErr.TerminalDeviceToken() {
					if clearErr := p.store.ClearWidgetToken(device.DeviceID, device.WidgetToken); clearErr != nil {
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

// Validate immediately before sending: a reset can pass while earlier alerts
// are in flight. Stale data cannot establish whether a queued alert is valid.
func validateQueuedAlert(event Event, snapshot Snapshot, hasSnapshot bool, rule AlertRule, now time.Time) (discard, deferDelivery bool) {
	threshold := event.Type == "early_threshold" || event.Type == "danger_threshold" || event.Type == "danger_reminder"
	if threshold && event.ResetsAt != nil && !event.ResetsAt.After(now) {
		return true, false
	}
	if !hasSnapshot {
		return false, false
	}
	for _, provider := range snapshot.Providers {
		if canonicalProviderID(provider.ID) != canonicalProviderID(event.ProviderID) {
			continue
		}
		if snapshot.Stale || provider.Stale || provider.Error != "" {
			return false, true
		}
		if !threshold {
			return false, false
		}
		for _, window := range provider.Windows {
			if window.ID != event.WindowID {
				continue
			}
			if (event.ResetsAt == nil) != (window.ResetsAt == nil) ||
				(event.ResetsAt != nil && !event.ResetsAt.Equal(*window.ResetsAt)) {
				return true, false
			}
			if event.Type == "early_threshold" {
				return window.UsedPercent < rule.EarlyThresholdPct, false
			}
			return window.RemainingPercent > rule.DangerThresholdPct, false
		}
		return true, false
	}
	return false, true
}
