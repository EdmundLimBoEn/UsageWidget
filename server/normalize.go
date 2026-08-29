package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Snapshot struct {
	FetchedAt           time.Time  `json:"fetchedAt"`
	Stale               bool       `json:"stale"`
	Providers           []Provider `json:"providers"`
	PollIntervalMinutes int        `json:"pollIntervalMinutes"`
	// SourceKind is "openusage" or "codexbar". Empty means legacy/unknown.
	SourceKind string `json:"sourceKind,omitempty"`
}

type Provider struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Error   string          `json:"error,omitempty"`
	Stale   bool            `json:"stale,omitempty"`
	Windows []Window        `json:"windows"`
	Credits *Credits        `json:"credits,omitempty"`
	Raw     json.RawMessage `json:"raw,omitempty"`
}

type Window struct {
	ID               string          `json:"id"`
	Key              string          `json:"key"`
	Title            string          `json:"title"`
	UsedPercent      float64         `json:"usedPercent"`
	RemainingPercent float64         `json:"remainingPercent"`
	ResetsAt         *time.Time      `json:"resetsAt,omitempty"`
	WindowLabel      string          `json:"windowLabel,omitempty"`
	PaceForecast     *WindowForecast `json:"-"` // filled during normalize; merged into Forecast on save
	Forecast         *WindowForecast `json:"forecast,omitempty"`
}

type Credits struct {
	AvailableCount int `json:"availableCount"`
}

// CodexBar serve/CLI emits either:
//   - a single provider payload
//   - an array of provider payloads
//   - { "providers": [ ... ] } (forward-compatible wrapper)
//
// Real payload fields (see CodexBar docs/cli.md):
//
//	provider, usage.{primary,secondary,tertiary}, credits.remaining, error.message
func Normalize(body []byte, pollIntervalMinutes int, fetchedAt time.Time) (Snapshot, error) {
	if looksLikeOpenUsage(body) {
		return normalizeOpenUsage(body, pollIntervalMinutes, fetchedAt)
	}
	return normalizeCodexBar(body, pollIntervalMinutes, fetchedAt)
}

func normalizeCodexBar(body []byte, pollIntervalMinutes int, fetchedAt time.Time) (Snapshot, error) {
	rawProviders, err := extractProviderRaw(body)
	if err != nil {
		return Snapshot{}, err
	}

	providers := make([]Provider, 0, len(rawProviders))
	for _, rawProvider := range rawProviders {
		p, err := normalizeOne(rawProvider)
		if err != nil {
			return Snapshot{}, err
		}
		if !keepPlanProvider(p) {
			continue
		}
		providers = append(providers, p)
	}

	for pi := range providers {
		for wi := range providers[pi].Windows {
			w := &providers[pi].Windows[wi]
			if w.WindowLabel == "" {
				w.WindowLabel = inferWindowLabel(w.Title, w.Key)
			}
			if w.ResetsAt != nil && w.WindowLabel != "" {
				w.PaceForecast = paceProjection(w.UsedPercent, *w.ResetsAt, w.WindowLabel, fetchedAt)
			}
		}
	}

	return Snapshot{
		FetchedAt:           fetchedAt,
		Stale:               false,
		Providers:           providers,
		PollIntervalMinutes: pollIntervalMinutes,
		SourceKind:          "codexbar",
	}, nil
}

func inferWindowLabel(title, key string) string {
	lower := strings.ToLower(title + " " + key)
	switch {
	case strings.Contains(lower, "5h") || strings.Contains(lower, "5 h") || strings.Contains(lower, "primary"):
		return "5h"
	case strings.Contains(lower, "weekly") || strings.Contains(lower, "7d") || strings.Contains(lower, "secondary"):
		return "7d"
	case strings.Contains(lower, "monthly") || strings.Contains(lower, "30d"):
		return "30d"
	case strings.Contains(lower, "daily") || strings.Contains(lower, "1d") || strings.Contains(lower, "24h"):
		return "1d"
	default:
		return ""
	}
}

func extractProviderRaw(body []byte) ([]json.RawMessage, error) {
	trim := strings.TrimSpace(string(body))
	if trim == "" {
		return nil, fmt.Errorf("normalize: empty body")
	}

	switch trim[0] {
	case '[':
		var arr []json.RawMessage
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil, fmt.Errorf("normalize: decode array: %w", err)
		}
		return arr, nil
	case '{':
		// Wrapped form from plan.
		var wrapped struct {
			Providers []json.RawMessage `json:"providers"`
		}
		if err := json.Unmarshal(body, &wrapped); err == nil && len(wrapped.Providers) > 0 {
			return wrapped.Providers, nil
		}
		// Single provider payload.
		var single json.RawMessage
		if err := json.Unmarshal(body, &single); err != nil {
			return nil, fmt.Errorf("normalize: decode object: %w", err)
		}
		return []json.RawMessage{single}, nil
	default:
		return nil, fmt.Errorf("normalize: unexpected JSON start %q", trim[:1])
	}
}

