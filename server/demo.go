package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"time"
)

type DemoWindowState struct {
	UsedPercent float64   `json:"usedPercent"`
	ResetsAt    time.Time `json:"resetsAt"`
}

type DemoState struct {
	Primary          DemoWindowState `json:"primary"`
	Secondary        DemoWindowState `json:"secondary"`
	CreditsAvailable int             `json:"creditsAvailable"`
	Stale            bool            `json:"stale"`
	ProviderError    bool            `json:"providerError"`
	UpdatedAt        time.Time       `json:"updatedAt"`
	Revision         int64           `json:"revision"`
	LastDemoRunID    string          `json:"lastDemoRunID,omitempty"`
}

type DemoWindowPatch struct {
	UsedPercent *float64   `json:"usedPercent,omitempty"`
	ResetsAt    *time.Time `json:"resetsAt,omitempty"`
}

type DemoStatePatch struct {
	Primary          *DemoWindowPatch `json:"primary,omitempty"`
	Secondary        *DemoWindowPatch `json:"secondary,omitempty"`
	CreditsAvailable *int             `json:"creditsAvailable,omitempty"`
	Stale            *bool            `json:"stale,omitempty"`
	ProviderError    *bool            `json:"providerError,omitempty"`
}

func (p *DemoWindowPatch) UnmarshalJSON(data []byte) error {
	type plain DemoWindowPatch
	var decoded plain
	if err := decodeDemoPatch(data, &decoded); err != nil {
		return err
	}
	*p = DemoWindowPatch(decoded)
	return nil
}

func (p *DemoStatePatch) UnmarshalJSON(data []byte) error {
	type plain DemoStatePatch
	var decoded plain
	if err := decodeDemoPatch(data, &decoded); err != nil {
		return err
	}
	*p = DemoStatePatch(decoded)
	return nil
}

func decodeDemoPatch(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("demo patch: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("demo patch: unexpected trailing JSON value")
		}
		return fmt.Errorf("demo patch: %w", err)
	}
	return nil
}

func DefaultDemoState(now time.Time) DemoState {
	now = now.UTC()
	daysUntilMonday := (int(time.Monday) - int(now.Weekday()) + 7) % 7
	if daysUntilMonday == 0 {
		daysUntilMonday = 7
	}
	nextMonday := time.Date(now.Year(), now.Month(), now.Day()+daysUntilMonday, 0, 0, 0, 0, time.UTC)
	return DemoState{
		Primary: DemoWindowState{
			UsedPercent: 62,
			ResetsAt:    now.Add(2*time.Hour + 8*time.Minute),
		},
		Secondary: DemoWindowState{
			UsedPercent: 34,
			ResetsAt:    nextMonday,
		},
		CreditsAvailable: 2,
		UpdatedAt:        now,
		Revision:         1,
	}
}

func ApplyDemoPatch(state DemoState, patch DemoStatePatch, now time.Time) (DemoState, error) {
	if patch.Primary != nil {
		applyDemoWindowPatch(&state.Primary, *patch.Primary)
	}
	if patch.Secondary != nil {
		applyDemoWindowPatch(&state.Secondary, *patch.Secondary)
	}
	if patch.CreditsAvailable != nil {
		state.CreditsAvailable = *patch.CreditsAvailable
	}
	if patch.Stale != nil {
		state.Stale = *patch.Stale
	}
	if patch.ProviderError != nil {
		state.ProviderError = *patch.ProviderError
	}
	state.UpdatedAt = now

	if err := validateDemoState(state, now); err != nil {
		return DemoState{}, err
	}
	state.Revision++
	return state, nil
}

func applyDemoWindowPatch(state *DemoWindowState, patch DemoWindowPatch) {
	if patch.UsedPercent != nil {
		state.UsedPercent = *patch.UsedPercent
	}
	if patch.ResetsAt != nil {
		state.ResetsAt = *patch.ResetsAt
	}
}

