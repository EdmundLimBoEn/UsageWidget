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
}

type limitsV1Error struct {
	ProviderID string `json:"providerId"`
	Message    string `json:"message"`
}

func looksLikeOpenUsageLimitsV1(body []byte) bool {
	return bytes.Contains(body, []byte(`"openusage.limits.v1"`))
}

func catalogCLIAttempts() [][]string {
	out := make([][]string, 0, len(providerCatalog))
	for _, e := range providerCatalog {
		names := []string{e.CLI}
		if e.ID == "gemini_cli" {
			names = append(names, "antigravity")
		}
		out = append(out, names)
	}
	return out
}

func CollectOpenUsageLimits(ctx context.Context, binary string) ([]byte, error) {
	raw, err := runOpenUsageCLI(ctx, binary)
	if err != nil {
		return nil, err
	}
	if !looksLikeOpenUsageLimitsV1(raw) || !json.Valid(raw) {
		return nil, fmt.Errorf("openusage: expected limits.v1 JSON")
	}
	var base limitsV1Envelope
	if err := json.Unmarshal(raw, &base); err != nil {
		return nil, fmt.Errorf("openusage: decode limits.v1: %w", err)
	}
	if base.Providers == nil {
		base.Providers = map[string]limitsV1Provider{}
	}
	present := make(map[string]bool, len(base.Providers)+len(base.Errors))
	for id := range base.Providers {
		present[canonicalProviderID(id)] = true
	}
	for _, e := range base.Errors {
		present[canonicalProviderID(e.ProviderID)] = true
	}
	for _, names := range catalogCLIAttempts() {
		canon := canonicalProviderID(names[0])
		if present[canon] {
			continue
		}
		var extra limitsV1Envelope
		found := false
		for _, name := range names {
			extraRaw, runErr := runOpenUsageCLI(ctx, binary, name)
			if runErr != nil || len(bytes.TrimSpace(extraRaw)) == 0 || !json.Valid(extraRaw) {
				continue
			}
			if err := json.Unmarshal(extraRaw, &extra); err != nil {
				continue
			}
			found = true
			break
		}
		if !found {
			continue
		}
		for id, p := range extra.Providers {
			key := canonicalProviderID(id)
			if present[key] {
				continue
			}
			if base.Providers == nil {
				base.Providers = map[string]limitsV1Provider{}
			}
			base.Providers[id] = p
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
	out, err := json.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("openusage: encode merged limits: %w", err)
	}
	return out, nil
}

func runOpenUsageCLI(ctx context.Context, binary string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, binary, args...)
	configureCommandCancellation(cmd)
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("openusage: timeout running %s %s", binary, strings.Join(args, " "))
	}
	if stdout.Len() > collectorMaxResponseBytes {
		return nil, fmt.Errorf("openusage: response too large")
	}
	if json.Valid(stdout.Bytes()) {
		return stdout.Bytes(), nil
	}
	if runErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = runErr.Error()
		}
		return nil, fmt.Errorf("openusage: %s", truncateDiagnostic(detail, 180))
	}
	return nil, fmt.Errorf("openusage: invalid JSON")
}

func normalizeOpenUsageLimitsV1(body []byte, pollIntervalMinutes int, fetchedAt time.Time) (Snapshot, error) {
	var env limitsV1Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return Snapshot{}, fmt.Errorf("normalize openusage: decode limits.v1: %w", err)
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
		SourceKind:          "openusage",
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
	priority := []string{"totalUsage", "autoUsage", "session", "weekly", "premiumCredits"}
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
		title := limitsWindowTitle(providerID, key, label)
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
	if isAPINoiseWindow(providerID, key, "") {
		return true
	}
	if !strings.EqualFold(res.Kind, "consumption") {
		return true
	}
	if !strings.EqualFold(res.Unit, "percent") {
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
	return 0, false
}

func limitsWindowTitle(providerID, key, windowLabel string) string {
	switch key {
	case "totalUsage":
		return "Plan"
	case "autoUsage":
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
	case "premiumCredits":
		return "Premium"
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
	for _, key := range []string{"credits", "creditValue"} {
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
