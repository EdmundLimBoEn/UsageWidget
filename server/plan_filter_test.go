package server

import "testing"

func TestKeepPlanProvider(t *testing.T) {
	if keepPlanProvider(Provider{ID: "openai", Windows: []Window{{Title: "API credits"}}}) {
		t.Fatal("openai spend/API must drop even with a window")
	}
	if keepPlanProvider(Provider{ID: "openrouter", Error: "No available fetch strategy for openrouter."}) {
		t.Fatal("API plugin errors must drop")
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
}

func TestAPINoiseWindow(t *testing.T) {
	if !isAPINoiseWindow("cursor", "tertiary", "Third Party") {
		t.Fatal("cursor third-party/API bar should drop")
	}
	if isAPINoiseWindow("cursor", "primary", "Plan") || isAPINoiseWindow("codex", "primary", "5h limit") {
		t.Fatal("plan windows should stay")
	}
}
