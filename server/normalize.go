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
}

type Provider struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Error   string          `json:"error,omitempty"`
	Windows []Window        `json:"windows"`
	Credits *Credits        `json:"credits,omitempty"`
	Raw     json.RawMessage `json:"raw,omitempty"`
}

type Window struct {
	ID               string     `json:"id"`
	Key              string     `json:"key"`
	Title            string     `json:"title"`
	UsedPercent      float64    `json:"usedPercent"`
	RemainingPercent float64    `json:"remainingPercent"`
	ResetsAt         *time.Time `json:"resetsAt,omitempty"`
}

type Credits struct {
	AvailableCount int `json:"availableCount"`
}

type upstreamResponse struct {
	Providers []json.RawMessage `json:"providers"`
}

type upstreamProvider struct {
	ID                string                `json:"id"`
	Name              string                `json:"name"`
	Primary           *upstreamWindow       `json:"primary"`
	Secondary         *upstreamWindow       `json:"secondary"`
	Tertiary          *upstreamWindow       `json:"tertiary"`
	ExtraRateWindows  []upstreamExtraWindow `json:"extraRateWindows"`
	CodexResetCredits *upstreamCredits      `json:"codexResetCredits"`
	Error             *string               `json:"error"`
}

type upstreamWindow struct {
	Title       string     `json:"title"`
	UsedPercent float64    `json:"usedPercent"`
	ResetsAt    *time.Time `json:"resetsAt"`
}

type upstreamExtraWindow struct {
	Key         string     `json:"key"`
	Title       string     `json:"title"`
	UsedPercent float64    `json:"usedPercent"`
	ResetsAt    *time.Time `json:"resetsAt"`
}

type upstreamCredits struct {
	AvailableCount int `json:"availableCount"`
}

func Normalize(body []byte, pollIntervalMinutes int, fetchedAt time.Time) (Snapshot, error) {
	var upstream upstreamResponse
	if err := json.Unmarshal(body, &upstream); err != nil {
		return Snapshot{}, fmt.Errorf("normalize: decode response: %w", err)
	}

	providers := make([]Provider, 0, len(upstream.Providers))
	for _, rawProvider := range upstream.Providers {
		var up upstreamProvider
		if err := json.Unmarshal(rawProvider, &up); err != nil {
			return Snapshot{}, fmt.Errorf("normalize: decode provider: %w", err)
		}

		p := Provider{
			ID:   up.ID,
			Name: up.Name,
			Raw:  rawProvider,
		}
		if up.Error != nil {
			p.Error = *up.Error
		}
		if up.CodexResetCredits != nil {
			p.Credits = &Credits{AvailableCount: up.CodexResetCredits.AvailableCount}
		}

		usedKeys := make(map[string]bool)
		addWindow := func(key, title string, usedPercent float64, resetsAt *time.Time) {
			p.Windows = append(p.Windows, Window{
				ID:               up.ID + "." + key,
				Key:              key,
				Title:            title,
				UsedPercent:      usedPercent,
				RemainingPercent: 100 - usedPercent,
				ResetsAt:         resetsAt,
			})
			usedKeys[key] = true
		}

		if up.Primary != nil {
			addWindow("primary", up.Primary.Title, up.Primary.UsedPercent, up.Primary.ResetsAt)
		}
		if up.Secondary != nil {
			addWindow("secondary", up.Secondary.Title, up.Secondary.UsedPercent, up.Secondary.ResetsAt)
		}
		if up.Tertiary != nil {
			addWindow("tertiary", up.Tertiary.Title, up.Tertiary.UsedPercent, up.Tertiary.ResetsAt)
		}
		for _, extra := range up.ExtraRateWindows {
			key := extra.Key
			if key == "" {
				key = slugify(extra.Title)
			}
			key = uniqueKey(key, usedKeys)
			addWindow(key, extra.Title, extra.UsedPercent, extra.ResetsAt)
		}

		providers = append(providers, p)
	}

	return Snapshot{
		FetchedAt:           fetchedAt,
		Stale:               false,
		Providers:           providers,
		PollIntervalMinutes: pollIntervalMinutes,
	}, nil
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
