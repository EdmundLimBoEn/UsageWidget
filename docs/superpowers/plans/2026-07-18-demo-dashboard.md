# UsageWidget Demo Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the approved Lab Console at `demo.usagewidget.edmundlim.systems`, controlling one synthetic demo provider through UsageWidget’s real normalization, persistence, event, APNs, iOS app, and widget pipeline.

**Architecture:** Cloudflare Access protects a Cloudflare Tunnel that forwards only to a dedicated `127.0.0.1:8378` listener on edServe. That listener serves the static console and a demo-only API. Demo state is persisted in SQLite and injected into the raw upstream payload before the existing normalizer; the existing `:8377` bearer-authenticated Tailscale API remains unchanged.

**Tech Stack:** Go 1.26 standard library, `modernc.org/sqlite`, vanilla HTML/CSS/TypeScript, Bun for frontend tests and bundling, Swift/SwiftUI/WidgetKit, Cloudflare Access and Tunnel.

## Global Constraints

- Preserve `.superpowers/brainstorm/79315-1784346965/content/lab-console.html` as the visual source of truth.
- The web dashboard controls only the synthetic demo provider.
- Keep the frontend clean, restrained, dark, keyboard accessible, responsive, and reduced-motion aware.
- Do not add React, a component framework, a runtime JavaScript dependency, Cloudflare Pages, or a Worker.
- Bind the demo listener to `127.0.0.1:8378`; reject wildcard or public demo-listener addresses.
- Keep the existing `:8377` Tailscale API, bearer authentication, CLI, and iOS API client behavior unchanged.
- Demo state and event keys use the `demo.*` namespace; requests never accept an arbitrary provider ID.
- Inject the synthetic provider before `Normalize`; do not create a parallel demo normalization/event pipeline.
- `Snapshot.Stale` remains the whole-upstream failure flag. Demo staleness is provider-scoped.
- Never expose real-provider, health, settings, devices, database, APNs administration, or deployment routes through the demo listener.
- Before every git command, run `curl -s api.ipify.org`; if it returns `223.25.70.145`, enable the `edserve` Tailscale exit node for that command and restore it immediately afterward.
- Frontend implementation/review should use a Fable agent when Claude workers are available; while unavailable, use Codex workers with this plan and the approved mockup as context.

---

## File Structure

Create:

- `server/demo.go` — demo state types, defaults, patch validation, raw provider construction, and upstream injection.
- `server/demo_test.go` — state, patch, raw shape, collision, and upstream-shape tests.
- `server/demo_store.go` — additive demo SQLite schema plus state, run, and event persistence.
- `server/demo_store_test.go` — persistence, retention, filtering, and namespace tests.
- `server/demo_api.go` — embedded assets, narrow demo mux, API handlers, limits, security headers, and response shaping.
- `server/demo_api_test.go` — route allowlist, static assets, validation, errors, and redaction tests.
- `server/web/index.html` — semantic production markup derived from the approved Lab Console.
- `server/web/styles.css` — approved responsive visual direction.
- `server/web/model.ts` — JSON contracts and pure UI helpers.
- `server/web/model.test.ts` — Bun tests for patches, filters, resets, and formatting.
- `server/web/app.ts` — DOM binding and same-origin API client.
- `server/web/app.js` — committed Bun-generated browser bundle embedded in the Go binary.
- `server/web/package.json` — Bun test/build scripts with no dependencies.

Modify:

- `server/config.go` and `server/config_test.go` — `DemoListenAddr` loading and loopback validation.
- `server/store.go` and `server/store_test.go` — execute additive demo schema from `OpenStore`.
- `server/normalize.go` and `server/normalize_test.go` — provider-level stale field.
- `server/events.go` and `server/events_test.go` — detailed outcomes, demo-key namespace, stale/error skipping.
- `server/poller.go` and `server/poller_test.go` — one shared poll path, pre-normalization injection, pipeline stage and delivery capture.
- `server/apns.go` and `server/apns_test.go` — delivery counts and notifier-enabled status without breaking existing test doubles.
- `server/api.go` and `server/api_test.go` — share the existing test-alert operation while preserving the `:8377` route.
- `server/cmd/usagewidgetd/main.go` — dual-listener startup, failure propagation, and graceful shutdown.
- `server/deploy/redeploy.sh` and `server/deploy/README.md` — Bun build, listener smoke checks, Tunnel/Access instructions, rollback.
- `README.md` and `HUMANS.md` — architecture, demo API, human Cloudflare and device verification steps.
- `ios/Sources/Core/Models.swift` — provider-level stale decoding.
- `ios/Sources/App/DashboardView.swift` — demo-provider stale indicator.
- `ios/Sources/Widget/ProviderWidget.swift` — demo-provider stale indicator.
- `ios/Tests/CoreTests/ModelsAndStoreTests.swift` — provider stale decoding compatibility.

Do not modify:

- `server/deploy/usagewidget.service` — embedded assets and the second socket need no new filesystem permission.
- `cli/usagewidget` — it stays on the existing bearer-authenticated `:8377` API.
- `ios/Sources/Core/APIClient.swift` — the phone continues using its existing server contract.

---

### Task 1: Persist and Normalize the Demo Provider

