package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDefaultDemoState(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	got := DefaultDemoState(now)

	if got.Primary.UsedPercent != 62 || !got.Primary.ResetsAt.Equal(now.Add(2*time.Hour+8*time.Minute)) {
		t.Fatalf("unexpected primary defaults: %#v", got.Primary)
	}
	if got.Secondary.UsedPercent != 34 || !got.Secondary.ResetsAt.Equal(time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected secondary defaults: %#v", got.Secondary)
	}
	if got.CreditsAvailable != 2 || got.Stale || got.ProviderError || !got.UpdatedAt.Equal(now) {
		t.Fatalf("unexpected demo defaults: %#v", got)
	}
}

func TestApplyDemoPatchPreservesOmittedFields(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	state := DefaultDemoState(now)
	used := 81.0

	got, err := ApplyDemoPatch(state, DemoStatePatch{
		Primary: &DemoWindowPatch{UsedPercent: &used},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Primary.UsedPercent != 81 || got.Secondary != state.Secondary || got.CreditsAvailable != state.CreditsAvailable {
		t.Fatalf("unexpected patched state: %#v", got)
	}
	if !got.UpdatedAt.Equal(now) {
		t.Fatalf("expected updatedAt %v, got %v", now, got.UpdatedAt)
	}
}

func TestApplyDemoPatchValidation(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	below := -0.1
	above := 100.1
	negativeCredits := -1
	zero := time.Time{}
	tooOld := now.Add(-24*time.Hour - time.Second)
	tooNew := now.Add(31*24*time.Hour + time.Second)

	tests := []struct {
		name  string
		patch DemoStatePatch
	}{
		{name: "primary percent below zero", patch: DemoStatePatch{Primary: &DemoWindowPatch{UsedPercent: &below}}},
		{name: "secondary percent above one hundred", patch: DemoStatePatch{Secondary: &DemoWindowPatch{UsedPercent: &above}}},
		{name: "negative credits", patch: DemoStatePatch{CreditsAvailable: &negativeCredits}},
		{name: "zero primary reset", patch: DemoStatePatch{Primary: &DemoWindowPatch{ResetsAt: &zero}}},
		{name: "secondary reset too old", patch: DemoStatePatch{Secondary: &DemoWindowPatch{ResetsAt: &tooOld}}},
		{name: "primary reset too new", patch: DemoStatePatch{Primary: &DemoWindowPatch{ResetsAt: &tooNew}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ApplyDemoPatch(DefaultDemoState(now), tt.patch, now); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestDemoStatePatchRejectsUnknownJSONFields(t *testing.T) {
	for _, input := range []string{
		`{"surprise":true}`,
		`{"primary":{"surprise":true}}`,
	} {
		var patch DemoStatePatch
		if err := json.Unmarshal([]byte(input), &patch); err == nil {
			t.Fatalf("expected unknown field in %s to fail", input)
		}
	}
}

func TestDemoStatePatchRejectsTrailingJSON(t *testing.T) {
	var patch DemoStatePatch
	if err := patch.UnmarshalJSON([]byte(`{"stale":true} {"providerError":true}`)); err == nil {
		t.Fatal("expected trailing JSON value to fail")
	}
}

func TestBuildDemoRaw(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	raw, err := BuildDemoRaw(DefaultDemoState(now))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["provider"] != "demo" || got["name"] != "Demo" || got["stale"] != false {
		t.Fatalf("unexpected provider identity: %s", raw)
	}
	usage := got["usage"].(map[string]any)
	primary := usage["primary"].(map[string]any)
	secondary := usage["secondary"].(map[string]any)
	if primary["title"] != "5h limit" || primary["windowMinutes"] != float64(300) || primary["resetsAt"] != "2026-07-18T14:08:00Z" {
		t.Fatalf("unexpected primary payload: %#v", primary)
	}
	if secondary["title"] != "Weekly" || secondary["windowMinutes"] != float64(10080) || secondary["resetsAt"] != "2026-07-20T00:00:00Z" {
		t.Fatalf("unexpected secondary payload: %#v", secondary)
	}
	if got["codexResetCredits"].(map[string]any)["availableCount"] != float64(2) {
		t.Fatalf("unexpected credits: %s", raw)
	}
}

func TestBuildDemoRawProviderError(t *testing.T) {
	state := DefaultDemoState(time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
	state.ProviderError = true
	raw, err := BuildDemoRaw(state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"usage"`) || strings.Contains(string(raw), `"codexResetCredits"`) {
		t.Fatalf("error payload must omit windows and credits: %s", raw)
	}
	var got struct {
		Provider string `json:"provider"`
		Error    struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Provider != "demo" || got.Error.Message != "Synthetic demo provider error" {
		t.Fatalf("unexpected error payload: %s", raw)
	}
}

func TestInjectDemoProviderSupportedRootShapes(t *testing.T) {
	state := DefaultDemoState(time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
	tests := []struct {
		name  string
		input string
	}{
		{name: "single", input: `{"provider":"claude","usage":{}}`},
		{name: "array", input: `[{"provider":"claude","usage":{}}]`},
		{name: "wrapped", input: `{"providers":[{"provider":"claude","usage":{}}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged, err := InjectDemoProvider([]byte(tt.input), state)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := Normalize(merged, 5, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Providers) != 2 || snapshot.Providers[0].ID != "claude" || snapshot.Providers[1].ID != "demo" {
				t.Fatalf("unexpected providers: %#v", snapshot.Providers)
			}
		})
	}
}

func TestInjectDemoProviderReplacesCollision(t *testing.T) {
	state := DefaultDemoState(time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
	input := []byte(`{"providers":[{"provider":"claude","usage":{}},{"provider":"demo","usage":{}},{"id":"demo"}]}`)

	merged, err := InjectDemoProvider(input, state)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := Normalize(merged, 5, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Providers) != 2 || snapshot.Providers[1].ID != "demo" || len(snapshot.Providers[1].Windows) != 2 {
		t.Fatalf("unexpected providers: %#v", snapshot.Providers)
	}
}
