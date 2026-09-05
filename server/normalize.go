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
	SourceKind          string     `json:"sourceKind,omitempty"`
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
	PaceForecast     *WindowForecast `json:"-"`
	Forecast         *WindowForecast `json:"forecast,omitempty"`
}

type Credits struct {
	AvailableCount int `json:"availableCount"`
}

func Normalize(body []byte, pollIntervalMinutes int, fetchedAt time.Time) (Snapshot, error) {
	if looksLikeLimitsV1(body) {
		return normalizeLimitsV1(body, pollIntervalMinutes, fetchedAt)
	}
	if looksLikeUsageLines(body) {
		converted, err := usageLinesToLimits(body)
		if err != nil {
			return Snapshot{}, err
		}
		return normalizeLimitsV1(converted, pollIntervalMinutes, fetchedAt)
	}
	return Snapshot{}, fmt.Errorf("normalize: unrecognized CrossUsage payload")
}

func inferWindowLabel(title, key string) string {
	lower := strings.ToLower(title + " " + key)
	switch {
	case strings.Contains(lower, "5h") || strings.Contains(lower, "5 h") || strings.Contains(lower, "session"):
		return "5h"
	case strings.Contains(lower, "weekly") || strings.Contains(lower, "7d"):
		return "7d"
	case strings.Contains(lower, "monthly") || strings.Contains(lower, "30d") || strings.Contains(lower, "total"):
		return "30d"
	case strings.Contains(lower, "daily") || strings.Contains(lower, "1d") || strings.Contains(lower, "24h"):
		return "1d"
	default:
		return ""
	}
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
	case "primary", "session":
		return "Primary"
	case "secondary", "weekly":
		return "Secondary"
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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