**Files:**
- Create: `server/demo.go`
- Create: `server/demo_test.go`
- Create: `server/demo_store.go`
- Create: `server/demo_store_test.go`
- Modify: `server/store.go:15-71`
- Modify: `server/normalize.go:10-210`
- Modify: `server/store_test.go`
- Modify: `server/normalize_test.go`

**Interfaces:**
- Produces: `DemoState`, `DemoStatePatch`, `DefaultDemoState`, `ApplyDemoPatch`, `BuildDemoRaw`, `InjectDemoProvider`.
- Produces: `Store.LoadDemoState`, `Store.SaveDemoState`, `Store.SaveDemoRun`, `Store.LatestDemoRun`, `Store.AppendDemoEvents`, `Store.ListDemoEvents`.
- Produces: `Provider.Stale bool` in normalized JSON.
- Consumes: existing `Store`, `Normalize`, and `extractProviderRaw` behavior.

- [ ] **Step 1: Write failing demo-domain tests**

Add table-driven tests in `server/demo_test.go` covering defaults, omitted-field preservation, validation, raw generation, all supported upstream payload shapes, and replacement of an upstream `demo` collision.

```go
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
}

func TestInjectDemoProviderReplacesCollision(t *testing.T) {
	state := DefaultDemoState(time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
	input := []byte(`{"providers":[{"provider":"claude","usage":{}},{"provider":"demo","usage":{}}]}`)

	merged, err := InjectDemoProvider(input, state)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := Normalize(merged, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Providers) != 2 || snapshot.Providers[1].ID != "demo" {
		t.Fatalf("unexpected providers: %#v", snapshot.Providers)
	}
}
```

Validation cases must reject percentages outside `0...100`, negative credits, zero reset timestamps, reset timestamps before `now-24h` or after `now+31d`, and unknown JSON fields.

- [ ] **Step 2: Run the focused tests and confirm they fail**

Run:

```bash
cd server
go test . -run 'TestApplyDemo|TestBuildDemo|TestInjectDemo|TestNormalizeDemo'
```

Expected: compile failure because `DemoState`, `ApplyDemoPatch`, `BuildDemoRaw`, `InjectDemoProvider`, or `Provider.Stale` does not exist.

- [ ] **Step 3: Implement demo state and patch validation**

Create `server/demo.go` with these public types:

```go
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
	UpdatedAt        time.Time        `json:"updatedAt"`
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
```

`DefaultDemoState(now)` returns primary `62% / now+2h08m`, secondary `34% / next Monday 00:00 UTC`, two credits, and false stale/error flags. `ApplyDemoPatch` copies the state, applies only non-nil fields, validates the complete result, and sets `UpdatedAt = now`.

- [ ] **Step 4: Implement raw provider creation and injection**

`BuildDemoRaw` emits the existing CodexBar-compatible shape:

```json
{
  "provider": "demo",
  "name": "Demo",
  "stale": false,
  "usage": {
    "primary": {"title":"5h limit","usedPercent":62,"windowMinutes":300,"resetsAt":"2026-07-18T14:08:00Z"},
    "secondary": {"title":"Weekly","usedPercent":34,"windowMinutes":10080,"resetsAt":"2026-07-20T00:00:00Z"}
  },
  "codexResetCredits": {"availableCount":2}
}
```

When `ProviderError` is true, emit provider identity plus `{"error":{"message":"Synthetic demo provider error"}}` and omit windows. `InjectDemoProvider` must parse the same root forms supported by `extractProviderRaw`, remove any existing provider whose `provider` or `id` equals `demo`, append the synthetic provider, and encode `{"providers":[...]}`.

- [ ] **Step 5: Add provider-scoped stale normalization**

Extend the normalized provider and raw payload types:

```go
type Provider struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Error   string          `json:"error,omitempty"`
	Stale   bool            `json:"stale,omitempty"`
	Windows []Window        `json:"windows"`
	Credits *Credits        `json:"credits,omitempty"`
	Raw     json.RawMessage `json:"raw,omitempty"`
}
```

Copy raw `stale` to `Provider.Stale`. Do not alter `Snapshot.Stale` semantics.

- [ ] **Step 6: Add additive SQLite demo schema and store methods**

Create `demoSchema` in `server/demo_store.go` and execute it after the existing schema in `OpenStore`:

```sql
CREATE TABLE IF NOT EXISTS demo_state (
    key TEXT PRIMARY KEY CHECK (key = 'demo.state'),
    payload TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS demo_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at TEXT NOT NULL,
    completed_at TEXT NOT NULL,
    success INTEGER NOT NULL,
    payload TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS demo_event_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id INTEGER,
    event_key TEXT NOT NULL CHECK (event_key LIKE 'demo.%'),
    event_type TEXT NOT NULL,
    created_at TEXT NOT NULL,
    payload TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_demo_event_log_recent ON demo_event_log(id DESC);
CREATE INDEX IF NOT EXISTS idx_demo_event_log_type_recent ON demo_event_log(event_type, id DESC);
```

Implement complete-JSON upsert for `demo.state`, seed defaults only when absent, retain the latest 20 runs and 500 event rows, default event limit to 50, and cap it at 100. Never prune the existing dedup `events` table.