func validateDemoState(state DemoState, now time.Time) error {
	if state.UpdatedAt.IsZero() {
		return fmt.Errorf("demo: updatedAt must not be zero")
	}
	if err := validateDemoWindow("primary", state.Primary, now); err != nil {
		return err
	}
	if err := validateDemoWindow("secondary", state.Secondary, now); err != nil {
		return err
	}
	if state.CreditsAvailable < 0 {
		return fmt.Errorf("demo: creditsAvailable must be non-negative")
	}
	return nil
}

func validateDemoWindow(name string, window DemoWindowState, now time.Time) error {
	if math.IsNaN(window.UsedPercent) || math.IsInf(window.UsedPercent, 0) || window.UsedPercent < 0 || window.UsedPercent > 100 {
		return fmt.Errorf("demo: %s usedPercent must be between 0 and 100", name)
	}
	if window.ResetsAt.IsZero() {
		return fmt.Errorf("demo: %s resetsAt must not be zero", name)
	}
	if window.ResetsAt.Before(now.Add(-24*time.Hour)) || window.ResetsAt.After(now.Add(31*24*time.Hour)) {
		return fmt.Errorf("demo: %s resetsAt is outside the allowed range", name)
	}
	return nil
}

func BuildDemoRaw(state DemoState) ([]byte, error) {
	type identity struct {
		Provider string `json:"provider"`
		Name     string `json:"name"`
		Stale    bool   `json:"stale"`
	}
	if state.ProviderError {
		return json.Marshal(struct {
			identity
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}{
			identity: identity{Provider: "demo", Name: "Demo", Stale: state.Stale},
			Error: struct {
				Message string `json:"message"`
			}{Message: "Synthetic demo provider error"},
		})
	}

	type rawWindow struct {
		Title         string    `json:"title"`
		UsedPercent   float64   `json:"usedPercent"`
		WindowMinutes int       `json:"windowMinutes"`
		ResetsAt      time.Time `json:"resetsAt"`
	}
	return json.Marshal(struct {
		identity
		Usage struct {
			Primary   rawWindow `json:"primary"`
			Secondary rawWindow `json:"secondary"`
		} `json:"usage"`
		CodexResetCredits struct {
			AvailableCount int `json:"availableCount"`
		} `json:"codexResetCredits"`
	}{
		identity: identity{Provider: "demo", Name: "Demo", Stale: state.Stale},
		Usage: struct {
			Primary   rawWindow `json:"primary"`
			Secondary rawWindow `json:"secondary"`
		}{
			Primary: rawWindow{
				Title: "5h limit", UsedPercent: state.Primary.UsedPercent,
				WindowMinutes: 300, ResetsAt: state.Primary.ResetsAt,
			},
			Secondary: rawWindow{
				Title: "Weekly", UsedPercent: state.Secondary.UsedPercent,
				WindowMinutes: 10080, ResetsAt: state.Secondary.ResetsAt,
			},
		},
		CodexResetCredits: struct {
			AvailableCount int `json:"availableCount"`
		}{AvailableCount: state.CreditsAvailable},
	})
}

func InjectDemoProvider(body []byte, state DemoState) ([]byte, error) {
	providers, err := extractProviderRaw(body)
	if err != nil {
		return nil, err
	}
	filtered := make([]json.RawMessage, 0, len(providers)+1)
	for _, raw := range providers {
		var identity struct {
			Provider string `json:"provider"`
			ID       string `json:"id"`
		}
		if err := json.Unmarshal(raw, &identity); err != nil {
			return nil, fmt.Errorf("demo: decode provider identity: %w", err)
		}
		if identity.Provider == "demo" || identity.ID == "demo" {
			continue
		}
		filtered = append(filtered, raw)
	}
	demo, err := BuildDemoRaw(state)
	if err != nil {
		return nil, fmt.Errorf("demo: build provider: %w", err)
	}
	filtered = append(filtered, json.RawMessage(demo))
	return json.Marshal(struct {
		Providers []json.RawMessage `json:"providers"`
	}{Providers: filtered})
}
