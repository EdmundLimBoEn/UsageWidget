package server

import (
	"fmt"
	"time"
)

type Event struct {
	Key              string
	Type             string
	Title            string
	ProviderID       string
	ProviderName     string
	WindowID         string
	WindowTitle      string
	UsedPercent      float64
	RemainingPercent float64
	ResetsAt         *time.Time
}

type EventEngine struct {
	store *Store
}

func NewEventEngine(store *Store) *EventEngine {
	return &EventEngine{store: store}
}

func (e *EventEngine) Process(snap Snapshot, s Settings, now time.Time) ([]Event, error) {
	hidden := make(map[string]bool, len(s.HiddenProviders))
	for _, id := range s.HiddenProviders {
		hidden[id] = true
	}

	var out []Event
	for _, p := range snap.Providers {
		if hidden[p.ID] {
			continue
		}

		for _, w := range p.Windows {
			prev, had, err := e.store.GetWindowState(w.ID)
			if err != nil {
				return nil, err
			}
			if had && s.NotificationsEnabled {
				for _, ev := range detectWindowEvents(prev, w, p, s, now) {
					claimed, err := e.claim(ev.Key)
					if err != nil {
						return nil, err
					}
					if claimed {
						out = append(out, ev)
					}
				}
			}
			if err := e.store.SetWindowState(WindowState{
				WindowID:    w.ID,
				UsedPercent: w.UsedPercent,
				ResetsAt:    w.ResetsAt,
			}); err != nil {
				return nil, err
			}
		}

		if p.Credits != nil {
			creditsID := p.ID + "#credits"
			prev, had, err := e.store.GetWindowState(creditsID)
			if err != nil {
				return nil, err
			}
			if had && s.NotificationsEnabled && prev.CreditsAvailable != nil && p.Credits.AvailableCount > *prev.CreditsAvailable {
				ev := Event{
					Key:          fmt.Sprintf("credits:%s:%d", p.ID, p.Credits.AvailableCount),
					Type:         "credits_increase",
					Title:        "Reset credits available",
					ProviderID:   p.ID,
					ProviderName: p.Name,
				}
				claimed, err := e.claim(ev.Key)
				if err != nil {
					return nil, err
				}
				if claimed {
					out = append(out, ev)
				}
			}
			count := p.Credits.AvailableCount
			if err := e.store.SetWindowState(WindowState{
				WindowID:         creditsID,
				CreditsAvailable: &count,
			}); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func (e *EventEngine) claim(key string) (bool, error) {
	notified, err := e.store.EventNotified(key)
	if err != nil {
		return false, err
	}
	if notified {
		return false, nil
	}
	if err := e.store.RecordEvent(key); err != nil {
		return false, err
	}
	return true, nil
}

func detectWindowEvents(prev WindowState, w Window, p Provider, s Settings, now time.Time) []Event {
	var evs []Event

	if prev.UsedPercent < s.EarlyThresholdPct && w.UsedPercent >= s.EarlyThresholdPct {
		evs = append(evs, mkEvent("early_threshold", "Approaching limit", eventKey("early", w.ID, w.ResetsAt), p, w))
	}

	prevRemaining := 100 - prev.UsedPercent
	if prevRemaining > s.DangerThresholdPct && w.RemainingPercent <= s.DangerThresholdPct {
		evs = append(evs, mkEvent("danger_threshold", "Almost out", eventKey("danger", w.ID, w.ResetsAt), p, w))
	}

	if prev.ResetsAt != nil {
		if !prev.ResetsAt.After(now) {
			resetChanged := w.ResetsAt == nil || !w.ResetsAt.Equal(*prev.ResetsAt)
			usageDropped := w.UsedPercent < prev.UsedPercent
			if resetChanged || usageDropped {
				evs = append(evs, mkEvent("reset", "Limit reset", eventKey("reset", w.ID, prev.ResetsAt), p, w))
			}
		} else if isSurpriseReset(prev.UsedPercent, w.UsedPercent) {
			title := "Surprise reset"
			if p.ID == "codex" {
				title = "Tibo blessed"
			}
			evs = append(evs, mkEvent("tibo_reset", title, eventKey("tibo", w.ID, prev.ResetsAt), p, w))
		}
	}

	return evs
}

func isSurpriseReset(prevUsed, curUsed float64) bool {
	if prevUsed-curUsed >= 50 {
		return true
	}
	return prevUsed >= 20 && curUsed <= 5
}

func mkEvent(typ, title, key string, p Provider, w Window) Event {
	return Event{
		Key:              key,
		Type:             typ,
		Title:            title,
		ProviderID:       p.ID,
		ProviderName:     p.Name,
		WindowID:         w.ID,
		WindowTitle:      w.Title,
		UsedPercent:      w.UsedPercent,
		RemainingPercent: w.RemainingPercent,
		ResetsAt:         w.ResetsAt,
	}
}

func eventKey(kind, windowID string, resetsAt *time.Time) string {
	token := "epoch"
	if resetsAt != nil {
		token = resetsAt.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("%s:%s:%s", kind, windowID, token)
}
