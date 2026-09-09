package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const limitsSchemaV1 = "crossusage.limits.v1"

type limitsV1Envelope struct {
	Schema      string                      `json:"schema"`
	GeneratedAt string                      `json:"generatedAt"`
	Providers   map[string]limitsV1Provider `json:"providers"`
	Errors      []limitsV1Error             `json:"errors"`
}

type limitsV1Provider struct {
	DisplayName string                      `json:"displayName"`
	Plan        string                      `json:"plan"`
	Stale       bool                        `json:"stale"`
	Resources   map[string]limitsV1Resource `json:"resources"`
}

type limitsV1Resource struct {
	Kind          string   `json:"kind"`
	Unit          string   `json:"unit"`
	Used          *float64 `json:"used"`
	Utilization   *float64 `json:"utilization"`
	Limit         *float64 `json:"limit"`
	Remaining     *float64 `json:"remaining"`
	Available     *float64 `json:"available"`
	ResetsAt      string   `json:"resetsAt"`
	WindowSeconds *float64 `json:"windowSeconds"`
	Label         string   `json:"label"`
}

type limitsV1Error struct {
	ProviderID string `json:"providerId"`
	Message    string `json:"message"`
}

type usageLineSnapshot struct {
	ProviderID  string      `json:"providerId"`
	DisplayName string      `json:"displayName"`
	Plan        string      `json:"plan"`
	Stale       bool        `json:"stale"`
	Lines       []usageLine `json:"lines"`
	FetchedAt   string      `json:"fetchedAt"`
}

type usageLine struct {
	Type             string   `json:"type"`
	Label            string   `json:"label"`
	Used             *float64 `json:"used"`
	Limit            *float64 `json:"limit"`
	ResetsAt         string   `json:"resetsAt"`
	PeriodDurationMs *float64 `json:"periodDurationMs"`
	Format           *struct {
		Kind string `json:"kind"`
	} `json:"format"`
}

func looksLikeLimitsV1(body []byte) bool {
	return bytes.Contains(body, []byte(`"crossusage.limits.v1"`))
}

func looksLikeUsageLines(body []byte) bool {
	trim := bytes.TrimSpace(body)
	if len(trim) == 0 {
		return false
	}
	if !bytes.Contains(trim, []byte(`"providerId"`)) || !bytes.Contains(trim, []byte(`"lines"`)) {
		return false
	}
	return trim[0] == '[' || bytes.Contains(trim, []byte(`"type"`))
}

func CollectCrossUsageLimits(ctx context.Context, binary string) ([]byte, error) {
	ids := catalogPluginIDs()
	raw, err := runCrossUsageCLI(ctx, binary, append([]string{"limits"}, ids...)...)
	if err == nil && looksLikeLimitsV1(raw) {
		return raw, nil
	}
	if err != nil && !isUnknownPlugin(err) && !looksLikeLimitsV1(raw) {
		merged, mergeErr := collectLimitsByID(ctx, binary, ids)
		if mergeErr == nil {
			return merged, nil
		}
		return nil, err
	}
	return collectLimitsByID(ctx, binary, ids)
}

func collectLimitsByID(ctx context.Context, binary string, ids []string) ([]byte, error) {
	base := limitsV1Envelope{
		Schema:    limitsSchemaV1,
		Providers: map[string]limitsV1Provider{},
	}
	present := map[string]bool{}
	var lastErr error
	got := false
	for _, id := range ids {
		raw, err := runCrossUsageCLI(ctx, binary, "limits", id)
		if err != nil {
			lastErr = err
			if isUnknownPlugin(err) {
				continue
			}
			if !looksLikeLimitsV1(raw) {
				continue
			}
		}
		if !looksLikeLimitsV1(raw) {
			continue
		}
		var extra limitsV1Envelope
		if err := json.Unmarshal(raw, &extra); err != nil {
			lastErr = err
			continue
		}
		got = true
		for pid, p := range extra.Providers {
			key := canonicalProviderID(pid)
			if present[key] {
				continue
			}
			base.Providers[pid] = p
			present[key] = true
		}
		for _, e := range extra.Errors {
			key := canonicalProviderID(e.ProviderID)
			if present[key] {
				continue
			}
			base.Errors = append(base.Errors, e)
			present[key] = true
		}
	}
	if !got {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("crossusage: no catalog providers returned limits")
	}
	out, err := json.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("crossusage: encode merged limits: %w", err)
	}
	return out, nil
}

func runCrossUsageCLI(ctx context.Context, binary string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 240*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, binary, args...)
	configureCommandCancellation(cmd)
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("crossusage: timeout running %s %s", binary, strings.Join(args, " "))
	}
	if stdout.Len() > collectorMaxResponseBytes {
		return nil, fmt.Errorf("crossusage: response too large")
	}
	if body, ok := extractJSONDocument(stdout.Bytes()); ok {
		return body, nil
	}
	if runErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = runErr.Error()
		}
		return nil, fmt.Errorf("crossusage: %s", truncateDiagnostic(detail, 180))
	}
	return nil, fmt.Errorf("crossusage: invalid JSON")
}

