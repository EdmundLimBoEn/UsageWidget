package server

import (
	"fmt"
	"time"
)

type Event struct {
	Key                     string
	Type                    string
	Title                   string
	ProviderID              string
	ProviderName            string
	WindowID                string
	WindowTitle             string
	UsedPercent             float64
	RemainingPercent        float64
	ResetsAt                *time.Time
	Silent                  bool
	DangerPolicyFingerprint string
	DangerResetEpoch        string
}

type EventValue struct {
	UsedPercent      *float64   `json:"usedPercent,omitempty"`
	ResetsAt         *time.Time `json:"resetsAt,omitempty"`
	CreditsAvailable *int       `json:"creditsAvailable,omitempty"`
}

type EventOutcome struct {
	Event        Event
	Before       EventValue
	After        EventValue
	Deduplicated bool
}

type EventProcessResult struct {
	Emitted  []Event
	Outcomes []EventOutcome
}

type EventEngine struct {
	store *Store
}

func NewEventEngine(store *Store) *EventEngine {
	return &EventEngine{store: store}
}

func (e *EventEngine) Process(snap Snapshot, s Settings, now time.Time) ([]Event, error) {
	result, err := e.ProcessDetailed(snap, s, now)
	return result.Emitted, err
}

func (e *EventEngine) ProcessDetailed(snap Snapshot, s Settings, now time.Time) (EventProcessResult, error) {
	hidden := make(map[string]bool, len(s.HiddenProviders))
	for _, id := range s.HiddenProviders {
		hidden[id] = true
	}

	var result EventProcessResult
	for _, p := range snap.Providers {
		if hidden[p.ID] || p.Stale || p.Error != "" {
			continue
		}
		providerRule := s.EffectiveRule(p.ID, "")

		for _, w := range p.Windows {
			rule := s.EffectiveRule(p.ID, w.ID)
			prev, had, err := e.store.GetWindowState(w.ID)
			if err != nil {
				return EventProcessResult{}, err
			}
			if had && s.NotificationsEnabled && rule.Enabled {
				dangerEmitted := false
				for _, ev := range detectWindowEvents(prev, w, p, rule, now) {
					ev.Silent = s.QuietHours.Contains(now)
					claimed, err := e.claim(ev.Key)
					if err != nil {
						return EventProcessResult{}, err
					}
					result.Outcomes = append(result.Outcomes, EventOutcome{
						Event:        ev,
						Before:       windowEventValue(prev.UsedPercent, prev.ResetsAt),
						After:        windowEventValue(w.UsedPercent, w.ResetsAt),
						Deduplicated: !claimed,
					})
					if claimed {
						if ev.Type == "danger_threshold" {
							ev.DangerResetEpoch = resetEpoch(w.ResetsAt)
							ev.DangerPolicyFingerprint = policyFingerprint(rule)
							dangerEmitted = true
						}
						result.Emitted = append(result.Emitted, ev)
					}
				}
				if !dangerEmitted && w.RemainingPercent <= rule.DangerThresholdPct && rule.RepeatIntervalMinutes > 0 {
					epoch, fingerprint := resetEpoch(w.ResetsAt), policyFingerprint(rule)
					claimed, err := e.store.DangerDeliveryDue(w.ID, epoch, fingerprint, now, time.Duration(rule.RepeatIntervalMinutes)*time.Minute)
					if err != nil {
						return EventProcessResult{}, err
					}
					if claimed {
						ev := mkEvent("danger_reminder", "Still almost out", fmt.Sprintf("danger-reminder:%s:%d", w.ID, now.Unix()), p, w)
						ev.Silent = s.QuietHours.Contains(now)
						ev.DangerResetEpoch = epoch
						ev.DangerPolicyFingerprint = fingerprint
						result.Emitted = append(result.Emitted, ev)
					}
				}
			}
			if err := e.store.SetWindowState(WindowState{
				WindowID:    w.ID,
				UsedPercent: w.UsedPercent,
				ResetsAt:    w.ResetsAt,
			}); err != nil {
				return EventProcessResult{}, err
			}
		}

		if p.Credits != nil {
			creditsID := creditsWindowID(p.ID)
			prev, had, err := e.store.GetWindowState(creditsID)
			if err != nil {
				return EventProcessResult{}, err
			}
			if had && s.NotificationsEnabled && providerRule.Enabled && prev.CreditsAvailable != nil && p.Credits.AvailableCount > *prev.CreditsAvailable {
				ev := Event{
					Key:          creditsEventKey(p.ID, p.Credits.AvailableCount),
					Type:         "credits_increase",
					Title:        "Reset credits available",
					ProviderID:   p.ID,
					ProviderName: p.Name,
				}
				claimed, err := e.claim(ev.Key)
				if err != nil {
					return EventProcessResult{}, err
				}
				count := p.Credits.AvailableCount
				result.Outcomes = append(result.Outcomes, EventOutcome{
					Event:        ev,
					Before:       EventValue{CreditsAvailable: prev.CreditsAvailable},
					After:        EventValue{CreditsAvailable: &count},
					Deduplicated: !claimed,
				})
				if claimed {
					ev.Silent = s.QuietHours.Contains(now)
					result.Emitted = append(result.Emitted, ev)
				}
			}
			count := p.Credits.AvailableCount
			if err := e.store.SetWindowState(WindowState{
				WindowID:         creditsID,
				CreditsAvailable: &count,
			}); err != nil {
				return EventProcessResult{}, err
			}
		}
	}
	return result, nil
}

