package server

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// OpenUsage export / hub shapes (schema_version 1).
type openUsageEnvelope struct {
	SchemaVersion string              `json:"schema_version"`
	GeneratedAt   time.Time           `json:"generated_at"`
	Snapshots     []openUsageSnapshot `json:"snapshots"`
}

type openUsageSnapshot struct {
	ProviderID  string                      `json:"provider_id"`
	AccountID   string                      `json:"account_id"`
	Timestamp   time.Time                   `json:"timestamp"`
	Status      string                      `json:"status"`
	Metrics     map[string]openUsageMetric  `json:"metrics"`
	Resets      map[string]time.Time        `json:"resets"`
	Message     string                      `json:"message"`
	Attributes  map[string]string           `json:"attributes"`
	Diagnostics map[string]string           `json:"diagnostics"`
}

type openUsageMetric struct {
	Limit     *float64 `json:"limit"`
	Remaining *float64 `json:"remaining"`
	Used      *float64 `json:"used"`
	Unit      string   `json:"unit"`
	Window    string   `json:"window"`
}

type openUsageReadModel struct {
	Snapshots map[string]openUsageSnapshot `json:"snapshots"`
}

func looksLikeOpenUsage(body []byte) bool {
	trim := strings.TrimSpace(string(body))
	if trim == "" {
		return false
	}
	if strings.Contains(trim, `"schema_version"`) && strings.Contains(trim, `"snapshots"`) {
		return true
	}
	if strings.Contains(trim, `"provider_id"`) && strings.Contains(trim, `"metrics"`) {
		return true
	}
	return false
}

func normalizeOpenUsage(body []byte, pollIntervalMinutes int, fetchedAt time.Time) (Snapshot, error) {
	snaps, err := extractOpenUsageSnapshots(body)
	if err != nil {
		return Snapshot{}, err
	}

	byProvider := make(map[string]*Provider)
	order := make([]string, 0)

	for _, raw := range snaps {
		id := strings.TrimSpace(raw.ProviderID)
		if id == "" {
			continue
		}
		p, ok := byProvider[id]
		if !ok {
			name := displayNameForProvider(id)
			if raw.Attributes != nil {
				if v := strings.TrimSpace(raw.Attributes["display_name"]); v != "" {
					name = v
				}
			}
			p = &Provider{ID: id, Name: name}
			byProvider[id] = p
			order = append(order, id)
		}

		status := strings.ToUpper(strings.TrimSpace(raw.Status))
		switch status {
		case "AUTH", "AUTH_REQUIRED":
			if p.Error == "" {
				p.Error = firstNonEmpty(raw.Message, "authentication required")
			}
		case "ERROR", "ERR":
			if p.Error == "" {
				p.Error = firstNonEmpty(raw.Message, "provider fetch failed")
			}
		case "UNSUPPORTED":
			if p.Error == "" {
				p.Error = firstNonEmpty(raw.Message, "unsupported on this machine")
			}
		}

		windows := openUsageGaugeWindows(id, raw)
		if len(windows) == 0 {
			continue
		}
		// Prefer the account snapshot with the most capacity gauges.
		if len(windows) >= len(p.Windows) {
			p.Windows = windows
			if blob, err := json.Marshal(raw); err == nil {
				p.Raw = blob
			}
			if credits := openUsageCredits(raw); credits != nil {
				p.Credits = credits
			}
			if status == "OK" || status == "NEAR_LIMIT" || status == "LIMITED" || status == "WARN" {
				p.Error = ""
			}
		}
	}

	providers := make([]Provider, 0, len(order))
	for _, id := range order {
		p := byProvider[id]
		// Drop providers that never reported a usage-limit gauge and have no error.
		// This is the OpenUsage-shaped filter: capacity only, not spend/telemetry noise.
		if len(p.Windows) == 0 && p.Error == "" {
			continue
		}
		providers = append(providers, *p)
	}

	return Snapshot{
		FetchedAt:           fetchedAt,
		Stale:               false,
		Providers:           providers,
		PollIntervalMinutes: pollIntervalMinutes,
		SourceKind:          "openusage",
	}, nil
}