type codexBarPayload struct {
	Provider string `json:"provider"`
	// Alternate id field used by plan-shaped fixtures.
	ID    string `json:"id"`
	Name  string `json:"name"`
	Stale bool   `json:"stale"`

	// Nested usage windows (real CodexBar shape).
	Usage *struct {
		Primary   *codexBarWindow `json:"primary"`
		Secondary *codexBarWindow `json:"secondary"`
		Tertiary  *codexBarWindow `json:"tertiary"`
	} `json:"usage"`

	// Flat windows (plan-shaped fixtures / possible future).
	Primary          *codexBarWindow       `json:"primary"`
	Secondary        *codexBarWindow       `json:"secondary"`
	Tertiary         *codexBarWindow       `json:"tertiary"`
	ExtraRateWindows []codexBarExtraWindow `json:"extraRateWindows"`

	Credits *struct {
		Remaining      *float64 `json:"remaining"`
		AvailableCount *int     `json:"availableCount"`
	} `json:"credits"`
	CodexResetCredits *struct {
		AvailableCount int `json:"availableCount"`
	} `json:"codexResetCredits"`

	// error can be a string or {message, kind, code}.
	Error json.RawMessage `json:"error"`
}

type codexBarWindow struct {
	Title         string     `json:"title"`
	UsedPercent   float64    `json:"usedPercent"`
	WindowMinutes *float64   `json:"windowMinutes"`
	ResetsAt      *time.Time `json:"resetsAt"`
}

type codexBarExtraWindow struct {
	Key           string     `json:"key"`
	Title         string     `json:"title"`
	UsedPercent   float64    `json:"usedPercent"`
	WindowMinutes *float64   `json:"windowMinutes"`
	ResetsAt      *time.Time `json:"resetsAt"`
}

func normalizeOne(raw json.RawMessage) (Provider, error) {
	var up codexBarPayload
	if err := json.Unmarshal(raw, &up); err != nil {
		return Provider{}, fmt.Errorf("normalize: decode provider: %w", err)
	}

	id := up.Provider
	if id == "" {
		id = up.ID
	}
	if id == "" {
		return Provider{}, fmt.Errorf("normalize: provider missing id")
	}

	name := up.Name
	if name == "" {
		name = displayNameForProvider(id)
	}

	p := Provider{
		ID:    id,
		Name:  name,
		Stale: up.Stale,
		Raw:   raw,
	}
	if msg := decodeErrorMessage(up.Error); msg != "" {
		p.Error = msg
	}

	if up.CodexResetCredits != nil {
		p.Credits = &Credits{AvailableCount: up.CodexResetCredits.AvailableCount}
	} else if up.Credits != nil && up.Credits.AvailableCount != nil {
		p.Credits = &Credits{AvailableCount: *up.Credits.AvailableCount}
	}

	usedKeys := make(map[string]bool)
	add := func(key string, w *codexBarWindow) {
		if w == nil {
			return
		}
		title := planWindowTitle(id, key, w.Title, w.WindowMinutes)
		if isAPINoiseWindow(id, key, title) {
			return
		}
		p.Windows = append(p.Windows, Window{
			ID:               id + "." + key,
			Key:              key,
			Title:            title,
			UsedPercent:      w.UsedPercent,
			RemainingPercent: 100 - w.UsedPercent,
			ResetsAt:         w.ResetsAt,
		})
		usedKeys[key] = true
	}

	// Prefer nested usage.* (real CodexBar), fall back to flat fields.
	if up.Usage != nil {
		add("primary", up.Usage.Primary)
		add("secondary", up.Usage.Secondary)
		add("tertiary", up.Usage.Tertiary)
	} else {
		add("primary", up.Primary)
		add("secondary", up.Secondary)
		add("tertiary", up.Tertiary)
	}

	for _, extra := range up.ExtraRateWindows {
		key := extra.Key
		if key == "" {
			key = slugify(extra.Title)
		}
		key = uniqueKey(key, usedKeys)
		title := planWindowTitle(id, key, extra.Title, extra.WindowMinutes)
		if isAPINoiseWindow(id, key, title) {
			continue
		}
		p.Windows = append(p.Windows, Window{
			ID:               id + "." + key,
			Key:              key,
			Title:            title,
			UsedPercent:      extra.UsedPercent,
			RemainingPercent: 100 - extra.UsedPercent,
			ResetsAt:         extra.ResetsAt,
		})
		usedKeys[key] = true
	}

	return p, nil
}

func decodeErrorMessage(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.Message
	}
	return strings.TrimSpace(string(raw))
}

func displayName(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "Unknown"
	}
	parts := strings.Fields(id)
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func windowTitle(key string, minutes *float64) string {
	if minutes != nil {
		m := *minutes
		switch {
		case m >= 60*24*6.5 && m <= 60*24*7.5:
			return "Weekly"
		case m >= 60*24*29 && m <= 60*24*32:
			return "Monthly"
		case m >= 60*4.5 && m <= 60*5.5:
			return "5h limit"
		case m >= 60:
			h := int(m+0.5) / 60
			return fmt.Sprintf("%dh limit", h)
		case m > 0:
			return fmt.Sprintf("%.0fm limit", m)
		}
	}
	switch key {
	case "primary":
		return "Primary"
	case "secondary":
		return "Secondary"
	case "tertiary":
		return "Tertiary"
	default:
		return key
	}
}

func slugify(title string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if s == "" {
		return "window"
	}
	return s
}

func uniqueKey(base string, used map[string]bool) string {
	if !used[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !used[candidate] {
			return candidate
		}
	}
}