func isUnknownPlugin(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "unknown plugin")
}

func normalizeLimitsV1(body []byte, pollIntervalMinutes int, fetchedAt time.Time) (Snapshot, error) {
	var env limitsV1Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return Snapshot{}, fmt.Errorf("normalize crossusage: decode limits.v1: %w", err)
	}
	byID := make(map[string]Provider, len(env.Providers)+len(env.Errors))
	for rawID, raw := range env.Providers {
		id := canonicalProviderID(rawID)
		if id == "" || !inProviderCatalog(id) {
			continue
		}
		p := limitsProviderToDomain(id, raw, fetchedAt)
		if !keepPlanProvider(p) {
			continue
		}
		byID[id] = p
	}
	for _, e := range env.Errors {
		id := canonicalProviderID(e.ProviderID)
		if id == "" || !inProviderCatalog(id) {
			continue
		}
		if _, ok := byID[id]; ok {
			continue
		}
		msg := strings.TrimSpace(e.Message)
		if msg == "" {
			msg = "authentication required"
		}
		p := Provider{ID: id, Name: displayNameForProvider(id), Error: msg}
		if keepPlanProvider(p) {
			byID[id] = p
		}
	}

	providers := make([]Provider, 0, len(byID))
	seen := make(map[string]bool, len(byID))
	for _, id := range defaultProviderOrder {
		p, ok := byID[id]
		if !ok {
			continue
		}
		providers = append(providers, p)
		seen[id] = true
	}
	for id, p := range byID {
		if seen[id] {
			continue
		}
		providers = append(providers, p)
	}

	return Snapshot{
		FetchedAt:           fetchedAt,
		Stale:               false,
		Providers:           providers,
		PollIntervalMinutes: pollIntervalMinutes,
		SourceKind:          "crossusage",
	}, nil
}

func limitsProviderToDomain(id string, raw limitsV1Provider, now time.Time) Provider {
	p := Provider{
		ID:    id,
		Name:  displayNameForProvider(id),
		Stale: raw.Stale,
	}
	if name, ok := catalogDisplayName(id); ok {
		p.Name = name
	}
	p.Windows = limitsResourceWindows(id, raw.Resources, now)
	p.Credits = limitsCredits(raw.Resources)
	if len(p.Windows) > 0 {
		p.Error = ""
	}
	return p
}

