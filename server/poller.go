package server

import (
	"context"
	"encoding/json"
	"log"
	"time"
)

type Poller struct {
	store    *Store
	codexbar *CodexBarClient
	engine   *EventEngine
	notifier Notifier
	api      *API
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
	p.pollOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(p.interval()):
			p.pollOnce(ctx)
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

func (p *Poller) pollOnce(ctx context.Context) {
	now := time.Now().UTC()

	settings, err := loadSettings(p.store)
	if err != nil {
		log.Printf("poller: load settings: %v", err)
		p.api.RecordPollResult(now, false)
		return
	}

	body, err := p.codexbar.Fetch(ctx)
	if err != nil {
		log.Printf("poller: fetch failed, keeping last snapshot: %v", err)
		p.markStale()
		p.api.RecordPollResult(now, false)
		return
	}

	snap, err := Normalize(body, settings.PollIntervalMinutes, now)
	if err != nil {
		log.Printf("poller: normalize failed: %v", err)
		p.api.RecordPollResult(now, false)
		return
	}

	changed := p.snapshotChanged(snap)
	payload, err := json.Marshal(snap)
	if err != nil {
		log.Printf("poller: marshal snapshot: %v", err)
		p.api.RecordPollResult(now, false)
		return
	}
	if err := p.store.SaveSnapshot(now, payload); err != nil {
		log.Printf("poller: save snapshot: %v", err)
		p.api.RecordPollResult(now, false)
		return
	}

	events, err := p.engine.Process(snap, settings, now)
	if err != nil {
		log.Printf("poller: process events: %v", err)
		p.api.RecordPollResult(now, true)
		return
	}

	p.dispatch(ctx, events, changed)
	p.api.RecordPollResult(now, true)
}

func (p *Poller) dispatch(ctx context.Context, events []Event, changed bool) {
	devices, err := p.store.ListDevices()
	if err != nil {
		log.Printf("poller: list devices: %v", err)
		return
	}
	for _, ev := range events {
		for _, d := range devices {
			if d.APNsToken == "" {
				continue
			}
			if err := p.notifier.SendAlert(ctx, d.APNsToken, ev); err != nil {
				log.Printf("poller: send alert to %s: %v", d.DeviceID, err)
			}
		}
	}
	if changed {
		for _, d := range devices {
			if d.WidgetToken == "" {
				continue
			}
			if err := p.notifier.SendWidgetRefresh(ctx, d.WidgetToken); err != nil {
				log.Printf("poller: widget refresh to %s: %v", d.DeviceID, err)
			}
		}
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