- [ ] **Step 7: Run focused and full server tests**

```bash
cd server
go test . -run 'TestApplyDemo|TestBuildDemo|TestInjectDemo|TestNormalizeDemo|TestDemoState|TestDemoRun|TestDemoEventStore'
go test ./...
```

Expected: PASS.

- [ ] **Step 8: Commit the task**

```bash
curl -s api.ipify.org
# If required: tailscale set --exit-node=edserve
git add server/demo.go server/demo_test.go server/demo_store.go server/demo_store_test.go server/store.go server/store_test.go server/normalize.go server/normalize_test.go
git commit -m "Add persisted demo provider state"
# If enabled: tailscale set --exit-node=
```

---

### Task 2: Produce Namespaced Detailed Demo Events

**Files:**
- Modify: `server/events.go:8-220`
- Modify: `server/events_test.go`
- Modify: `server/api.go` demo test-alert helper
- Modify: `server/api_test.go`

**Interfaces:**
- Consumes: `Provider.Stale`, existing `EventEngine`, `Store.RecordEvent`, and window-state baselines.
- Produces: `EventValue`, `EventOutcome`, `EventProcessResult`, and `EventEngine.ProcessDetailed`.
- Preserves: existing `EventEngine.Process` signature for all current callers.

- [ ] **Step 1: Write failing detailed-event tests**

Add tests that prove:

```go
func TestProcessDetailedReturnsDeduplicatedOutcome(t *testing.T) {
	// Establish a demo.primary baseline below the early threshold.
	// Process a crossing once, then process the identical candidate again.
	// The second result must contain one outcome with Deduplicated=true and no emitted event.
}

func TestStaleDemoProviderDoesNotEmitOrAdvanceBaseline(t *testing.T) {
	// Persist a demo.primary baseline, process Provider{ID:"demo", Stale:true},
	// and assert no event plus an unchanged window_state row.
}

func TestRealProviderKeysRemainUnchanged(t *testing.T) {
	// Existing non-demo event keys must stay byte-for-byte identical.
}
```

Also test that demo keys begin with `demo.`, credits use `demo.credits`, and provider errors skip both event creation and baseline advancement.

- [ ] **Step 2: Run tests and confirm the failure**

```bash
cd server
go test . -run 'TestProcessDetailed|TestDemoEvent|TestStaleDemoProvider|TestRealProviderKeys'
```

Expected: compile failure because `ProcessDetailed` and detailed outcome types do not exist, or assertion failure because current keys are not namespaced.

- [ ] **Step 3: Add detailed event result types**

```go
type EventValue struct {
	UsedPercent      *float64   `json:"usedPercent,omitempty"`
	ResetsAt         *time.Time `json:"resetsAt,omitempty"`
	CreditsAvailable *int      `json:"creditsAvailable,omitempty"`
}

type EventOutcome struct {
	Event        Event
	Before       EventValue
	After        EventValue
	Deduplicated bool
}

type EventProcessResult struct {
	Emitted  []Event
	Outcomes []EventOutcome
}
```

Implement:

```go
func (e *EventEngine) ProcessDetailed(snap Snapshot, settings Settings, now time.Time) (EventProcessResult, error)

func (e *EventEngine) Process(snap Snapshot, settings Settings, now time.Time) ([]Event, error) {
	result, err := e.ProcessDetailed(snap, settings, now)
	return result.Emitted, err
}
```

`Outcomes` includes both newly claimed and deduplicated candidates. Only `Emitted` is dispatched.

- [ ] **Step 4: Namespace only demo keys**

Use these persisted identifiers:

```text
demo.state
demo.primary
demo.secondary
demo.credits
demo.event.early:demo.primary:<cycle>
demo.event.danger:demo.primary:<cycle>
demo.event.reset:demo.primary:<cycle>
demo.event.tibo:demo.primary:<cycle>
demo.event.credits:<count>
demo.test_alert.<timestamp>
```

Do not change real-provider keys. Change the existing synthetic alert event to type `test_alert` and a `demo.*` key while preserving the existing bearer route behavior on `:8377`.

- [ ] **Step 5: Skip stale and errored providers**

At the provider loop boundary, continue without event detection or baseline updates when `provider.Stale` or `provider.Error != ""`. This prevents stale/error toggles from manufacturing threshold or reset events.

- [ ] **Step 6: Run tests**

```bash
cd server
go test . -run 'TestProcessDetailed|TestDemoEvent|TestStaleDemoProvider|TestRealProviderKeys|TestDemoAlert'
go test ./...
```

Expected: PASS, including all existing event tests.

- [ ] **Step 7: Commit the task**

```bash
curl -s api.ipify.org
git add server/events.go server/events_test.go server/api.go server/api_test.go
git commit -m "Record detailed demo event outcomes"
```

---

### Task 3: Run Demo State Through the Shared Poll Pipeline

**Files:**
- Modify: `server/poller.go:11-180`
- Modify: `server/poller_test.go`
- Modify: `server/apns.go:22-220`
- Modify: `server/apns_test.go`
- Modify: `server/demo_store.go`
- Modify: `server/demo_store_test.go`

