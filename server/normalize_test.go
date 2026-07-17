package server

import (
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
			name: "codex fixture with primary secondary extra credits",
			body: `{
				"providers": [
					{
						"id": "codex",
						"name": "Codex",
						"primary":   {"title": "5h limit", "usedPercent": 42.0, "resetsAt": "2026-07-17T20:00:00Z"},
						"secondary": {"title": "Weekly", "usedPercent": 11.5, "resetsAt": "2026-07-21T00:00:00Z"},
						"tertiary":  null,
						"extraRateWindows": [
							{"key": "opus", "title": "Opus weekly", "usedPercent": 3.0, "resetsAt": "2026-07-21T00:00:00Z"}
						],
						"codexResetCredits": {"availableCount": 2},
						"error": null
					}
				]
			}`,
			check: func(t *testing.T, snap Snapshot) {
				if len(snap.Providers) != 1 {
					t.Fatalf("expected 1 provider, got %d", len(snap.Providers))
				}
				p := snap.Providers[0]
				if p.ID != "codex" || p.Name != "Codex" {
					t.Fatalf("unexpected provider id/name: %+v", p)
				}
				if len(p.Windows) != 3 {
					t.Fatalf("expected 3 windows, got %d: %+v", len(p.Windows), p.Windows)
				}
				w := p.Windows[0]
				if w.ID != "codex.primary" || w.Key != "primary" || w.UsedPercent != 42.0 || w.RemainingPercent != 58.0 {
					t.Fatalf("unexpected primary window: %+v", w)
				}
				if w.ResetsAt == nil || !w.ResetsAt.Equal(time.Date(2026, 7, 17, 20, 0, 0, 0, time.UTC)) {
					t.Fatalf("unexpected resetsAt: %+v", w.ResetsAt)
				}
				extra := p.Windows[2]
				if extra.ID != "codex.opus" || extra.Key != "opus" {
					t.Fatalf("unexpected extra window: %+v", extra)
				}
				if p.Credits == nil || p.Credits.AvailableCount != 2 {
					t.Fatalf("unexpected credits: %+v", p.Credits)
				}
				if p.Raw == nil {
					t.Fatalf("expected raw JSON to be preserved")
				}
			},
		},
		{
			name: "claude fixture missing secondary and tertiary",
			body: `{
				"providers": [
					{
						"id": "claude",
						"name": "Claude",
						"primary": {"title": "5h limit", "usedPercent": 20.0, "resetsAt": null},
						"secondary": null,
						"tertiary": null
					}
				]
			}`,
			check: func(t *testing.T, snap Snapshot) {
				p := snap.Providers[0]
				if len(p.Windows) != 1 {
					t.Fatalf("expected 1 window, got %d: %+v", len(p.Windows), p.Windows)
				}
				if p.Windows[0].ResetsAt != nil {
					t.Fatalf("expected nil resetsAt, got %+v", p.Windows[0].ResetsAt)
				}
			},
		},
		{
			name: "grok fixture with tertiary window",
			body: `{
				"providers": [
					{
						"id": "grok",
						"name": "Grok",
						"primary": {"title": "5h limit", "usedPercent": 5.0, "resetsAt": "2026-07-17T20:00:00Z"},
						"secondary": {"title": "Weekly", "usedPercent": 8.0, "resetsAt": "2026-07-21T00:00:00Z"},
						"tertiary": {"title": "Monthly", "usedPercent": 1.0, "resetsAt": "2026-08-01T00:00:00Z"}
					}
				]
			}`,
			check: func(t *testing.T, snap Snapshot) {
				p := snap.Providers[0]
				if len(p.Windows) != 3 {
					t.Fatalf("expected 3 windows, got %d", len(p.Windows))
				}
				if p.Windows[2].ID != "grok.tertiary" {
					t.Fatalf("unexpected tertiary window id: %s", p.Windows[2].ID)
				}
			},
		},
		{
			name: "extra window without key derives slug from title",
			body: `{
				"providers": [
					{
						"id": "codex",
						"name": "Codex",
						"extraRateWindows": [
							{"title": "Opus Weekly!!", "usedPercent": 3.0, "resetsAt": null}
						]
					}
				]
			}`,
			check: func(t *testing.T, snap Snapshot) {
				w := snap.Providers[0].Windows[0]
				if w.Key != "opus-weekly" || w.ID != "codex.opus-weekly" {
					t.Fatalf("unexpected derived key/id: key=%s id=%s", w.Key, w.ID)
				}
			},
		},
		{
			name: "extra window key collision gets numeric suffix",
			body: `{
				"providers": [
					{
						"id": "codex",
						"name": "Codex",
						"primary": {"title": "5h limit", "usedPercent": 1.0, "resetsAt": null},
						"extraRateWindows": [
							{"key": "primary", "title": "Duplicate", "usedPercent": 2.0, "resetsAt": null}
						]
					}
				]
			}`,
			check: func(t *testing.T, snap Snapshot) {
				windows := snap.Providers[0].Windows
				if len(windows) != 2 {
					t.Fatalf("expected 2 windows, got %d", len(windows))
				}
				if windows[0].Key != "primary" {
					t.Fatalf("expected first window key primary, got %s", windows[0].Key)
				}
				if windows[1].Key != "primary-2" || windows[1].ID != "codex.primary-2" {
					t.Fatalf("expected collision-resolved key primary-2, got %+v", windows[1])
				}
			},
		},
		{
			name: "unknown provider passes through untouched",
			body: `{
				"providers": [
					{
						"id": "mystery",
						"name": "Mystery Provider",
						"primary": {"title": "Daily", "usedPercent": 50.0, "resetsAt": null}
					}
				]
			}`,
			check: func(t *testing.T, snap Snapshot) {
				p := snap.Providers[0]
				if p.ID != "mystery" || p.Name != "Mystery Provider" {
					t.Fatalf("unexpected unknown provider: %+v", p)
				}
				if len(p.Windows) != 1 {
					t.Fatalf("expected 1 window, got %d", len(p.Windows))
				}
			},
		},
		{
			name: "null resetsAt preserved as nil",
			body: `{
				"providers": [
					{
						"id": "codex",
						"name": "Codex",
						"primary": {"title": "5h limit", "usedPercent": 10.0, "resetsAt": null}
					}
				]
			}`,
			check: func(t *testing.T, snap Snapshot) {
				if snap.Providers[0].Windows[0].ResetsAt != nil {
					t.Fatalf("expected nil resetsAt")
				}
			},
		},
		{
			name: "provider level error with no windows",
			body: `{
				"providers": [
					{
						"id": "codex",
						"name": "Codex",
						"error": "upstream timeout"
					}
				]
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
			body:    `{"providers": [ this is not json }`,
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
			tt.check(t, snap)
		})
	}
}
