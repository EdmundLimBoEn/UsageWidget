package server

import (
	"math"
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	fetchedAt := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		body    string
		wantErr bool
		check   func(t *testing.T, snap Snapshot)
	}{
		{
			name: "codex session and weekly",
			body: `{
				"schema": "crossusage.limits.v1",
				"providers": {
					"codex": {
						"displayName": "Codex",
						"resources": {
							"session": {"unit":"percent","used":42,"limit":100,"remaining":58,"utilization":0.42,"resetsAt":"2026-07-17T20:00:00Z","label":"Session","windowSeconds":18000},
							"weekly": {"unit":"percent","used":11.5,"limit":100,"remaining":88.5,"utilization":0.115,"resetsAt":"2026-07-21T00:00:00Z","label":"Weekly","windowSeconds":604800}
						}
					}
				},
				"errors": []
			}`,
			check: func(t *testing.T, snap Snapshot) {
				if len(snap.Providers) != 1 {
					t.Fatalf("expected 1 provider, got %d", len(snap.Providers))
				}
				p := snap.Providers[0]
				if p.ID != "codex" || p.Name != "Codex" {
					t.Fatalf("unexpected provider id/name: %+v", p)
				}
				if len(p.Windows) != 2 {
					t.Fatalf("expected 2 windows, got %d: %+v", len(p.Windows), p.Windows)
				}
				w := p.Windows[0]
				if w.ID != "codex.session" || w.Key != "session" || math.Abs(w.UsedPercent-42) > 0.01 {
					t.Fatalf("unexpected session window: %+v", w)
				}
				if w.ResetsAt == nil || !w.ResetsAt.Equal(time.Date(2026, 7, 17, 20, 0, 0, 0, time.UTC)) {
					t.Fatalf("unexpected resetsAt: %+v", w.ResetsAt)
				}
			},
		},
		{
			name: "claude session only",
			body: `{
				"schema": "crossusage.limits.v1",
				"providers": {
					"claude": {
						"displayName": "Claude",
						"resources": {
							"session": {"unit":"percent","used":20,"limit":100,"utilization":0.2,"label":"Session"}
						}
					}
				}
			}`,
			check: func(t *testing.T, snap Snapshot) {
				p := snap.Providers[0]
				if p.ID != "claude_code" {
					t.Fatalf("expected claude_code, got %+v", p)
				}
				if len(p.Windows) != 1 {
					t.Fatalf("expected 1 window, got %d: %+v", len(p.Windows), p.Windows)
				}
				if p.Windows[0].ResetsAt != nil {
					t.Fatalf("expected nil resetsAt, got %+v", p.Windows[0].ResetsAt)
				}
			},
		},
		{
			name: "unknown provider is dropped",
			body: `{
				"schema": "crossusage.limits.v1",
				"providers": {
					"mystery": {
						"displayName": "Mystery",
						"resources": {
							"session": {"unit":"percent","used":50,"limit":100,"utilization":0.5,"label":"Session"}
						}
					}
				}
			}`,
			check: func(t *testing.T, snap Snapshot) {
				if len(snap.Providers) != 0 {
					t.Fatalf("unknown plugins must drop, got %+v", snap.Providers)
				}
			},
		},
		{
			name: "provider level error with no windows",
			body: `{
				"schema": "crossusage.limits.v1",
				"providers": {},
				"errors": [{"providerId":"codex","message":"upstream timeout"}]
			}`,
			check: func(t *testing.T, snap Snapshot) {
				p := snap.Providers[0]
				if p.Error != "upstream timeout" {
					t.Fatalf("expected error to be preserved, got %q", p.Error)
				}
				if len(p.Windows) != 0 {
					t.Fatalf("expected no windows, got %d", len(p.Windows))
				}
			},
		},
		{
			name:    "malformed JSON rejected",
			body:    `{"schema": "crossusage.limits.v1", "providers": [ this is not json }`,
			wantErr: true,
		},
		{
			name: "usage line array maps to catalog gauges",
			body: `[
				{
					"providerId": "codex",
					"displayName": "Codex",
					"lines": [
						{"type":"progress","label":"Session","used":28,"limit":100,"format":{"kind":"percent"},"resetsAt":"2025-12-04T19:15:00Z","periodDurationMs":18000000},
						{"type":"progress","label":"Weekly","used":59,"limit":100,"format":{"kind":"percent"},"resetsAt":"2025-12-05T17:00:00Z","periodDurationMs":604800000},
						{"type":"text","label":"Today","value":"$1.33"}
					]
				},
				{
					"providerId": "openai",
					"displayName": "OpenAI",
					"lines": [
						{"type":"progress","label":"API","used":88,"limit":100,"format":{"kind":"percent"}}
					]
				}
			]`,
			check: func(t *testing.T, snap Snapshot) {
				if len(snap.Providers) != 1 {
					t.Fatalf("expected 1 provider (openai dropped), got %d: %+v", len(snap.Providers), snap.Providers)
				}
				codex := snap.Providers[0]
				if codex.ID != "codex" || codex.Name != "Codex" {
					t.Fatalf("unexpected codex: %+v", codex)
				}
				if len(codex.Windows) != 2 {
					t.Fatalf("expected 2 windows, got %d: %+v", len(codex.Windows), codex.Windows)
				}
			},
		},
		{
			name:    "unrecognized payload rejected",
			body:    `{"providers":[{"id":"codex","primary":{"usedPercent":1}}]}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap, err := Normalize([]byte(tt.body), 5, fetchedAt)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if snap.PollIntervalMinutes != 5 {
				t.Fatalf("expected pollIntervalMinutes 5, got %d", snap.PollIntervalMinutes)
			}
			if !snap.FetchedAt.Equal(fetchedAt) {
				t.Fatalf("unexpected fetchedAt: %+v", snap.FetchedAt)
			}
			if snap.SourceKind != "crossusage" {
				t.Fatalf("source=%q", snap.SourceKind)
			}
			tt.check(t, snap)
		})
	}
}

func TestNormalizeProviderScopedStale(t *testing.T) {
	fetchedAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	snapshot, err := Normalize([]byte(`{"schema":"crossusage.limits.v1","providers":{"codex":{"displayName":"Codex","stale":true,"resources":{"session":{"unit":"percent","used":1,"limit":100,"utilization":0.01,"label":"Session"}}}}}`), 5, fetchedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Providers) != 1 || !snapshot.Providers[0].Stale {
		t.Fatalf("expected provider stale flag, got %#v", snapshot.Providers)
	}
	if snapshot.Stale {
		t.Fatal("provider stale must not change snapshot stale semantics")
	}
}