func windowEventValue(usedPercent float64, resetsAt *time.Time) EventValue {
	return EventValue{UsedPercent: &usedPercent, ResetsAt: resetsAt}
}

func creditsWindowID(providerID string) string {
	return providerID + "#credits"
}

func creditsEventKey(providerID string, count int) string {
	return fmt.Sprintf("credits:%s:%d", providerID, count)
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

func detectWindowEvents(prev WindowState, w Window, p Provider, rule AlertRule, now time.Time) []Event {
	var evs []Event

	if prev.UsedPercent < rule.EarlyThresholdPct && w.UsedPercent >= rule.EarlyThresholdPct {
		evs = append(evs, mkEvent("early_threshold", "Approaching limit", providerEventKey("early", p.ID, w.ID, w.ResetsAt), p, w))
	}

	prevRemaining := 100 - prev.UsedPercent
	if prevRemaining > rule.DangerThresholdPct && w.RemainingPercent <= rule.DangerThresholdPct {
		evs = append(evs, mkEvent("danger_threshold", "Almost out", providerEventKey("danger", p.ID, w.ID, w.ResetsAt), p, w))
	}

	if prev.ResetsAt != nil {
		if !prev.ResetsAt.After(now) {
			resetChanged := w.ResetsAt == nil || !w.ResetsAt.Equal(*prev.ResetsAt)
			usageDropped := w.UsedPercent < prev.UsedPercent
			if resetChanged || usageDropped {
				evs = append(evs, mkEvent("reset", "Limit reset", providerEventKey("reset", p.ID, w.ID, prev.ResetsAt), p, w))
			}
		} else if isSurpriseReset(prev.UsedPercent, w.UsedPercent) {
			evs = append(evs, mkEvent("tibo_reset", surpriseResetTitle(p.ID), providerEventKey("tibo", p.ID, w.ID, prev.ResetsAt), p, w))
		}
	}

	return evs
}

func resetEpoch(resetsAt *time.Time) string {
	if resetsAt == nil {
		return "epoch"
	}
	return resetsAt.UTC().Format(time.RFC3339)
}

func surpriseResetTitle(providerID string) string {
	switch providerID {
	case "claude":
		return "Tibo has struck again! Claude limits reset"
	case "codex":
		return "Saint Tibo has blessed you with tokens, Codex limits reset"
	default:
		return "Surprise reset"
	}
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

func providerEventKey(kind, providerID, windowID string, resetsAt *time.Time) string {
	_ = providerID
	return eventKey(kind, windowID, resetsAt)
}

func eventKey(kind, windowID string, resetsAt *time.Time) string {
	token := "epoch"
	if resetsAt != nil {
		token = resetsAt.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("%s:%s:%s", kind, windowID, token)
}