**Interfaces:**
- Consumes: `Store.LoadDemoState`, `InjectDemoProvider`, `EventEngine.ProcessDetailed`, existing notifier and poll mutex.
- Produces: `DemoPipelineResult`, `DemoPipelineStage`, `DemoDeliveryResult`, `DemoEventRecord`, and `Poller.PollDemoNow`.
- Preserves: `Poller.PollNow` and the scheduled poll path.

- [ ] **Step 1: Write failing poll-pipeline tests**

Cover:

- A real upstream payload plus demo state yields both providers.
- The demo raw provider is injected before normalization.
- Real provider values and baselines remain unchanged.
- `PollNow` and `PollDemoNow` serialize on the same mutex.
- Stage status is `ok`, `warning`, `failed`, or `skipped`.
- Partial APNs failures produce an APNs warning but do not fail the whole pipeline.
- Disabled/no-token APNs produces `skipped`.
- Demo run and event rows persist.
- A demo-state load or normalize failure does not overwrite the last stored snapshot.

```go
func TestPollDemoNowPreservesRealProvider(t *testing.T) {
	// Stub upstream with one real provider, persist demo state, run PollDemoNow,
	// then assert both providers exist and the real provider matches its normalized input.
}
```

- [ ] **Step 2: Run tests and confirm the failure**

```bash
cd server
go test . -run 'TestPoller.*Demo|TestDemoPoll|TestPollDelivery'
```

Expected: compile failure because `PollDemoNow` and pipeline types do not exist.

- [ ] **Step 3: Add pipeline result contracts**

```go
type DemoStageStatus string

const (
	DemoStageOK      DemoStageStatus = "ok"
	DemoStageWarning DemoStageStatus = "warning"
	DemoStageFailed  DemoStageStatus = "failed"
	DemoStageSkipped DemoStageStatus = "skipped"
)

type DemoPipelineStage struct {
	ID         string          `json:"id"`
	Status     DemoStageStatus `json:"status"`
	Detail     string          `json:"detail,omitempty"`
	DurationMS int64           `json:"durationMs"`
}

type DeliveryCount struct {
	Attempted int `json:"attempted"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}

type DemoDeliveryResult struct {
	Alerts        DeliveryCount `json:"alerts"`
	WidgetRefresh DeliveryCount `json:"widgetRefresh"`
}

type DemoPipelineResult struct {
	ID                 int64               `json:"id"`
	StartedAt          time.Time           `json:"startedAt"`
	CompletedAt        time.Time           `json:"completedAt"`
	Success            bool                `json:"success"`
	FailedStage        string              `json:"failedStage,omitempty"`
	SnapshotChanged    bool                `json:"snapshotChanged"`
	EventsEmitted      int                 `json:"eventsEmitted"`
	EventsDeduplicated int                 `json:"eventsDeduplicated"`
	Stages             []DemoPipelineStage `json:"stages"`
	Delivery           DemoDeliveryResult  `json:"delivery"`
	Error              string              `json:"error,omitempty"`
}
```

Fixed stage IDs are `demo_state`, `normalize`, `snapshot_persisted`, `event_engine`, and `apns`.

- [ ] **Step 4: Capture notifier availability and delivery counts**

Add an optional internal capability:

```go
type notifierStatus interface {
	Enabled() bool
}
```

`noopNotifier.Enabled()` returns false; the real APNs client returns true. Existing test notifiers that do not implement the interface are treated as enabled. Refactor dispatch to return per-channel attempted/succeeded/failed counts without changing notification payloads.

- [ ] **Step 5: Refactor to one internal poll pipeline**

Both `PollNow` and `PollDemoNow` must acquire the existing `Poller.mu` and call one internal method. The demo-recording path performs:

1. Load demo state.
2. Fetch real upstream data.
3. `InjectDemoProvider(raw, state)`.
4. `Normalize` once.
5. Save one combined snapshot.
6. Call `ProcessDetailed` once.
7. Dispatch only emitted events.
8. Persist demo outcomes and delivery results.
9. Retain the latest 20 runs and 500 feed events.

Do not duplicate normalization, persistence, event detection, or APNs code.

- [ ] **Step 6: Persist demo feed records**

```go
type DemoEventRecord struct {
	ID           int64              `json:"id"`
	RunID        *int64             `json:"runID,omitempty"`
	Key          string             `json:"key"`
	Type         string             `json:"type"`
	CreatedAt    time.Time          `json:"createdAt"`
	WindowID     string             `json:"windowID,omitempty"`
	Before       *EventValue        `json:"before,omitempty"`
	After        *EventValue        `json:"after,omitempty"`
	Deduplicated bool               `json:"deduplicated"`
	Delivery     DemoDeliveryResult `json:"delivery"`
}
```

Allowed feed types: `early_threshold`, `danger_threshold`, `reset`, `tibo_reset`, `credits_increase`, `manual_poll`, `test_alert`, and `pipeline_error`.

- [ ] **Step 7: Run focused, full, race, and vet checks**

```bash
cd server
go test . -run 'TestPoller.*Demo|TestDemoPoll|TestPollDelivery'
go test ./...
go test -race ./...
go vet ./...
```

Expected: PASS.

- [ ] **Step 8: Commit the task**

```bash
curl -s api.ipify.org
git add server/poller.go server/poller_test.go server/apns.go server/apns_test.go server/demo_store.go server/demo_store_test.go
git commit -m "Run demo state through the real poll pipeline"
```

---

### Task 4: Build the Approved Lab Console

**Files:**
- Create: `server/web/package.json`
- Create: `server/web/model.ts`
- Create: `server/web/model.test.ts`
- Create: `server/web/index.html`
- Create: `server/web/styles.css`
- Create: `server/web/app.ts`
- Create: `server/web/app.js`
- Reference only: `.superpowers/brainstorm/79315-1784346965/content/lab-console.html`

**Interfaces:**
- Consumes: the exact demo JSON contracts from Tasks 1–3.
- Produces: static `index.html`, `styles.css`, and `app.js` for Go embedding.
- Does not consume: real-provider, health, settings, devices, or deployment APIs.

- [ ] **Step 1: Add the dependency-free Bun package scripts**

```json
{
  "private": true,
  "scripts": {
    "test": "bun test",
    "build": "bun build app.ts --outfile app.js --target browser"
  }
}
```

Do not add dependencies or a lock file solely for this package.

- [ ] **Step 2: Write failing pure-model tests**

`server/web/model.test.ts` must test patch construction, surprise-reset eligibility, reset presets, event filtering, before/after formatting, delivery formatting, and stage-status labels.

```ts
import { describe, expect, test } from "bun:test";
import { canSurpriseReset, filterEvents, makePatch } from "./model";

