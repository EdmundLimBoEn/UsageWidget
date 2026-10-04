package server

import (
	"strings"
	"testing"
	"time"
)

func TestKeepPlanProvider(t *testing.T) {
	for _, id := range []string{"openai", "openrouter", "azureopenai", "opencode"} {
		if keepPlanProvider(Provider{ID: id, Windows: []Window{{Title: "API credits"}}}) {
			t.Fatalf("%s with windows must drop", id)
		}
	}
	if !keepPlanProvider(Provider{ID: "cursor", Windows: []Window{{Title: "Plan"}}}) {
		t.Fatal("cursor plan must stay")
	}
	if !keepPlanProvider(Provider{ID: "cursor", Error: "authentication required"}) {
		t.Fatal("cursor errors must stay so last-known usage can fill in")
	}
	if keepPlanProvider(Provider{ID: "mystery", Error: "No available fetch strategy"}) {
		t.Fatal("unknown error-only providers must drop")
	}
	if !keepPlanProvider(Provider{ID: "antigravity", Windows: []Window{{Title: "Plan"}}}) {
		t.Fatal("antigravity is the Gemini catalog member")
	}
	if !keepPlanProvider(Provider{ID: "devin", Windows: []Window{{Title: "Credits"}}}) {
		t.Fatal("Devin plan credits must stay")
	}
	if !keepPlanProvider(Provider{ID: "cognition", Error: "authentication required"}) {
		t.Fatal("Devin errors must stay so last-known usage can fill in")
	}
}

func TestDefaultProviderOrderIncludesDevin(t *testing.T) {
	want := []string{"cursor", "codex", "claude_code", "copilot", "gemini_cli", "grok", "devin"}
	if strings.Join(defaultProviderOrder, ",") != strings.Join(want, ",") {
		t.Fatalf("defaultProviderOrder=%v", defaultProviderOrder)
	}
	if !inProviderCatalog("devin") || !inProviderCatalog("cognition") {
		t.Fatal("Devin catalog membership")
	}
	order := sanitizeProviderIDs([]string{"cursor", "codex"}, true)
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("filled order=%v", order)
	}
	hidden := sanitizeProviderIDs([]string{"cognition", "mystery"}, false)
	if len(hidden) != 1 || hidden[0] != "devin" {
		t.Fatalf("hidden=%v", hidden)
	}
}

func TestCanonicalProviderID(t *testing.T) {
	cases := map[string]string{
		"Cursor-IDE":     "cursor",
		"cursor_ai":      "cursor",
		"claude":         "claude_code",
		"codex_cli":      "codex",
		"xai":            "grok",
		"github_copilot": "copilot",
		"gemini":         "gemini_cli",
		"antigravity":    "gemini_cli",
		"cognition":      "devin",
		"windsurf":       "devin",
		"codeium":        "devin",
		"openai":         "openai",
	}
	for in, want := range cases {
		if got := canonicalProviderID(in); got != want {
			t.Fatalf("canonicalProviderID(%q)=%q want %q", in, got, want)
		}
	}
}

func TestNormalizeCanonicalizesTrackedProviders(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)

	cursor, err := Normalize([]byte(`{"schema":"crossusage.limits.v1","providers":{"cursor":{"resources":{"total-usage":{"unit":"percent","used":10,"limit":100,"utilization":0.1,"label":"Total usage"}}}}}`), 5, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(cursor.Providers) != 1 || cursor.Providers[0].ID != "cursor" || cursor.Providers[0].Name != "Cursor" {
		t.Fatalf("cursor: %+v", cursor.Providers)
	}

	claude, err := Normalize([]byte(`{"schema":"crossusage.limits.v1","providers":{"claude":{"resources":{"session":{"unit":"percent","used":20,"limit":100,"utilization":0.2,"windowSeconds":18000,"label":"Session"}}}}}`), 5, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(claude.Providers) != 1 || claude.Providers[0].ID != "claude_code" || claude.Providers[0].Name != "Claude Code" {
		t.Fatalf("claude: %+v", claude.Providers)
	}
}

func TestProjectCatalogProvidersDropsNoiseAndMergesAliases(t *testing.T) {
	got := projectCatalogProviders([]Provider{
		{ID: "openai", Name: "OpenAI", Windows: []Window{{Title: "API"}}},
		{ID: "claude", Name: "Claude", Windows: []Window{{Title: "5h", UsedPercent: 10}}},
		{ID: "claude_code", Name: "Claude Code", Windows: []Window{{Title: "5h", UsedPercent: 20}, {Title: "7d"}}},
		{ID: "codex", Name: "Codex"},
	})
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].ID != "claude_code" || got[0].Name != "Claude Code" || len(got[0].Windows) != 2 {
		t.Fatalf("merged claude: %+v", got[0])
	}
	if got[1].ID != "codex" || got[1].Name != "Codex" {
		t.Fatalf("codex: %+v", got[1])
	}
}

func TestAPINoiseWindow(t *testing.T) {
	if !isAPINoiseWindow("cursor", "tertiary", "Third Party") {
		t.Fatal("cursor third-party/API bar should drop")
	}
	if isAPINoiseWindow("cursor", "primary", "Plan") || isAPINoiseWindow("codex", "primary", "5h limit") {
		t.Fatal("plan windows should stay")
	}
	if !isAPINoiseWindow("cursor", "apiUsage", "") || !isAPINoiseWindow("cursor", "grokBot", "") {
		t.Fatal("cursor API/grokBot gauges should drop")
	}
}