func extractOpenUsageSnapshots(body []byte) ([]openUsageSnapshot, error) {
	trim := strings.TrimSpace(string(body))
	if trim == "" {
		return nil, fmt.Errorf("normalize openusage: empty body")
	}

	var env openUsageEnvelope
	if err := json.Unmarshal(body, &env); err == nil && (len(env.Snapshots) > 0 || env.SchemaVersion != "") {
		return env.Snapshots, nil
	}

	var readModel openUsageReadModel
	if err := json.Unmarshal(body, &readModel); err == nil && len(readModel.Snapshots) > 0 {
		out := make([]openUsageSnapshot, 0, len(readModel.Snapshots))
		keys := make([]string, 0, len(readModel.Snapshots))
		for k := range readModel.Snapshots {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, readModel.Snapshots[k])
		}
		return out, nil
	}

	var arr []openUsageSnapshot
	if err := json.Unmarshal(body, &arr); err == nil && len(arr) > 0 {
		return arr, nil
	}

	var single openUsageSnapshot
	if err := json.Unmarshal(body, &single); err == nil && single.ProviderID != "" {
		return []openUsageSnapshot{single}, nil
	}

	return nil, fmt.Errorf("normalize openusage: unrecognized payload")
}

func openUsageGaugeWindows(providerID string, snap openUsageSnapshot) []Window {
	if len(snap.Metrics) == 0 {
		return nil
	}
	keys := make([]string, 0, len(snap.Metrics))
	for k := range snap.Metrics {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	keys = prioritizeGaugeKeys(keys)

	usedKeys := make(map[string]bool)
	out := make([]Window, 0, 8)
	for _, key := range keys {
		if !isCapacityGaugeKey(key) {
			continue
		}
		met := snap.Metrics[key]
		used := metricUsedPercent(key, met)
		if used < 0 {
			continue
		}
		windowKey := uniqueKey(slugify(key), usedKeys)
		usedKeys[windowKey] = true

		title := gaugeTitle(key, met.Window)
		var resetsAt *time.Time
		if t, ok := resolveReset(snap.Resets, key); ok {
			tt := t.UTC()
			resetsAt = &tt
		}

		windowLabel := normalizeWindowLabel(met.Window)
		w := Window{
			ID:               providerID + "." + windowKey,
			Key:              windowKey,
			Title:            title,
			UsedPercent:      used,
			RemainingPercent: math.Max(0, 100-used),
			ResetsAt:         resetsAt,
			WindowLabel:      windowLabel,
		}
		if resetsAt != nil && windowLabel != "" {
			w.PaceForecast = paceProjection(used, *resetsAt, windowLabel, time.Now().UTC())
		}
		out = append(out, w)
	}
	return out
}

func openUsageCredits(snap openUsageSnapshot) *Credits {
	for _, key := range []string{"codex_credit_limit", "credit_balance", "plan_bonus"} {
		met, ok := snap.Metrics[key]
		if !ok {
			continue
		}
		if met.Remaining != nil {
			return &Credits{AvailableCount: int(math.Round(*met.Remaining))}
		}
		if met.Used != nil && met.Limit != nil {
			return &Credits{AvailableCount: int(math.Round(math.Max(0, *met.Limit-*met.Used)))}
		}
	}
	return nil
}

func metricUsedPercent(key string, m openUsageMetric) float64 {
	if key == "context_window" {
		return -1
	}
	if m.Unit == "%" && m.Used != nil {
		return *m.Used
	}
	if m.Limit != nil && m.Remaining != nil && *m.Limit > 0 {
		return (*m.Limit - *m.Remaining) / *m.Limit * 100
	}
	if m.Limit != nil && m.Used != nil && *m.Limit > 0 {
		return *m.Used / *m.Limit * 100
	}
	return -1
}

func isCapacityGaugeKey(key string) bool {
	k := strings.ToLower(key)
	switch {
	case strings.HasPrefix(k, "model_"), strings.HasPrefix(k, "client_"), strings.HasPrefix(k, "tool_"),
		strings.HasPrefix(k, "source_"), strings.HasPrefix(k, "interface_"), strings.HasPrefix(k, "project_"),
		strings.HasPrefix(k, "tokens_"), strings.HasPrefix(k, "analytics_"), strings.HasPrefix(k, "usage_model_"),
		strings.HasPrefix(k, "usage_source_"), strings.HasPrefix(k, "usage_client_"):
		return false
	}

	switch k {
	case "rpm", "tpm", "rpd", "tpd", "cache_hit_ratio", "context_window",
		"today_cost", "billing_total_cost", "composer_cost", "requests_today",
		"total_ai_requests", "composer_sessions", "messages_today", "sessions_today":
		return false
	}

	if strings.Contains(k, "percent_used") || strings.Contains(k, "percent") && strings.Contains(k, "used") {
		return true
	}
	if strings.HasPrefix(k, "usage_") || strings.HasPrefix(k, "rate_limit_") {
		return true
	}
	switch k {
	case "plan_percent_used", "plan_auto_percent_used", "plan_api_percent_used",
		"spend_limit", "individual_spend", "team_budget", "plan_spend",
		"codex_credit_percent_used", "billing_cycle_progress", "composer_context_pct":
		return true
	}
	return false
}

func prioritizeGaugeKeys(keys []string) []string {
	priority := []string{
		"plan_percent_used", "plan_auto_percent_used", "plan_api_percent_used",
		"usage_five_hour", "usage_seven_day", "usage_seven_day_sonnet", "usage_seven_day_opus", "usage_seven_day_cowork",
		"codex_credit_percent_used", "rate_limit_primary", "rate_limit_secondary",
		"rate_limit_5h", "rate_limit_7d", "spend_limit", "billing_cycle_progress",
	}
	rank := make(map[string]int, len(priority))
	for i, k := range priority {
		rank[k] = i
	}
	sort.SliceStable(keys, func(i, j int) bool {
		ri, oki := rank[keys[i]]
		rj, okj := rank[keys[j]]
		switch {
		case oki && okj:
			return ri < rj
		case oki:
			return true
		case okj:
			return false
		default:
			return keys[i] < keys[j]
		}
	})
	return keys
}

func gaugeTitle(key, window string) string {
	labels := map[string]string{
		"plan_percent_used":            "Plan",
		"plan_auto_percent_used":       "Auto",
		"plan_api_percent_used":        "API",
		"usage_five_hour":              "5h",
		"usage_seven_day":              "7d",
		"usage_seven_day_sonnet":       "7d Sonnet",
		"usage_seven_day_opus":         "7d Opus",
		"usage_seven_day_cowork":       "7d Team",
		"codex_credit_percent_used":    "Credits",
		"rate_limit_primary":           "Primary",
		"rate_limit_secondary":         "Weekly",
		"spend_limit":                  "Spend limit",
		"billing_cycle_progress":       "Billing cycle",
		"individual_spend":             "Individual spend",
		"team_budget":                  "Team budget",
		"plan_spend":                   "Plan spend",
		"composer_context_pct":         "Context",
	}
	if label, ok := labels[key]; ok {
		return label
	}
	if strings.HasPrefix(key, "rate_limit_") {
		w := normalizeWindowLabel(window)
		if w != "" {
			return "Usage " + w
		}
		return "Usage " + strings.TrimPrefix(key, "rate_limit_")
	}
	if w := normalizeWindowLabel(window); w != "" {
		return displayName(strings.TrimPrefix(key, "usage_")) + " (" + w + ")"
	}
	return displayName(strings.ReplaceAll(key, "_", " "))
}

func normalizeWindowLabel(window string) string {
	switch strings.ToLower(strings.TrimSpace(window)) {
	case "5h", "rolling-5h":
		return "5h"
	case "1d", "24h", "today":
		return "1d"
	case "7d", "week", "weekly":
		return "7d"
	case "14d":
		return "14d"
	case "30d", "month", "monthly", "billing":
		return "30d"
	default:
		return strings.ToLower(strings.TrimSpace(window))
	}
}

func resolveReset(resets map[string]time.Time, key string) (time.Time, bool) {
	if resets == nil {
		return time.Time{}, false
	}
	if t, ok := resets[key]; ok && !t.IsZero() {
		return t, true
	}
	// Cursor often keys resets as billing_cycle_end while gauges use plan_* keys.
	fallbacks := []string{
		key + "_reset",
		"billing_cycle_end",
		"billing_cycle",
		"plan_reset",
		"rate_limit_primary",
		"rate_limit_secondary",
		"usage_five_hour",
		"usage_seven_day",
		"codex_credit_limit",
	}
	for _, fb := range fallbacks {
		if t, ok := resets[fb]; ok && !t.IsZero() {
			return t, true
		}
	}
	return time.Time{}, false
}

func displayNameForProvider(id string) string {
	switch strings.ToLower(id) {
	case "cursor":
		return "Cursor"
	case "claude", "claude_code":
		return "Claude Code"
	case "codex", "codex_cli":
		return "Codex"
	case "copilot", "github_copilot":
		return "Copilot"
	case "gemini", "gemini_cli":
		return "Gemini"
	case "grok", "xai":
		return "Grok"
	case "openrouter":
		return "OpenRouter"
	case "openai":
		return "OpenAI"
	case "anthropic":
		return "Anthropic"
	case "ollama":
		return "Ollama"
	default:
		return displayName(strings.ReplaceAll(id, "_", " "))
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
