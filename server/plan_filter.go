package server

import (
	"slices"
	"strings"
)

type providerCatalogEntry struct {
	ID      string
	Name    string
	Aliases []string
}

var providerCatalog = []providerCatalogEntry{
	{ID: "cursor", Name: "Cursor", Aliases: []string{"cursor_ai", "cursor_ide"}},
	{ID: "codex", Name: "Codex", Aliases: []string{"codex_cli"}},
	{ID: "claude_code", Name: "Claude Code", Aliases: []string{"claude"}},
	{ID: "grok", Name: "Grok", Aliases: []string{"xai"}},
	{ID: "copilot", Name: "Copilot", Aliases: []string{"github_copilot"}},
	{ID: "gemini_cli", Name: "Gemini", Aliases: []string{"gemini"}},
}

var defaultProviderOrder = []string{"cursor", "codex", "claude_code", "grok"}

var (
	aliasToCanonical map[string]string
	catalogNames     map[string]string
)

func init() {
	aliasToCanonical = make(map[string]string, len(providerCatalog)*3)
	catalogNames = make(map[string]string, len(providerCatalog))
	for _, e := range providerCatalog {
		catalogNames[e.ID] = e.Name
		aliasToCanonical[e.ID] = e.ID
		for _, alias := range e.Aliases {
			aliasToCanonical[alias] = e.ID
		}
	}
}

func canonicalProviderID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	id = strings.ReplaceAll(id, "-", "_")
	id = strings.ReplaceAll(id, " ", "_")
	if canon, ok := aliasToCanonical[id]; ok {
		return canon
	}
	return id
}

func inProviderCatalog(id string) bool {
	_, ok := catalogNames[canonicalProviderID(id)]
	return ok
}

func catalogDisplayName(id string) (string, bool) {
	name, ok := catalogNames[canonicalProviderID(id)]
	return name, ok
}

func displayNameForProvider(id string) string {
	canon := canonicalProviderID(id)
	if name, ok := catalogNames[canon]; ok {
		return name
	}
	return displayName(strings.ReplaceAll(canon, "_", " "))
}

func keepPlanProvider(p Provider) bool {
	if !inProviderCatalog(p.ID) {
		return false
	}
	return len(p.Windows) > 0 || p.Error != "" || p.Stale
}

func isAPINoiseWindow(providerID, key, title string) bool {
	blob := strings.ToLower(strings.TrimSpace(key + " " + title))
	if blob == "" {
		return false
	}
	if strings.Contains(blob, "third party") || strings.Contains(blob, "third-party") {
		return true
	}
	if canonicalProviderID(providerID) == "cursor" && key == "tertiary" {
		return true
	}
	if strings.Contains(blob, "plan_api") || blob == "api" || strings.HasPrefix(blob, "api ") || strings.Contains(blob, " api") {
		return true
	}
	return false
}

func planWindowTitle(providerID, key, title string, minutes *float64) string {
	if canonicalProviderID(providerID) == "cursor" {
		switch key {
		case "primary", "plan_percent_used", "plan-percent-used":
			return "Plan"
		case "secondary", "plan_auto_percent_used", "plan-auto-percent-used":
			return "Auto"
		}
	}
	if title != "" {
		return title
	}
	return windowTitle(key, minutes)
}

func sanitizeProviderIDs(ids []string, ensureCursor bool) []string {
	seen := make(map[string]bool, len(ids)+1)
	out := make([]string, 0, len(ids)+1)
	for _, id := range ids {
		canon := canonicalProviderID(id)
		if canon == "" || !inProviderCatalog(canon) || seen[canon] {
			continue
		}
		seen[canon] = true
		out = append(out, canon)
	}
	if ensureCursor && !seen["cursor"] {
		out = append([]string{"cursor"}, out...)
	}
	return out
}

func sanitizeSettings(s *Settings) bool {
	order := sanitizeProviderIDs(s.ProviderOrder, true)
	hidden := sanitizeProviderIDs(s.HiddenProviders, false)
	dirty := !slices.Equal(order, s.ProviderOrder) || !slices.Equal(hidden, s.HiddenProviders)
	s.ProviderOrder = order
	s.HiddenProviders = hidden
	return dirty
}

func projectCatalogProviders(providers []Provider) []Provider {
	byID := make(map[string]Provider, len(providers))
	order := make([]string, 0, len(providers))
	for _, p := range providers {
		id := canonicalProviderID(p.ID)
		if id == "" || !inProviderCatalog(id) {
			continue
		}
		p.ID = id
		if name, ok := catalogDisplayName(id); ok {
			p.Name = name
		}
		if existing, ok := byID[id]; ok {
			if len(p.Windows) > len(existing.Windows) {
				byID[id] = p
			}
			continue
		}
		byID[id] = p
		order = append(order, id)
	}
	out := make([]Provider, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}

func fillMissingCatalogSlots(providers []Provider, order, hidden []string) []Provider {
	hiddenSet := make(map[string]bool, len(hidden))
	for _, id := range hidden {
		hiddenSet[canonicalProviderID(id)] = true
	}
	seen := make(map[string]bool, len(providers)+len(order))
	for _, p := range providers {
		seen[canonicalProviderID(p.ID)] = true
	}
	out := append([]Provider(nil), providers...)
	for _, id := range order {
		canon := canonicalProviderID(id)
		if canon == "" || hiddenSet[canon] || seen[canon] {
			continue
		}
		name, ok := catalogDisplayName(canon)
		if !ok {
			continue
		}
		out = append(out, Provider{
			ID:      canon,
			Name:    name,
			Error:   "Not in the latest collection",
			Windows: []Window{},
		})
		seen[canon] = true
	}
	return out
}