func limitsResourceWindows(providerID string, resources map[string]limitsV1Resource, now time.Time) []Window {
	if len(resources) == 0 {
		return nil
	}
	keys := make([]string, 0, len(resources))
	for k := range resources {
		keys = append(keys, k)
	}
	priority := []string{
		"totalUsage", "total-usage", "total_usage",
		"autoUsage", "auto-usage", "auto_usage",
		"session", "weekly", "premiumCredits", "premium-credits",
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

	usedKeys := make(map[string]bool)
	out := make([]Window, 0, len(keys))
	for _, key := range keys {
		res := resources[key]
		if skipLimitsResource(providerID, key, res) {
			continue
		}
		used, ok := consumptionUsedPercent(res)
		if !ok {
			continue
		}
		windowKey := uniqueKey(slugify(key), usedKeys)
		usedKeys[windowKey] = true
		label := ""
		if res.WindowSeconds != nil {
			label = windowLabelFromSeconds(*res.WindowSeconds)
		}
		title := limitsWindowTitle(providerID, key, res.Label, label)
		if label == "" {
			label = inferWindowLabel(title, key)
		}
		var resetsAt *time.Time
		if t, ok := parseLimitsTime(res.ResetsAt); ok {
			tt := t.UTC()
			resetsAt = &tt
		}
		w := Window{
			ID:               providerID + "." + windowKey,
			Key:              windowKey,
			Title:            title,
			UsedPercent:      used,
			RemainingPercent: math.Max(0, 100-used),
			ResetsAt:         resetsAt,
			WindowLabel:      label,
		}
		if resetsAt != nil && label != "" {
			w.PaceForecast = paceProjection(used, *resetsAt, label, now)
		}
		out = append(out, w)
	}
	return out
}

func skipLimitsResource(providerID, key string, res limitsV1Resource) bool {
	if isAPINoiseWindow(providerID, key, firstNonEmpty(res.Label, key)) {
		return true
	}
	if res.Kind != "" && !strings.EqualFold(res.Kind, "consumption") {
		return true
	}
	unit := strings.ToLower(strings.TrimSpace(res.Unit))
	if unit != "" && unit != "percent" {
		return true
	}
	return false
}

func consumptionUsedPercent(res limitsV1Resource) (float64, bool) {
	if res.Utilization != nil {
		pct := *res.Utilization * 100
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		return pct, true
	}
	if res.Used != nil && strings.EqualFold(res.Unit, "percent") {
		u := *res.Used
		if u <= 1 {
			return u * 100, true
		}
		if u > 100 {
			return 100, true
		}
		return u, true
	}
	if res.Limit != nil && *res.Limit > 0 && res.Remaining != nil {
		pct := (*res.Limit - *res.Remaining) / *res.Limit * 100
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		return pct, true
	}
	if res.Limit != nil && *res.Limit > 0 && res.Used != nil {
		pct := *res.Used / *res.Limit * 100
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		return pct, true
	}
	return 0, false
}

func limitsWindowTitle(providerID, key, resourceLabel, windowLabel string) string {
	norm := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", "-"), " ", "-"))
	switch norm {
	case "totalusage", "total-usage", "plan":
		return "Plan"
	case "autousage", "auto-usage", "auto":
		return "Auto"
	case "session":
		if windowLabel != "" {
			return windowLabel
		}
		return "5h"
	case "weekly":
		if windowLabel != "" {
			return windowLabel
		}
		return "7d"
	case "premiumcredits", "premium-credits", "premium":
		return "Premium"
	}
	label := strings.ToLower(strings.TrimSpace(resourceLabel))
	switch label {
	case "total usage", "plan usage", "plan":
		return "Plan"
	case "auto usage", "auto":
		return "Auto"
	case "session":
		if windowLabel != "" {
			return windowLabel
		}
		return "5h"
	case "weekly":
		if windowLabel != "" {
			return windowLabel
		}
		return "7d"
	case "premium", "premium credits":
		return "Premium"
	}
	if resourceLabel != "" {
		return resourceLabel
	}
	if windowLabel != "" {
		return windowLabel
	}
	return planWindowTitle(providerID, key, "", nil)
}

func windowLabelFromSeconds(seconds float64) string {
	if seconds <= 0 {
		return ""
	}
	hours := seconds / 3600
	switch {
	case hours >= 4 && hours <= 6:
		return "5h"
	case hours >= 20 && hours <= 28:
		return "1d"
	case hours >= 6*24 && hours <= 9*24:
		return "7d"
	case hours >= 25*24 && hours <= 40*24:
		return "30d"
	default:
		return ""
	}
}

func limitsCredits(resources map[string]limitsV1Resource) *Credits {
	for _, key := range []string{"credits", "creditValue", "credit-value"} {
		res, ok := resources[key]
		if !ok || !strings.EqualFold(res.Kind, "balance") {
			continue
		}
		if !strings.EqualFold(res.Unit, "credits") {
			continue
		}
		if res.Available != nil {
			return &Credits{AvailableCount: int(math.Round(*res.Available))}
		}
	}
	return nil
}

func parseLimitsTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func usageLinesToLimits(body []byte) ([]byte, error) {
	snaps, err := extractUsageLineSnapshots(body)
	if err != nil {
		return nil, err
	}
	env := limitsV1Envelope{
		Schema:    limitsSchemaV1,
		Providers: map[string]limitsV1Provider{},
	}
	for _, snap := range snaps {
		id := strings.TrimSpace(snap.ProviderID)
		if id == "" {
			continue
		}
		resources := map[string]limitsV1Resource{}
		usedKeys := map[string]bool{}
		for _, line := range snap.Lines {
			if !strings.EqualFold(line.Type, "progress") {
				continue
			}
			unit := "percent"
			if line.Format != nil && line.Format.Kind != "" {
				unit = strings.ToLower(line.Format.Kind)
			}
			key := uniqueKey(slugify(firstNonEmpty(line.Label, "resource")), usedKeys)
			usedKeys[key] = true
			res := limitsV1Resource{
				Unit:  unit,
				Label: line.Label,
				Used:  line.Used,
				Limit: line.Limit,
			}
			if line.ResetsAt != "" {
				res.ResetsAt = line.ResetsAt
			}
			if line.PeriodDurationMs != nil && *line.PeriodDurationMs > 0 {
				seconds := *line.PeriodDurationMs / 1000
				res.WindowSeconds = &seconds
			}
			resources[key] = res
		}
		env.Providers[id] = limitsV1Provider{
			DisplayName: snap.DisplayName,
			Plan:        snap.Plan,
			Stale:       snap.Stale,
			Resources:   resources,
		}
	}
	out, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("normalize crossusage: encode usage lines: %w", err)
	}
	return out, nil
}

func extractUsageLineSnapshots(body []byte) ([]usageLineSnapshot, error) {
	trim := bytes.TrimSpace(body)
	if len(trim) == 0 {
		return nil, fmt.Errorf("normalize crossusage: empty body")
	}
	if trim[0] == '[' {
		var arr []usageLineSnapshot
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil, fmt.Errorf("normalize crossusage: decode usage array: %w", err)
		}
		return arr, nil
	}
	var one usageLineSnapshot
	if err := json.Unmarshal(body, &one); err != nil {
		return nil, fmt.Errorf("normalize crossusage: decode usage object: %w", err)
	}
	if strings.TrimSpace(one.ProviderID) == "" {
		return nil, fmt.Errorf("normalize crossusage: unrecognized usage payload")
	}
	return []usageLineSnapshot{one}, nil
}