describe("demo controls", () => {
  test("surprise reset requires an established primary baseline", () => {
    expect(canSurpriseReset(19)).toBe(false);
    expect(canSurpriseReset(20)).toBe(true);
  });

  test("builds a demo-only patch", () => {
    expect(makePatch({ primaryUsed: 81, credits: 3, stale: false, providerError: false })).toEqual({
      primary: { usedPercent: 81 },
      creditsAvailable: 3,
      stale: false,
      providerError: false,
    });
  });
});
```

- [ ] **Step 3: Run Bun tests and confirm the failure**

```bash
cd server/web
bun test
```

Expected: module-not-found or missing-export failure for `model.ts`.

- [ ] **Step 4: Implement exact TypeScript contracts and helpers**

Define interfaces that match the Go JSON exactly, including:

```ts
export interface DemoWindowState { usedPercent: number; resetsAt: string }
export interface DemoState {
  primary: DemoWindowState;
  secondary: DemoWindowState;
  creditsAvailable: number;
  stale: boolean;
  providerError: boolean;
  updatedAt: string;
}
export interface DemoStatePatch {
  primary?: Partial<DemoWindowState>;
  secondary?: Partial<DemoWindowState>;
  creditsAvailable?: number;
  stale?: boolean;
  providerError?: boolean;
}
export type StageStatus = "ok" | "warning" | "failed" | "skipped";
```

Also mirror `DemoPipelineResult`, `DemoDeliveryResult`, and `DemoEventRecord` from Task 3. Keep helper functions pure and DOM-free.

- [ ] **Step 5: Port the approved semantic markup and CSS**

Split the approved mockup into production `index.html` and `styles.css`. Preserve the three-column desktop layout, single-column responsive layout, typography, spacing, colors, controls, pipeline trace, snapshot pane, and event feed. Replace placeholder header KPIs with demo-only values:

- Pipeline: latest demo-run status.
- Last poll: latest demo-run completion time.
- Demo state: fresh, stale, or error.
- Recent events: count in the bounded feed.

Remove inline scripts and inline event handlers so the server can send a strict Content Security Policy.

- [ ] **Step 6: Implement same-origin interactions**

`app.ts` performs:

- Startup: `GET /v1/demo`, then `GET /v1/demo/events?limit=50`.
- Sliders: update labels locally without changing the snapshot pane.
- Apply + Poll: `PATCH /v1/demo`, then `POST /v1/demo/poll`, then reload state/events.
- Surprise Reset: require current normalized primary baseline `>= 20`, patch primary usage to `5` while preserving reset time, poll, then reload.
- Test Alert: `POST /v1/demo/alert`, then reload events.
- Reset presets: five minutes, 30 minutes, two hours eight minutes, and one minute ago.
- Event filters: thresholds, resets, delivery.
- Failure: retain last successful snapshot, display a stale/error banner, and expose Retry.
- Busy state: disable actions and set `aria-busy`.
- Announcements: use `aria-live="polite"`.

Never request or store a bearer token.

- [ ] **Step 7: Run tests and build the committed bundle**

```bash
cd server/web
bun test
bun run build
```

Expected: tests PASS and `app.js` is generated without runtime dependencies.

- [ ] **Step 8: Compare the production page with the approved mockup**

Open both files locally and verify the production version preserves the selected direction. Do not introduce CRT, NOC, terminal, draggable-pane, production-monitoring, or extra navigation concepts.

- [ ] **Step 9: Commit the task**

```bash
curl -s api.ipify.org
git add server/web/package.json server/web/model.ts server/web/model.test.ts server/web/index.html server/web/styles.css server/web/app.ts server/web/app.js
git commit -m "Build the demo lab console"
```

---

### Task 5: Expose the Narrow Embedded Demo API

**Files:**
- Create: `server/demo_api.go`
- Create: `server/demo_api_test.go`
- Modify: `server/api.go:20-210`
- Modify: `server/api_test.go`

**Interfaces:**
- Consumes: `Store`, `DemoPoller`, notifier/test-alert helper, embedded frontend files.
- Produces: `DemoAPI.Handler()` with an exact route allowlist.
- Preserves: current `API.Handler()` and bearer middleware on `:8377`.

- [ ] **Step 1: Write failing route, validation, and asset tests**

Tests must prove:

- `/`, `/styles.css`, and `/app.js` serve correct content types.
- Only the specified demo API routes exist.
- `/v1/health`, `/v1/snapshot`, `/v1/settings`, `/v1/devices`, and `/v1/poll` return 404 on the demo handler.
- Demo routes do not require the existing bearer token.
- PATCH rejects unknown fields, invalid values, trailing JSON, and bodies over 16 KiB.
- Event type and limit validation returns 400.
- Poll timeout returns redacted JSON.
- Errors never contain database paths, APNs tokens, or upstream URLs.
- Demo test alerts append a feed row without adding a dedup key.

```go
func TestDemoRouteAllowlist(t *testing.T) {
	h := newTestDemoAPI(t).Handler()
	for _, path := range []string{"/v1/health", "/v1/snapshot", "/v1/settings", "/v1/devices", "/v1/poll"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != http.StatusNotFound {
			t.Fatalf("%s: got %d", path, res.Code)
		}
	}
}
```

- [ ] **Step 2: Run tests and confirm the failure**

```bash
cd server
go test . -run 'TestDemoAPI|TestDemoAssets|TestDemoRouteAllowlist'
```

Expected: compile failure because `DemoAPI` does not exist.

- [ ] **Step 3: Embed the approved assets**

```go
//go:embed web/index.html web/styles.css web/app.js
var demoAssets embed.FS
```

Serve exact paths with fixed content types. API responses use `Cache-Control: no-store`.

- [ ] **Step 4: Register only the narrow routes**

```go
func (d *DemoAPI) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", d.handleIndex)
	mux.HandleFunc("GET /styles.css", d.handleStyles)
	mux.HandleFunc("GET /app.js", d.handleScript)
	mux.HandleFunc("GET /v1/demo", d.handleGetDemo)
	mux.HandleFunc("PATCH /v1/demo", d.handlePatchDemo)
	mux.HandleFunc("POST /v1/demo/poll", d.handleDemoPoll)
	mux.HandleFunc("GET /v1/demo/events", d.handleDemoEvents)
	mux.HandleFunc("POST /v1/demo/alert", d.handleDemoAlert)
	return d.withSecurityHeaders(mux)
}
```

Do not wrap this mux in the existing bearer middleware and do not add CORS headers.

- [ ] **Step 5: Implement response contracts**

`GET /v1/demo` returns:

```json
{"state":{},"snapshot":{"fetchedAt":"...","provider":{}},"pipeline":null}
```

Return only the normalized demo provider and set its `Raw` field to nil. `PATCH /v1/demo` returns the complete persisted state. `POST /v1/demo/poll` returns `{"pipeline":{},"events":[]}`. `GET /v1/demo/events` returns `{"events":[]}`. Reuse the existing test-alert operation for `POST /v1/demo/alert`.

- [ ] **Step 6: Add limits, timeouts, and security headers**

Use `http.MaxBytesReader` with 16 KiB for PATCH, `json.Decoder.DisallowUnknownFields`, a second-decode EOF check, event default limit 50/max 100, and a 90-second poll context.

Set:

```text
Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'
X-Content-Type-Options: nosniff
Referrer-Policy: no-referrer
```

- [ ] **Step 7: Run focused and full tests**

```bash
cd server
go test . -run 'TestDemoAPI|TestDemoAssets|TestDemoRouteAllowlist'
go test ./...
```

Expected: PASS.

- [ ] **Step 8: Commit the task**

```bash
curl -s api.ipify.org
git add server/demo_api.go server/demo_api_test.go server/api.go server/api_test.go
git commit -m "Expose the narrow demo console API"
```

---

### Task 6: Start the Dedicated Loopback Listener

**Files:**
- Modify: `server/config.go:8-95`
- Modify: `server/config_test.go`
- Modify: `server/cmd/usagewidgetd/main.go:15-120`

**Interfaces:**
- Consumes: `DemoAPI.Handler()` and existing API/poller/store setup.
- Produces: `Config.DemoListenAddr` and two coordinated HTTP servers.
- Preserves: current `Config.ListenAddr` default and `:8377` behavior.

- [ ] **Step 1: Write failing configuration tests**

Test that default `DemoListenAddr` is `127.0.0.1:8378`, an explicit loopback override is accepted, and `:8378`, `0.0.0.0:8378`, public IPs, and malformed addresses are rejected.

```go
func TestLoadConfigRejectsPublicDemoListener(t *testing.T) {
	t.Setenv("API_TOKEN", "test")
	t.Setenv("DEMO_LISTEN_ADDR", "0.0.0.0:8378")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected public demo listener to be rejected")
	}
}
```

- [ ] **Step 2: Run tests and confirm the failure**

```bash
cd server
go test . -run 'TestLoadConfig|TestDemoListen'
```

Expected: missing `DemoListenAddr` or invalid public binding accepted.

- [ ] **Step 3: Add loopback-only configuration**

Extend `Config` with `DemoListenAddr string`, load `DEMO_LISTEN_ADDR` with default `127.0.0.1:8378`, parse with `net.SplitHostPort`, and require `net.ParseIP(host).IsLoopback()`. Production documentation uses IPv4 loopback exactly.

- [ ] **Step 4: Construct the dedicated server with fixed limits**

```go
demoServer := &http.Server{
	Addr:              cfg.DemoListenAddr,
	Handler:           demoAPI.Handler(),
	ReadHeaderTimeout: 5 * time.Second,
	ReadTimeout:       15 * time.Second,
	WriteTimeout:      95 * time.Second,
	IdleTimeout:       60 * time.Second,
	MaxHeaderBytes:    16 << 10,
}
```

Do not alter existing full-API timeouts unless required to preserve its current values.

- [ ] **Step 5: Implement dual-listener lifecycle**

In `main`:

1. Construct both `net.Listener`s before starting the poller.
2. If either bind fails, close the other and exit before background work starts.
3. Start `poller.Run(ctx)`.
4. Serve both HTTP servers in goroutines using a buffered error channel.
5. Cancel shared context on a signal or unexpected failure from either server.
6. Ignore `http.ErrServerClosed`.
7. Shut down both servers concurrently under one ten-second context.
8. Wait for poller exit, then close SQLite.
9. Do not call `log.Fatalf` from goroutines.

- [ ] **Step 6: Run server verification**

```bash
cd server
go test . -run 'TestLoadConfig|TestDemoListen'
go test ./...
go test -race ./...
go vet ./...
```

Expected: PASS.

- [ ] **Step 7: Commit the task**

```bash
curl -s api.ipify.org
git add server/config.go server/config_test.go server/cmd/usagewidgetd/main.go
git commit -m "Serve the demo console on a loopback listener"
```

---

### Task 7: Decode and Render Provider-Level Staleness on iOS

**Files:**
- Modify: `ios/Sources/Core/Models.swift:3-100`
- Modify: `ios/Sources/App/DashboardView.swift:86-210`
- Modify: `ios/Sources/Widget/ProviderWidget.swift:162-280`
- Modify: `ios/Tests/CoreTests/ModelsAndStoreTests.swift`

**Interfaces:**
- Consumes: normalized provider JSON field `stale` from Task 1.
- Produces: backward-compatible `Provider.stale` behavior in app and widget.
- Preserves: `Snapshot.stale` whole-upstream behavior and all existing provider rendering.

- [ ] **Step 1: Write failing decoding tests**

Add one fixture with `"stale": true` and one without the field. Assert provider stale is true for the first and false for the legacy payload while snapshot stale remains independent.

```swift
func testProviderStaleDefaultsToFalse() throws {
    let data = Data(#"{"fetchedAt":"2026-07-18T12:00:00Z","stale":false,"providers":[{"id":"demo","name":"Demo","windows":[]}],"pollIntervalMinutes":5}"#.utf8)
    let snapshot = try JSONDecoder.usageWidget.decode(Snapshot.self, from: data)
    XCTAssertFalse(snapshot.providers[0].stale)
}
```

- [ ] **Step 2: Run the iOS test and confirm the failure**

```bash
cd ios
xcodebuild -project UsageWidget.xcodeproj -scheme UsageWidget -destination 'platform=iOS Simulator,name=iPhone 17 Pro' test
```

Expected: compile failure because `Provider.stale` does not exist, or decoding assertion failure.

- [ ] **Step 3: Add backward-compatible provider decoding**

Add `stale: Bool` and a custom `init(from:)` that uses `decodeIfPresent(Bool.self, forKey: .stale) ?? false` while decoding all existing fields unchanged. Do not make old server payloads fail.

- [ ] **Step 4: Add minimal provider-level stale treatment**

In app and widget provider rows, display the existing stale warning semantics only on the affected demo provider. Do not promote it to the global snapshot stale banner and do not desaturate real providers.

- [ ] **Step 5: Run tests and unsigned build**

```bash
cd ios
xcodebuild -project UsageWidget.xcodeproj -scheme UsageWidget -destination 'platform=iOS Simulator,name=iPhone 17 Pro' test
xcodebuild -project UsageWidget.xcodeproj -scheme UsageWidget -destination 'generic/platform=iOS' CODE_SIGNING_ALLOWED=NO build
```

Expected: PASS and BUILD SUCCEEDED.

- [ ] **Step 6: Commit the task**

```bash
curl -s api.ipify.org
git add ios/Sources/Core/Models.swift ios/Sources/App/DashboardView.swift ios/Sources/Widget/ProviderWidget.swift ios/Tests/CoreTests/ModelsAndStoreTests.swift
git commit -m "Show provider-level demo staleness on iOS"
```

---

### Task 8: Deploy, Document, and Verify the Full Flow

**Files:**
- Modify: `server/deploy/redeploy.sh`
- Modify: `server/deploy/README.md`
- Modify: `README.md`
- Modify: `HUMANS.md`

**Interfaces:**
- Consumes: all prior tasks.
- Produces: reproducible build/deploy checks, Cloudflare human steps, rollback, and release evidence.

- [ ] **Step 1: Update the deploy script build sequence**

Before Go build, run:

```bash
cd "$REPO_DIR/server/web"
bun test
bun run build

cd "$REPO_DIR/server"
go test ./...
```

Then cross-compile/install/restart as today. After restart, retain the existing `:8377` bearer smoke check and add non-mutating checks:

```bash
curl --fail --silent http://127.0.0.1:8378/ >/dev/null
curl --fail --silent http://127.0.0.1:8378/v1/demo >/dev/null
test "$(curl --silent --output /dev/null --write-out '%{http_code}' http://127.0.0.1:8378/v1/health)" = "404"
ss -ltn | grep -F '127.0.0.1:8378'
```

Do not send alerts or mutate demo state during deployment.

- [ ] **Step 2: Document Cloudflare Access before Tunnel publication**

Add these human-only steps to `server/deploy/README.md` and `HUMANS.md`:

1. Create a Cloudflare Access self-hosted application for `demo.usagewidget.edmundlim.systems`.
2. Add a narrow Allow policy for the operator’s exact identity/group; do not use Everyone.
3. Configure the intended IdP, session duration, and MFA policy.
4. Create/select the edServe Tunnel.
5. Publish `demo.usagewidget.edmundlim.systems` to `http://127.0.0.1:8378`.
6. Enable Protect with Access before exposing the route.
7. Install/start `cloudflared` as a service if edServe lacks a connector.
8. Verify unauthenticated login, unauthorized denial, authorized access, healthy connector, and same-origin requests without an edServe token.

- [ ] **Step 3: Document rollback**

Rollback order:

1. Disable/remove the Tunnel published route.
2. Confirm the public hostname no longer reaches `127.0.0.1:8378`.
3. Remove Access/DNS only if no longer needed.
4. Remove `cloudflared` only if it is a dedicated connector.
5. Redeploy the previous `usagewidgetd` binary.
6. Leave additive demo tables intact.
7. Never delete `events` or `window_state`.
8. Verify the existing `:8377` bearer API, Tailscale URL, real snapshots, and APNs.

- [ ] **Step 4: Run the complete automated gate**

```bash
cd server/web
bun test
bun run build

cd ..
go test ./...
go test -race ./...
go vet ./...
GOOS=linux GOARCH=amd64 go build -o /tmp/usagewidgetd ./cmd/usagewidgetd
bash -n deploy/redeploy.sh
bash -n ../cli/usagewidget

cd ../ios
xcodebuild -project UsageWidget.xcodeproj -scheme UsageWidget -destination 'generic/platform=iOS' CODE_SIGNING_ALLOWED=NO build
```

Expected: all commands succeed.

- [ ] **Step 5: Commit deployment and documentation**

```bash
curl -s api.ipify.org
git add server/deploy/redeploy.sh server/deploy/README.md README.md HUMANS.md server/web/app.js
git commit -m "Document and deploy the demo dashboard"
```

- [ ] **Step 6: Deploy to edServe**

Run the repository’s existing deploy workflow after the commit. Verify locally on edServe:

```bash
curl --fail http://127.0.0.1:8378/
curl --fail http://127.0.0.1:8378/v1/demo
curl --silent --output /dev/null --write-out '%{http_code}\n' http://127.0.0.1:8378/v1/health
```

Expected: 200, 200, and 404.

- [ ] **Step 7: Complete the signed physical-device release gate**

With sandbox APNs, a signed app, and an installed widget:

1. Confirm the existing Tailscale/bearer app connection still loads real providers.
2. Open the Access-protected dashboard; confirm no bearer token prompt or browser storage.
3. Establish a demo baseline before each crossing test.
4. Cross the early threshold once and confirm one alert, one feed outcome, app update, and widget refresh.
5. Cross the danger threshold once and confirm one alert and delivery counts.
6. Increase credits from one to two and confirm one `credits_increase` event.
7. Poll primary at 80 with a future reset, then Surprise Reset to five with the same reset time; confirm one `tibo_reset`.
8. Exercise a normal reset-cycle rollover and confirm one `reset`.
9. Toggle demo stale; confirm only Demo is stale and no stale-derived event fires.
10. Toggle provider error; confirm Demo has no windows while real providers remain unchanged.
11. Trigger an event, restart `usagewidget`, recreate the same candidate key, and confirm dedup prevents a second alert.
12. Send Test Alert and confirm notification, widget refresh count, and feed row.
13. Repeat unchanged polls and confirm no duplicate alerts.
14. Verify one-column layout, keyboard operation, reduced motion, Retry, and last-good snapshot retention.
15. Compare real-provider values and baselines before and after; only legitimate concurrent CodexBar updates may differ.

- [ ] **Step 8: Push the completed release**

```bash
curl -s api.ipify.org
# Apply the required exit-node routing if needed.
git push origin master
```

Expected: push succeeds after all automated and physical-device gates pass.
