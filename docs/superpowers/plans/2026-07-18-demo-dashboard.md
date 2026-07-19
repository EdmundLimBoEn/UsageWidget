# UsageWidget Demo Dashboard Implementation Plan

> **Status:** Working implementation record, not an operator runbook. Checkbox
> and task state below describe the staged plan at the time it was written and
> may not match the current checkout. Use `README.md`, `SECURITY.md`, and
> `server/deploy/README.md` for current setup, trust boundaries, routes, and
> teardown guidance.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **RECONCILED (2026-07-18) — Tasks 5+ rewritten against the CEO plan.** Tasks 1–4 are complete and committed (Task 1 `8e46b0a..1c8d248`, Task 2 `2b0d410`, Task 3 `f415abb..5d2f482`, Task 4 `5193cc0`, with Bun test/build and `go test ./...` passing). The prior "BLOCKED AFTER TASK 4" notice is lifted: Tasks 5 through 12 below are now reconciled with `~/.gstack/projects/EdmundLimBoEn-UsageWidget/ceo-plans/2026-07-18-usagewidget-portfolio.md` — temporary Access/Tunnel trust boundary, stateless CSRF + same-origin guards, default-off loopback listener, per-Access-identity and global rate limits, mutation audit, explicit demo delivery targets, client/server idempotency, `expectedRevision` conflict, persistent leased per-device APNs outbox, snapshot-freshness vs delivery-health split, iOS foreground banner, candidate-tag evidence gates, and post-hackathon removal. The reconciled order is: finish the console API and demo action records (Task 5) → default-off listener (Task 6) → **freeze the dashboard contract (Task 7)** → outbox reliability migration for every delivery producer (Task 8) → freshness/delivery-health split (Task 9) → iOS decode/render + foreground banner (Task 10) → evidence gates/deploy/docs (Task 11) → post-hackathon removal (Task 12). Task 7 is a hard gate: do not begin Task 8 until the contract is frozen.
>
> **Human verification before Task 5 implementation:** the Cloudflare Access identity header that carries the operator email must be confirmed against the live tenant and current Cloudflare docs before coding. This plan uses `Cf-Access-Authenticated-User-Email` as the default identity source for audit and rate-limit keys, with the signed `Cf-Access-Jwt-Assertion` JWT as the verified alternative. Because the demo path is Tunnel-to-loopback (an accepted trust boundary in the CEO plan), the origin trusts this header without independently validating the JWT. This human action is tracked in `HUMANS.md`.

**Goal:** Ship the approved Lab Console at `demo.example.com`, controlling one synthetic demo provider through UsageWidget’s real normalization, persistence, event, APNs, iOS app, and widget pipeline.

**Architecture:** Cloudflare Access protects a Cloudflare Tunnel that forwards only to a dedicated `127.0.0.1:8378` listener on the server. That listener serves the static console and a demo-only API. Demo state is persisted in SQLite and injected into the raw upstream payload before the existing normalizer; the existing `:8377` bearer-authenticated Tailscale API remains unchanged.

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
- Before network-sensitive operations, follow your organization's approved private-network policy; do not commit home or office public IP addresses.
- Frontend implementation/review should use a Fable agent when Claude workers are available; while unavailable, use Codex workers with this plan and the approved mockup as context.

---

## File Structure

Created and committed in Tasks 1–4 (do not recreate; modify only as later tasks specify):

- `server/demo.go`, `server/demo_test.go` — demo state types, defaults, patch validation, raw provider construction, upstream injection.
- `server/demo_store.go`, `server/demo_store_test.go` — additive demo SQLite schema plus state, run, and event persistence.
- `server/web/index.html`, `server/web/styles.css` — semantic production markup and approved responsive visual direction.
- `server/web/model.ts`, `server/web/model.test.ts` — JSON contracts and pure UI helpers with Bun tests.
- `server/web/app.ts` — DOM binding and same-origin API client.
- `server/web/app.js` — committed Bun-generated browser bundle embedded in the Go binary.
- `server/web/package.json` — Bun test/build scripts with no dependencies.

Create (Tasks 5–12):

- `server/demo_api.go` — embedded assets, narrow demo mux, API handlers, limits, security headers, action records, and response shaping. (Tasks 5, 8, 9)
- `server/demo_api_test.go` — route allowlist, static assets, validation, CSRF/origin, identity, rate-limit, idempotency, revision, audit, outbox enqueue, delivery-health, and redaction tests. (Tasks 5, 8, 9)
- `server/demo_guard.go` — stateless HMAC CSRF token, same-origin `Origin`/`Sec-Fetch-Site` guard, in-process per-identity + global rate limiter, and in-process idempotency store. (Task 5)
- `server/demo_guard_test.go` — CSRF issue/expiry/signature, origin/fetch-site, rate-limit, and idempotency retention/eviction tests. (Task 5)
- `server/outbox.go` — persistent per-device APNs outbox worker: atomic lease claim, attempt, classify, lease-required finalization, backoff, and retention. (Task 8)
- `server/outbox_test.go` — retry/backoff, expired-lease recovery, per-device independence, invalid-token permanent failure, collapse ID, crash-point, retention, and legacy-binary rollback tests. (Task 8)

Modify:

- `server/config.go` and `server/config_test.go` — `DemoDeviceIDs` allowlist and `AccessIdentityHeader` parsing (Task 5); `DemoEnabled` default-off and `DemoListenAddr` loopback validation (Task 6). (Tasks 5, 6)
- `server/demo.go` and `server/demo_test.go` — add `DemoState.Revision`, monotonic increment in `ApplyDemoPatch`. (Task 5)
- `server/demo_store.go` and `server/demo_store_test.go` — additive `demo_audit` table, `SaveDemoAudit`/`ListDemoAudit`, action/run IDs, revision persistence and conflict, 7-day/1000-row audit and event retention. (Task 5)
- `server/api.go` and `server/api_test.go` — demo-device target selection on the existing `:8377` test-alert route, then outbox-only test-alert enqueueing; add `deliveryHealth` to health and snapshot; DB-unreadable 503 on snapshot. (Tasks 5, 8, 9)
- `server/poller.go` and `server/poller_test.go` — `PollDemoNow(ctx, expectedRevision, demoRunID, targets)` conflict; explicit demo targets through all poll/surprise-reset deliveries; outbox-only enqueue; stale preservation and delivery health without altering freshness. (Tasks 5, 8, 9)
- `server/store.go` and `server/store_test.go` — additive outbox schema from `OpenStore`; atomic event + pending-delivery write; freshness state helpers; rollback compatibility. (Tasks 8, 9)
- `server/events.go` and `server/events_test.go` — produce durable per-device delivery work instead of synchronous dispatch. (Task 8)
- `server/apns.go` and `server/apns_test.go` — typed `APNsResult` classification (accepted / transient / permanent / invalid-token) with the token suffix; no device retirement. (Task 8)
- `server/cmd/usagewidgetd/main.go` — start the outbox worker; construct the demo listener only when `DemoEnabled`; dual-listener lifecycle, failure propagation, graceful shutdown. (Tasks 6, 8)
- `server/deploy/redeploy.sh` and `server/deploy/README.md` — default-off deploy, Bun build, listener smoke checks, Access-before-Tunnel instructions, rollback. (Task 11)
- `README.md` and `HUMANS.md` — architecture, demo API, delivery-health, trust boundaries, human Cloudflare/device/Access-header verification steps, and post-hackathon removal checklist. (Tasks 5, 11, 12)
- `ios/Sources/Core/Models.swift` and `ios/Tests/CoreTests/ModelsAndStoreTests.swift` — provider-level `stale` backward-compatible decode; snapshot `deliveryHealth` decode. (Task 10)
- `ios/Sources/Core/SnapshotStore.swift` — cached-age surfacing for offline/stale display. (Task 10)
- `ios/Sources/App/DashboardView.swift` and `ios/Sources/Widget/ProviderWidget.swift` — provider-level stale indicator and delivery-health surface. (Task 10)
- `ios/Sources/App/UsageWidgetApp.swift` — `UNUserNotificationCenterDelegate.userNotificationCenter(_:willPresent:)` foreground banner/sound. (Task 10)

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
# If required: tailscale set --exit-node=<approved-node>
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

### Task 5: Add DemoState Revision and Secure the Embedded Demo API

**Files:**
- Create: `server/demo_api.go`
- Create: `server/demo_api_test.go`
- Create: `server/demo_guard.go`
- Create: `server/demo_guard_test.go`
- Modify: `server/demo.go` (add `Revision`)
- Modify: `server/demo_test.go`
- Modify: `server/demo_store.go` (audit table + revision persistence)
- Modify: `server/demo_store_test.go`
- Modify: `server/poller.go` (`PollDemoNow` expected-revision conflict)
- Modify: `server/poller_test.go`
- Modify: `server/api.go` (demo-device allowlist on the existing `:8377` test-alert)
- Modify: `server/api_test.go`
- Modify: `server/config.go` (`DemoDeviceIDs`, `AccessIdentityHeader`)
- Modify: `server/config_test.go`
- Modify: `server/web/model.ts`, `server/web/model.test.ts`, `server/web/app.ts`, `server/web/app.js`

**Interfaces:**
- Produces: `DemoState.Revision int64`, `ErrDemoRevisionConflict`; `DemoAction{ID,Identity,Route,CreatedAt}`, `DemoAPI`, `DemoAPI.Handler()`; `DemoPoller` interface; guard helpers `issueCSRFToken`, `verifyCSRFToken`, `sameOriginOK`, `rateLimiter`, atomic `idempotencyStore`; `Store.SaveDemoAudit`, `Store.ListDemoAudit`, and `Store.DemoTargets`.
- Consumes: `Store`, `Poller.PollDemoNow`, the existing `demoEvent()` test-alert helper in `server/api.go`, embedded `server/web` assets, `Config.DemoDeviceIDs`, `Config.AccessIdentityHeader`.
- Preserves: `API.Handler()` and bearer middleware on `:8377`; the `DemoState`/`DemoStatePatch`/`DemoPipelineResult`/`DemoEventRecord` JSON already frozen by Tasks 1–4 (revision is additive).

**Frozen request/response contract (authoritative for Task 7):**
- `GET /v1/demo` -> `{"state":DemoState,"snapshot":{"fetchedAt":"RFC3339 timestamp","provider":Provider},"pipeline":DemoPipelineResult|null,"csrfToken":"<token>","deliveryHealth":"ok"|"degraded"}`. `provider.raw` is nil; `deliveryHealth` is computed at response time.
- `PATCH /v1/demo` -> `{"state":DemoState,"demoRunID":"opaque action ID","deliveryHealth":"ok"|"degraded"}`; `POST /v1/demo/poll` -> `{"pipeline":DemoPipelineResult,"events":[DemoEventRecord],"demoRunID":"opaque action ID","deliveryHealth":"ok"|"degraded"}`; `POST /v1/demo/alert` -> `{"delivery":DemoDeliveryResult,"demoRunID":"opaque action ID","deliveryHealth":"ok"|"degraded"}`.
- Every mutation response includes a server-generated `demoRunID`; the same ID is persisted on the state, audit, run, and event rows for that action, including an alert action that produces no state-value change. It is never inserted into an APNs payload.
- Mutations (`PATCH /v1/demo`, `POST /v1/demo/poll`, `POST /v1/demo/alert`) require all of: `X-Demo-CSRF: <token>`; `Content-Type: application/json`; same-origin `Origin` (host equals request host) or absent-with `Sec-Fetch-Site: same-origin`; `Sec-Fetch-Site` in `{same-origin,none}`; `Idempotency-Key` (opaque, <=128 chars).
- `POST /v1/demo/poll` body `{"expectedRevision":<int64>}`. On mismatch -> `409 {"error":"revision conflict","currentRevision":<n>}`.
- Rate limit exceeded -> `429 {"error":"rate limited","retryAfterSeconds":<n>}` plus a `Retry-After` header.
- Idempotent replay -> stored status + body plus header `Idempotency-Replayed: true`.
- CSRF token format: `base64url(<expUnix>.<nonceHex>.<hmacSHA256>)`, 15-minute TTL, HMAC over `<expUnix>.<nonceHex>`.

- [ ] **Step 1: Write failing guard, revision, audit, and API tests**

`server/demo_guard_test.go`:

```go
func TestCSRFTokenRoundTrip(t *testing.T) {
	key := []byte("test-csrf-key-0123456789abcdef")
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	tok := issueCSRFToken(key, now)
	if err := verifyCSRFToken(key, tok, now.Add(14*time.Minute)); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
}

func TestCSRFTokenExpired(t *testing.T) {
	key := []byte("test-csrf-key-0123456789abcdef")
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	tok := issueCSRFToken(key, now)
	if err := verifyCSRFToken(key, tok, now.Add(16*time.Minute)); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestCSRFTokenTampered(t *testing.T) {
	key := []byte("test-csrf-key-0123456789abcdef")
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	tok := issueCSRFToken(key, now) + "x"
	if err := verifyCSRFToken(key, tok, now); err == nil {
		t.Fatal("expected tampered token to be rejected")
	}
}
```

Also add `TestSameOriginOK` (a truth table over `Origin`, `Host`, and `Sec-Fetch-Site`), `TestRateLimiterPerIdentityAndGlobal` (31st per-identity request in a window is denied; global cap denies across identities), and `TestIdempotencyStoreReplayAndEvict` (replay within TTL returns the stored entry; entries past 15 minutes or beyond 500 keys are evicted). Add `TestIdempotencyStoreConcurrentDuplicate`: two goroutines reserve the same `(identity, route, key)`; exactly one executes, while the other waits and replays its status/body (or receives the documented `409 {"error":"request in progress"}`), never a second side effect.

`server/demo_test.go`: `TestApplyDemoPatchIncrementsRevision` — each successful `ApplyDemoPatch` returns `Revision` exactly one greater than the input.

`server/demo_store_test.go`: `TestSaveDemoStatePersistsRevision`, `TestDemoActionIDPropagatesToStateAuditRunAndEvent`, `TestDemoAuditRetention`, and `TestDemoEventRetention`. Each retention test proves the strict boundary: delete entries older than 7 days **or** below the newest 1000; retain the 7-day boundary and the newest 1000.

`server/poller_test.go`: `TestPollDemoNowRevisionConflict` — a stale `expectedRevision` returns `ErrDemoRevisionConflict` and performs no poll or dispatch.

`server/demo_api_test.go`: `TestDemoRouteAllowlist` asserts that only `/`, `/styles.css`, `/app.js`, `GET /v1/demo`, `PATCH /v1/demo`, `POST /v1/demo/poll`, `GET /v1/demo/events`, and `POST /v1/demo/alert` are reachable and every other path is 404. Also add `TestDemoMutationRequiresCSRF`, `TestDemoMutationRejectsCrossOrigin`, `TestDemoMutationRejectsMissingOrBlankIdentity403`, `TestDemoPollRevisionConflict`, `TestDemoIdempotentReplay`, `TestDemoIdempotentConcurrentDuplicate`, `TestDemoMutationRateLimited`, `TestDemoMutationReturnsAndPersistsDemoRunID`, `TestDemoPatchRejectsOversizeBody`, and `TestDemoErrorsRedacted`. The redaction test injects a wrapped error containing a URL and absolute path and asserts every public response uses only its fixed stage-safe message and contains neither value.

`server/api_test.go`: `TestBearerDemoAlertAllowlistsDemoDevices` — the existing `:8377` `POST /v1/demo/alert` selects only devices in `Config.DemoDeviceIDs`; Task 8 changes this test to assert durable enqueue, not a notifier call.

`server/config_test.go`: `TestLoadConfigDemoDeviceIDsAndAccessIdentityHeader` — parse, trim, deduplicate, and reject an empty `DEMO_DEVICE_IDS` entry; default `AccessIdentityHeader` to `Cf-Access-Authenticated-User-Email` and reject a blank configured header.

- [ ] **Step 2: Run tests and confirm the failure**

```bash
cd server
go test . -run 'TestCSRF|TestSameOrigin|TestRateLimiter|TestIdempotency|TestApplyDemoPatchIncrementsRevision|TestSaveDemoStatePersistsRevision|TestDemo(ActionID|AuditRetention|EventRetention)|TestPollDemoNowRevisionConflict|TestDemoRouteAllowlist|TestDemoMutation|TestDemoPoll|TestDemoIdempotent|TestDemoPatchRejects|TestDemoErrorsRedacted|TestBearerDemoAlert|TestLoadConfigDemoDeviceIDsAndAccessIdentityHeader'
```

Expected: compile failure because `DemoState.Revision`, `issueCSRFToken`, `rateLimiter`, `idempotencyStore`, `Store.SaveDemoAudit`, `ErrDemoRevisionConflict`, and `DemoAPI` do not exist.

- [ ] **Step 3: Add demo target and Access identity configuration**

Extend `Config` and `LoadConfig` now (before `DemoAPI` is compiled) with:

```go
DemoDeviceIDs        []string // DEMO_DEVICE_IDS, comma-separated, trimmed/deduplicated
AccessIdentityHeader string   // ACCESS_IDENTITY_HEADER, default Cf-Access-Authenticated-User-Email
```

Reject blank `ACCESS_IDENTITY_HEADER` and empty device IDs; preserve an empty allowlist as a valid safe configuration that targets no devices. Add `func (s *Store) DemoTargets(allowed []string) ([]Device, error)` which intersects registered alert and widget device records with the explicit allowlist. Every demo mutation obtains this concrete target slice before it starts its action; no demo call may fall back to `Store.ListDevices()`.

- [ ] **Step 4: Add the monotonic revision to demo state**

In `server/demo.go` add `Revision int64` with JSON key `revision` and `LastDemoRunID string` with JSON key `lastDemoRunID,omitempty` to `DemoState`. In `ApplyDemoPatch`, after validation set `next.Revision = state.Revision + 1`. `DefaultDemoState` starts at `Revision: 1`; `SaveDemoState` receives the action ID introduced in Step 6 and persists it with the complete JSON.

- [ ] **Step 5: Implement the stateless guard primitives**

Create `server/demo_guard.go` using only `crypto/hmac`, `crypto/sha256`, `crypto/rand`, `encoding/base64`, `net`, `net/http`, `sync`, and `time`.

```go
// issueCSRFToken returns base64url(exp "." nonce "." hmac) valid for 15 minutes.
func issueCSRFToken(key []byte, now time.Time) string
func verifyCSRFToken(key []byte, token string, now time.Time) error // constant-time compare

// sameOriginOK enforces Origin/host match (or Sec-Fetch-Site same-origin/none).
func sameOriginOK(r *http.Request) bool

// ponytail: in-process fixed-window limiter, single instance only. If the demo
// ever runs multiple replicas, move counters to SQLite or a shared store.
type rateLimiter struct {
	mu       sync.Mutex
	window   time.Duration // 60s
	perID    int           // 30
	global   int           // 120
	buckets  map[string]int
	globalN  int
	resetAt  time.Time
}
func (l *rateLimiter) allow(identity string, now time.Time) (bool, time.Duration)

// ponytail: in-process idempotency cache, 15-min TTL / 500-key cap, single
// instance only. Restart or replica fan-out loses stored results and permits
// one duplicate side effect per lost key; acceptable for the demo surface.
type idempotencyKey struct { Identity, Route, Key string }
type idempotencyStore struct {
	mu      sync.Mutex
	ttl     time.Duration // 15m
	cap     int           // 500
	entries map[idempotencyKey]idempotencyEntry // state is reserved or complete
}
func (s *idempotencyStore) reserve(key idempotencyKey, now time.Time) (entry idempotencyEntry, owner bool, done <-chan struct{})
func (s *idempotencyStore) complete(key idempotencyKey, status int, body []byte, now time.Time)
```

The CSRF key is generated once at process start with `crypto/rand`. Add a `// ponytail:` note that the key is process-lifetime only; a server restart invalidates outstanding tokens, which is acceptable for a single-operator demo. The upgrade path is an env-provided key if cross-restart token stability is ever required.

- [ ] **Step 6: Add the additive audit table and store methods**

In `server/demo_store.go` extend `demoSchema` with an additive table (executed after the existing demo schema in `OpenStore`):

```sql
CREATE TABLE IF NOT EXISTS demo_audit (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    demo_run_id TEXT NOT NULL,
    identity TEXT NOT NULL,
    route TEXT NOT NULL,
    action TEXT NOT NULL,
    result TEXT NOT NULL,
    status INTEGER NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_demo_audit_recent ON demo_audit(id DESC);
```

Add `demo_run_id TEXT NOT NULL` to `demo_runs` and `demo_event_log`; for already-created Task 1 tables, apply additive `ALTER TABLE ... ADD COLUMN` migrations guarded by a column-exists check. Implement `NewDemoAction(identity, route string, now time.Time) DemoAction`, `SaveDemoAudit(entry DemoAuditEntry) error`, and `ListDemoAudit(limit int) ([]DemoAuditEntry, error)`. Persist the action ID to the state update, run, audit, and event record made by that mutation. Replace Task 1's `demo_event_log` latest-500 cleanup with pruning on each write: delete rows older than 7 days **or** older than the newest 1000; apply the same 7-day/newest-1000 rule to `demo_audit`. Never store request/response bodies, tokens, or the CSRF value.

Use these concrete persisted types and columns (all IDs are server-generated opaque UUIDs):

```go
type DemoAction struct { ID, Identity, Route string; CreatedAt time.Time }
type DemoAuditEntry struct { DemoRunID, Identity, Route, Action, Result string; Status int; CreatedAt time.Time }
```

Add `last_demo_run_id TEXT NOT NULL DEFAULT ''` to `demo_state` and `demo_run_id TEXT NOT NULL DEFAULT ''` to `demo_runs`/`demo_event_log`. `SaveDemoState(state, demoRunID)` writes the same ID atomically to `demo_state.last_demo_run_id` and the serialized state (`DemoState.LastDemoRunID`, `json:"lastDemoRunID,omitempty"`). Each mutation calls `SaveDemoState` to record its action ID, then calls `SaveDemoRun` and `AppendDemoEvents` with that non-empty ID; an alert writes an action/run/event record even when it changes no demo values. The ID is an audit/correlation value only and must never be copied into `Event`, outbox payload JSON, or APNs payload JSON.

- [ ] **Step 7: Add the expected-revision conflict to the poll path**

Change the signature to `func (p *Poller) PollDemoNow(ctx context.Context, expectedRevision int64, demoRunID string, targets []Device) (DemoPipelineResult, error)`. Under `p.mu`, load demo state first; when `expectedRevision != 0 && expectedRevision != state.Revision`, return the zero result and `ErrDemoRevisionConflict` (a package sentinel wrapping the current revision, e.g. `fmt.Errorf("%w: current %d", ErrDemoRevisionConflict, state.Revision)`). Otherwise continue into the existing shared `poll` pipeline, recording `demoRunID` on its run/event rows. The target slice is required for demo-originated work and is passed unchanged to later enqueueing; scheduled and bearer real polls pass `nil` and `demoRunID == ""`.

- [ ] **Step 8: Implement the narrow demo API**

Create `server/demo_api.go`:

```go
type DemoPoller interface {
	PollDemoNow(ctx context.Context, expectedRevision int64, demoRunID string, targets []Device) (DemoPipelineResult, error)
}

type DemoAPI struct {
	store          *Store
	poller         DemoPoller
	notifier       Notifier
	deviceIDs      map[string]bool // Config.DemoDeviceIDs allowlist
	identityHeader string          // Config.AccessIdentityHeader
	csrfKey        []byte
	limiter        *rateLimiter
	idem           *idempotencyStore
}

func (d *DemoAPI) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", d.handleIndex)
	mux.HandleFunc("GET /styles.css", d.handleStyles)
	mux.HandleFunc("GET /app.js", d.handleScript)
	mux.HandleFunc("GET /v1/demo", d.handleGetDemo)
	mux.HandleFunc("PATCH /v1/demo", d.guardMutation("patch", d.handlePatchDemo))
	mux.HandleFunc("POST /v1/demo/poll", d.guardMutation("poll", d.handleDemoPoll))
	mux.HandleFunc("GET /v1/demo/events", d.handleDemoEvents)
	mux.HandleFunc("POST /v1/demo/alert", d.guardMutation("alert", d.handleDemoAlert))
	return d.withSecurityHeaders(mux)
}
```

`guardMutation` runs, in order: same-origin (`sameOriginOK`), content-type `application/json`, CSRF (`verifyCSRFToken`), and identity extraction from `strings.TrimSpace(r.Header.Get(d.identityHeader))`; a missing or blank configured identity is `403 {"error":"access identity required"}` before rate limiting, with no anonymous fallback. It then rate-limits, atomically reserves `(identity, route, Idempotency-Key)`, executes only the owner, and completes/audits the entry. A duplicate waits for completion then replays its stored status/body with `Idempotency-Replayed: true`, or after the bounded wait receives `409 {"error":"request in progress"}`. Create one `DemoAction` at mutation entry and return/persist its ID. Do not wrap this mux in bearer middleware; add no CORS headers. `GET /v1/demo` includes a fresh `csrfToken`. `handleDemoAlert` selects `Store.DemoTargets(d.deviceIDs)` and, in Task 8, enqueues only those target devices; it never calls `Notifier` directly.

Define the sole public error vocabulary in `demo_api.go`: `"invalid request"`, `"access identity required"`, `"revision conflict"`, `"demo state unavailable"`, `"demo poll failed"`, `"demo normalization failed"`, `"demo delivery enqueue failed"`, `"rate limited"`, and `"request in progress"`. Each handler maps a typed stage error to one of these messages, logs the wrapped error with `log.Printf`, and never serialize `err.Error()` in a demo response or audit field. Keep conflict/rate-limit numeric fields only where frozen by the contract.

- [ ] **Step 9: Add body/response limits and security headers**

`http.MaxBytesReader` 16 KiB for PATCH/poll/alert, `json.Decoder.DisallowUnknownFields`, a second-decode EOF check, event default limit 50 / max 100, a 90-second poll context, and a bounded response writer. Set on every response:

```text
Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'
X-Content-Type-Options: nosniff
Referrer-Policy: no-referrer
Cache-Control: no-store
```

- [ ] **Step 10: Wire CSRF, idempotency, action IDs, and revision into the frontend**

In `server/web/model.ts` extend the request builders to carry `expectedRevision` (from the last loaded `state.revision`) in the poll body and expose helpers for the mutation headers. In `server/web/app.ts`: store `csrfToken` from `GET /v1/demo`; generate one `crypto.randomUUID()` `Idempotency-Key` per initiated action; send `X-Demo-CSRF` and `Content-Type: application/json` on every mutation; on `409` reload state/events and re-read the revision; on `429` surface the `retryAfterSeconds` hint and keep controls disabled until then. Never request or store a bearer token. Rebuild the committed bundle.

```bash
cd server/web
bun test
bun run build
```

- [ ] **Step 11: Run focused, full, race, and vet checks, then commit**

```bash
cd server
go test . -run 'TestCSRF|TestSameOrigin|TestRateLimiter|TestIdempotency|TestApplyDemoPatchIncrementsRevision|TestSaveDemoStatePersistsRevision|TestDemo(ActionID|AuditRetention|EventRetention)|TestPollDemoNowRevisionConflict|TestDemoRouteAllowlist|TestDemoMutation|TestDemoPoll|TestDemoIdempotent|TestDemoAlert|TestDemoPatchRejects|TestDemoErrorsRedacted|TestBearerDemoAlert|TestLoadConfigDemoDeviceIDsAndAccessIdentityHeader'
go test ./...
go test -race ./...
go vet ./...
```

Expected: PASS.

```bash
curl -s api.ipify.org
# If required: tailscale set --exit-node=<approved-node>
git add server/demo_api.go server/demo_api_test.go server/demo_guard.go server/demo_guard_test.go \
        server/demo.go server/demo_test.go server/demo_store.go server/demo_store_test.go \
        server/poller.go server/poller_test.go server/api.go server/api_test.go \
        server/config.go server/config_test.go \
        server/web/model.ts server/web/model.test.ts server/web/app.ts server/web/app.js
git commit -m "Secure the demo console API"
# If enabled: tailscale set --exit-node=
```

---

### Task 6: Start the Default-Off Loopback Listener

**Files:**
- Modify: `server/config.go:8-52`
- Modify: `server/config_test.go`
- Modify: `server/cmd/usagewidgetd/main.go:15-67`

**Interfaces:**
- Consumes: `DemoAPI.Handler()` and the existing API/poller/store setup.
- Produces: `Config.DemoEnabled` (default false), `Config.DemoListenAddr` (loopback-only), and a second HTTP server started only when enabled. `DemoDeviceIDs` and `AccessIdentityHeader` already exist from Task 5.
- Preserves: the current `Config.ListenAddr` default and `:8377` behavior; when demo is disabled only `:8377` runs.

- [ ] **Step 1: Write failing configuration and lifecycle tests**

```go
func TestDemoDisabledByDefault(t *testing.T) {
	t.Setenv("USAGEWIDGET_TOKEN", "test")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DemoEnabled {
		t.Fatal("demo must be disabled unless USAGEWIDGET_DEMO_ENABLED=true")
	}
}

func TestLoadConfigRejectsPublicDemoListener(t *testing.T) {
	t.Setenv("USAGEWIDGET_TOKEN", "test")
	t.Setenv("USAGEWIDGET_DEMO_ENABLED", "true")
	t.Setenv("DEMO_LISTEN_ADDR", "0.0.0.0:8378")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected public demo listener to be rejected")
	}
}
```

Also test the `127.0.0.1:8378` default, an accepted explicit loopback override, and rejection of `:8378` and malformed addresses (only when enabled). Device allowlist and Access-header parsing are Task 5 tests and are not reimplemented here.

- [ ] **Step 2: Run tests and confirm the failure**

```bash
cd server
go test . -run 'TestDemoDisabledByDefault|TestLoadConfig|TestDemoListen'
```

Expected: missing `DemoEnabled` fields or an accepted public binding.

- [ ] **Step 3: Add default-off, loopback-only configuration**

Extend the Task 5 `Config` with only `DemoEnabled bool` and `DemoListenAddr string`. In `LoadConfig`: `DemoEnabled` from `USAGEWIDGET_DEMO_ENABLED == "true"` (default false); `DemoListenAddr` from `DEMO_LISTEN_ADDR` default `127.0.0.1:8378`; when `DemoEnabled`, parse with `net.SplitHostPort` and require `net.ParseIP(host).IsLoopback()`. Validate the listener only when enabled so disabled deployments never fail on it.

- [ ] **Step 4: Construct the dedicated server with fixed limits**

Build the demo `http.Server` only when `cfg.DemoEnabled`:

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

Do not change the existing `:8377` server timeouts.

- [ ] **Step 5: Implement the conditional dual-listener lifecycle**

In `main`:

1. Always construct the `:8377` listener; construct the `127.0.0.1:8378` listener only when `cfg.DemoEnabled`.
2. If either required bind fails, close the other and exit before background work starts.
3. Start `poller.Run(ctx)` (and, from Task 8, the outbox worker).
4. Serve each active HTTP server in its own goroutine using a buffered error channel.
5. Cancel the shared context on a signal or an unexpected failure from either server.
6. Ignore `http.ErrServerClosed`.
7. Shut both active servers down concurrently under one ten-second context.
8. Wait for poller/outbox exit, then close SQLite.
9. Do not call `log.Fatalf` from goroutines.

- [ ] **Step 6: Run server verification and commit**

```bash
cd server
go test . -run 'TestDemoDisabledByDefault|TestLoadConfig|TestDemoListen'
go test ./...
go test -race ./...
go vet ./...
```

```bash
curl -s api.ipify.org
git add server/config.go server/config_test.go server/cmd/usagewidgetd/main.go
git commit -m "Serve the demo console on a default-off loopback listener"
```

---

### Task 7: Freeze the Dashboard Contract

Gate: do not begin Task 8 until this task is committed. This freezes the request/response and result shapes so the reliability refactor cannot silently break the console.

**Files:**
- Modify: `docs/superpowers/specs/2026-07-18-demo-dashboard-design.md`
- Modify: `server/demo_api_test.go` (add a shape-guard test)

**Interfaces:**
- Consumes: the contract produced in Task 5.
- Produces: a written "Frozen Contract v1" section and `TestFrozenContractShape`.
- Preserves: the exact response keys documented below, including the required `deliveryHealth` field on `GET /v1/demo` and every mutation response; later tasks may not add, rename, or remove fields without a new versioned contract freeze.

- [ ] **Step 1: Write the failing shape-guard test**

`server/demo_api_test.go` add `TestFrozenContractShape`: assert the exact top-level JSON keys and JSON value types of all five read/mutation response shapes, including the required `deliveryHealth` field on `GET /v1/demo` and every mutation response.

```go
func TestFrozenContractShape(t *testing.T) {
	// GET /v1/demo -> state, snapshot, pipeline, csrfToken, deliveryHealth
	// PATCH /v1/demo -> state, demoRunID, deliveryHealth
	// POST /v1/demo/poll -> pipeline, events, demoRunID, deliveryHealth
	// POST /v1/demo/alert -> delivery, demoRunID, deliveryHealth
	// GET /v1/demo/events -> events
	// Fail on any missing, extra, renamed, or type-changed key.
}
```

- [ ] **Step 2: Run and confirm failure**

```bash
cd server
go test . -run 'TestFrozenContractShape'
```

Expected: failure until the assertions match the implemented handlers.

- [ ] **Step 3: Document the frozen contract**

Add a "Frozen Contract v1 (2026-07-18)" section to the design spec enumerating, verbatim:

- The `GET /v1/demo`, `PATCH /v1/demo`, `POST /v1/demo/poll`, `GET /v1/demo/events`, and `POST /v1/demo/alert` request and response bodies.
- The mutation header requirements (`X-Demo-CSRF`, `Content-Type`, `Origin`/`Sec-Fetch-Site`, `Idempotency-Key`) and the CSRF token format.
- `DemoState` including `revision`, `DemoStatePatch`, `DemoPipelineResult`, `DemoPipelineStage`, `DemoDeliveryResult`, `DeliveryCount`, and `DemoEventRecord` field names.
- The security headers from Task 5 Step 8.
- The stability rule: `DemoDeliveryResult` counts represent enqueue/known-terminal outcomes at response time (Task 8 makes delivery asynchronous but keeps `attempted`/`succeeded`/`failed` field names; pending deliveries count as `attempted`). `deliveryHealth` is a frozen field in `GET /v1/demo` and every mutation response; its value is computed at response time and is not persisted into snapshot freshness. The same field is present in `GET /v1/health` and `GET /v1/snapshot` on `:8377`.

- [ ] **Step 4: Run and commit**

```bash
cd server
go test . -run 'TestFrozenContractShape'
go test ./...
```

```bash
curl -s api.ipify.org
git add docs/superpowers/specs/2026-07-18-demo-dashboard-design.md server/demo_api_test.go
git commit -m "Freeze the demo dashboard contract"
```

---

### Task 8: Implement the Persistent Per-Device APNs Outbox

**Files:**
- Create: `server/outbox.go`
- Create: `server/outbox_test.go`
- Modify: `server/store.go` (additive delivery schema + atomic write + retention)
- Modify: `server/store_test.go`
- Modify: `server/apns.go:22-122` (typed results)
- Modify: `server/apns_test.go`
- Modify: `server/events.go:47-134` (create durable delivery work)
- Modify: `server/events_test.go`
- Modify: `server/poller.go:249-331` (enqueue instead of synchronous dispatch)
- Modify: `server/poller_test.go`
- Modify: `server/api.go` and `server/api_test.go` (migrate the bearer `:8377` test-alert route to enqueue-only)
- Modify: `server/demo_api.go` and `server/demo_api_test.go` (migrate the console alert route to enqueue-only)
- Modify: `server/cmd/usagewidgetd/main.go` (start the outbox worker)

**Interfaces:**
- Produces: `Store` delivery methods (`InsertEventWithDeliveries`, `EnqueueTestAlert`, `ClaimDueDeliveries`, `MarkDeliveryAccepted`, `MarkDeliveryPending`, `MarkDeliveryPermanentlyFailed`, `PruneTerminalDeliveries`); `APNsResult`/`APNsResultKind`; `Outbox` worker with `Run(ctx)`.
- Consumes: existing `events` dedup table, explicit `[]Device` targets, `Notifier`.
- Preserves: `EventEngine.Process` signature, `DemoDeliveryResult` field names, and the `events`/`window_state` tables. Additive-only schema.

- [ ] **Step 1: Write failing outbox, classification, and atomicity tests**

`server/apns_test.go`: `TestClassifyAPNsStatus` — 200 -> `APNsAccepted`; 429/500/503 -> `APNsTransient`; 400/403/410 with `BadDeviceToken`/`Unregistered` -> `APNsInvalidToken`; other 4xx -> `APNsPermanent`.

`server/store_test.go`: `TestInsertEventWithDeliveriesAtomic` (event dedup row and all per-device pending rows commit together or roll back together), `TestEventClaimCreatesDeliveriesOnlyOnce` (a duplicate event claim produces no delivery rows), `TestClaimDueDeliveriesRespectsBackoffAndLease`, `TestLeaseRequiredFinalization`, `TestExpiredLeaseRetries`, and `TestPruneTerminalDeliveriesRetains7Days` (pending rows are never pruned).

`server/outbox_test.go`: `TestOutboxRetriesTransientWithBackoff` (<=3 attempts over <=5 minutes), `TestOutboxPerDeviceIndependence` (one device accepted, another still pending), `TestOutboxInvalidTokenPermanentNoRetirement` (delivery permanently_failed, device row untouched, token suffix logged), `TestOutboxUsesStableCollapseID`, `TestOutboxRestartResumesPending`, and four crash-point cases: before commit, after commit before send, after acceptance before ledger update (at-least-once, documented), after ledger update.

`server/store_test.go`: `TestOutboxRollbackCompatibility` is an explicit integration gate: create the DB with the outbox schema, build `/tmp/usagewidgetd-legacy-5193cc0` from a detached worktree at pre-outbox commit `5193cc0`, and run that legacy binary against the DB for its legacy snapshot/settings/event reads. It must succeed before the current binary reopens the same DB and proves pending rows resume without duplicating accepted deliveries. A current-code DB-open test is not acceptable evidence.

`server/api_test.go` and `server/demo_api_test.go`: verify both HTTP alert routes insert persistent work for only the explicit allowlisted targets and that their fake notifier receives zero calls. `server/poller_test.go`: verify poll-generated thresholds, surprise reset, and widget refresh use only the supplied demo target set, excluding a registered non-allowlisted device.

- [ ] **Step 2: Run and confirm failure**

```bash
cd server
go test . -run 'TestClassifyAPNs|TestInsertEventWithDeliveries|TestClaimDue|TestPruneTerminal|TestOutbox'
```

Expected: compile failure because the delivery schema, `APNsResult`, and `Outbox` do not exist.

- [ ] **Step 3: Add the additive delivery schema**

Add to `server/store.go` `schema` (executed from `OpenStore`, additive):

```sql
CREATE TABLE IF NOT EXISTS event_deliveries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_key TEXT NOT NULL,
    device_id TEXT NOT NULL,
    token_hash TEXT NOT NULL,     -- sha256(device token), never the token
    token_suffix TEXT NOT NULL,   -- last six chars for logs only
    kind TEXT NOT NULL,            -- alert | widget
    state TEXT NOT NULL,           -- pending | accepted | permanently_failed
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TEXT NOT NULL,
    lease_id TEXT,
    lease_until TEXT,
    last_status INTEGER,
    last_reason TEXT,
    payload TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(event_key, device_id, token_hash, kind)
);
CREATE INDEX IF NOT EXISTS idx_event_deliveries_due ON event_deliveries(state, next_attempt_at, lease_until);
```

- [ ] **Step 4: Write event and delivery rows atomically**

Define `PendingDelivery` with `EventKey`, `DeviceID`, `Token`, `TokenHash`, `Kind`, `Payload`, and a stable `Identity`; `TokenHash` is SHA-256 of the full APNs token and `Identity` is `sha256(eventKey + "\\x00" + deviceID + "\\x00" + tokenHash + "\\x00" + kind)`. In one `s.db.Begin()` transaction, `InsertEventWithDeliveries` inserts the dedup `events` row and checks `RowsAffected`. It inserts delivery rows **only when that event claim was newly inserted**; a pre-existing event claim creates none. The delivery UNIQUE key is `(event_key, device_id, token_hash, kind)`. `token_suffix` is only the final six token characters. `EnqueueTestAlert` uses the same transaction/outbox rows for both alert and widget work, with a route/action-derived event key. The APNs network call never happens inside this transaction.

- [ ] **Step 5: Type APNs results**

In `server/apns.go` add:

```go
type APNsResultKind int

const (
	APNsAccepted APNsResultKind = iota
	APNsTransient
	APNsPermanent
	APNsInvalidToken
)

type APNsResult struct {
	Kind   APNsResultKind
	Status int
	Reason string
}

func classifyAPNsStatus(status int, reason string) APNsResultKind
```

Replace the notifier contract exactly:

```go
type Notifier interface {
	SendAlert(ctx context.Context, deviceToken string, ev Event, collapseID string) (APNsResult, error)
	SendWidgetRefresh(ctx context.Context, widgetToken, collapseID string) (APNsResult, error)
}
```

- [ ] **Step 6: Emit durable delivery work from the event engine**

Refactor `EventEngine.ProcessDetailed` so that, instead of returning events for immediate dispatch, claimed events and the changed-snapshot widget refresh are written as pending deliveries through `InsertEventWithDeliveries`. Add an explicit `DeliveryTargets []Device`/`DemoRunID string` input owned by the poll invocation: real polls use their normal registered-device target selection; every demo poll, including surprise reset, uses only the precomputed allowlisted targets. Keep `Emitted`/`Outcomes` for the demo pipeline result counts, but they now represent enqueued work. Do not call `Store.ListDevices()` from a demo-originated path.

- [ ] **Step 7: Implement the outbox worker**

Create `server/outbox.go`:

```go
type Outbox struct {
	store    *Store
	notifier Notifier
	interval time.Duration // 10s poll
}

func (o *Outbox) Run(ctx context.Context) // claim due -> send -> classify -> mark
```

Claim each row with one SQLite statement using a random lease ID and `UPDATE ... RETURNING`: select a due pending row or a pending row whose `lease_until <= now`, then atomically set `lease_id = ?`, `lease_until = now + 30s`, and increment `attempts`. No worker may send an unleased row. `MarkDeliveryAccepted`, `MarkDeliveryPending`, and `MarkDeliveryPermanentlyFailed` each use `WHERE id = ? AND lease_id = ?` and clear the lease, so a stale worker cannot finalize a reclaimed delivery. Expired leases become eligible for retry; the next claim must preserve the attempt budget. Backoff: `next_attempt_at = now + base * 2^(attempts-1)` capped so total elapsed <= 5 minutes and `attempts <= 3`; on exhaustion mark `permanently_failed`. `APNsAccepted` -> `accepted`; transport/APNs transient -> `pending` with next attempt; configuration/serialization/APNs permanent/invalid-token -> `permanently_failed`. Accepted and permanently-failed rows are retained 7 days by `PruneTerminalDeliveries` (run on startup and daily); pending rows are never pruned. Document the at-least-once window (acceptance recorded but process crashes before ledger update) in a `// ponytail:` note and in the README (Task 11).

The claim implementation must use this single statement (one worker transaction per returned row):

```sql
UPDATE event_deliveries
SET lease_id = :lease_id,
    lease_until = :lease_until,
    attempts = attempts + 1,
    updated_at = :now
WHERE id = (
    SELECT id FROM event_deliveries
    WHERE state = 'pending'
      AND next_attempt_at <= :now
      AND (lease_until IS NULL OR lease_until <= :now)
    ORDER BY next_attempt_at, id
    LIMIT 1
)
AND state = 'pending'
AND (lease_until IS NULL OR lease_until <= :now)
RETURNING id, event_key, device_id, token_hash, token_suffix, kind, attempts,
          lease_id, lease_until, payload;
```

`ClaimDueDeliveries` returns no row when this statement returns no row. The returned `lease_id` is required by every finalize method and tests must prove a stale lease ID changes zero rows.

- [ ] **Step 8: Route the poller through the outbox and start the worker**

In `server/poller.go`, replace synchronous `dispatch` with enqueue-only writes; `DemoDeliveryResult` now reports enqueued counts (`attempted`) plus any terminal outcomes already known. Keep the `apns` pipeline stage but label it enqueue. In `server/api.go` and `server/demo_api.go`, replace both HTTP test-alert notifier calls with `EnqueueTestAlert` using explicit demo targets; neither HTTP handler may invoke `Notifier`. In `main.go`, construct `Outbox` and start `go outbox.Run(ctx)` alongside the poller; include it in graceful shutdown.

- [ ] **Step 9: Run focused, full, race, and vet checks, then commit**

Run the pinned legacy-reader integration gate before the normal suite:

```bash
legacy_dir=$(mktemp -d /tmp/usagewidget-legacy-XXXXXX)
curl -s api.ipify.org
# If required by policy, enable the approved private-network exit node temporarily.
git worktree add --detach "$legacy_dir" 5193cc0
(cd "$legacy_dir/server" && go build -o /tmp/usagewidgetd-legacy-5193cc0 ./cmd/usagewidgetd)
cd server
go test . -run TestOutboxRollbackCompatibility -count=1
curl -s api.ipify.org
# Apply the same exit-node rule before this git command.
git worktree remove "$legacy_dir"
```

`TestOutboxRollbackCompatibility` starts the pinned binary with a test-only database path, verifies its legacy reads, stops it, then reopens that same path with current `OpenStore` and an outbox worker. The test must remove the temporary database and process on cleanup, including failure.

```bash
cd server
go test . -run 'TestClassifyAPNs|TestInsertEventWithDeliveries|TestEventClaimCreatesDeliveriesOnlyOnce|TestClaimDue|TestLeaseRequiredFinalization|TestExpiredLease|TestPruneTerminal|TestOutbox|Test(Bearer)?DemoAlert'
go test ./...
go test -race ./...
go vet ./...
```

```bash
curl -s api.ipify.org
git add server/outbox.go server/outbox_test.go server/store.go server/store_test.go \
        server/apns.go server/apns_test.go server/events.go server/events_test.go \
        server/poller.go server/poller_test.go server/api.go server/api_test.go \
        server/demo_api.go server/demo_api_test.go server/cmd/usagewidgetd/main.go
git commit -m "Add a persistent per-device APNs outbox"
```

---

### Task 9: Separate Snapshot Freshness from Delivery Health

**Files:**
- Modify: `server/api.go:102-168` (health `deliveryHealth`, snapshot 503)
- Modify: `server/api_test.go`
- Modify: `server/poller.go` (compute delivery health, never mutate freshness)
- Modify: `server/poller_test.go`
- Modify: `server/store.go` (delivery-health query)
- Modify: `server/store_test.go`

**Interfaces:**
- Produces: `Store.DeliveryHealth(now) (DeliveryHealth, error)` where `DeliveryHealth` contains `Status` (`"ok"` | `"degraded"`) and separately bounded `RecentTerminalFailures`; `healthResponse.DeliveryHealth`, `Snapshot.DeliveryHealth`, and the frozen demo-response `deliveryHealth` field.
- Consumes: the `event_deliveries` table from Task 8.
- Preserves: `Snapshot.Stale` whole-upstream semantics; existing snapshot JSON keys.

- [ ] **Step 1: Write failing freshness/health tests**

- `TestDeliveryFailureDoesNotMarkStale` — terminally classified deliveries leave the persisted snapshot `stale == false`.
- `TestDeliveryHealthRecoversWhenWorkDrainsOrIsTerminal` — only pending work past its retry/lease deadline degrades health; health becomes `"ok"` after the work is accepted **or** terminally classified.
- `TestRecentTerminalFailuresExposedSeparately` — recent terminal failures appear in the bounded diagnostic list/counter without keeping `deliveryHealth` degraded.
- `TestSnapshotDatabaseUnreadable503` — when the snapshot read fails, `GET /v1/snapshot` returns `503` and does not synthesize a freshness state.
- `TestDemoFetchOrNormalizeFailurePreservesStaleSnapshot` — a demo fetch or normalize error calls the same stale-preservation path as a real upstream failure and retains the last valid snapshot with stale state.
- `TestSnapshotWriteFailurePreservesPriorSnapshot` — a recoverable snapshot write failure rolls back the attempted write, retains the prior committed snapshot, and marks freshness stale.
- `TestDeliveryHealthInjectedNotPersisted` — `deliveryHealth` appears in `:8377` health, `:8377` snapshot, and demo GET/PATCH/poll/alert responses, but is absent from the persisted snapshot JSON and never changes `Snapshot.Stale`.

- [ ] **Step 2: Run and confirm failure**

```bash
cd server
go test . -run 'TestDeliveryFailureDoesNotMarkStale|TestDeliveryHealthRecovers|TestRecentTerminalFailures|TestSnapshotDatabaseUnreadable503|TestDemoFetchOrNormalizeFailurePreservesStaleSnapshot|TestSnapshotWriteFailurePreservesPriorSnapshot|TestDeliveryHealthInjectedNotPersisted'
```

Expected: failure because `DeliveryHealth` and the 503 path do not exist.

- [ ] **Step 3: Implement the split**

Add `Store.DeliveryHealth(now)` returning `"degraded"` only for outstanding pending work overdue for retry or an expired lease; it returns `"ok"` once work drains or is terminally classified. Query recent `permanently_failed` rows separately (bounded and redacted to delivery ID/time/status/reason) for diagnostics. Add the frozen `deliveryHealth` shape to `healthResponse`, inject it into the response-only `Snapshot` returned by `handleGetSnapshot`, and include it in DemoAPI reads and every mutation response. Do not write this field back to `snapshots.payload`. An unreadable or corrupt database read (including corrupt stored payload/settings) returns fixed `503 {"error":"snapshot temporarily unavailable"}` instead of a freshness-implying `500`, with wrapped details only in server logs. Confirm `Poller` calls `markStale` for both real and demo fetch/normalize failures, preserving the prior snapshot; a recoverable snapshot write failure rolls back the attempted write, preserves the prior committed snapshot, and marks freshness stale. Delivery outcomes never call `markStale`.

- [ ] **Step 4: Run and commit**

```bash
cd server
go test ./...
go test -race ./...
go vet ./...
```

```bash
curl -s api.ipify.org
git add server/api.go server/api_test.go server/poller.go server/poller_test.go server/store.go server/store_test.go
git commit -m "Split snapshot freshness from delivery health"
```

---

### Task 10: Decode and Render Provider Staleness, Delivery Health, Cached Age, and Foreground Banner on iOS

**Files:**
- Modify: `ios/Sources/Core/Models.swift:17-121` (provider `stale`, snapshot `deliveryHealth`)
- Modify: `ios/Sources/Core/SnapshotStore.swift` (cached age)
- Modify: `ios/Sources/App/DashboardView.swift:86-210` (stale + delivery-health surface)
- Modify: `ios/Sources/Widget/ProviderWidget.swift:162-280` (provider stale indicator)
- Modify: `ios/Sources/App/UsageWidgetApp.swift:52-82` (foreground `willPresent`)
- Modify: `ios/Tests/CoreTests/ModelsAndStoreTests.swift`

**Interfaces:**
- Consumes: normalized provider `stale` (Task 1) and health `deliveryHealth` (Task 9).
- Produces: backward-compatible `Provider.stale`, optional `Snapshot.deliveryHealth` and health `deliveryHealth` decode, cached-age display, and a foreground notification banner.
- Preserves: `Snapshot.stale` whole-upstream behavior, existing provider rendering, and legacy-server payload decoding.

- [ ] **Step 1: Write failing decode and presentation tests**

```swift
func testProviderStaleDefaultsToFalse() throws {
    let data = Data(#"{"fetchedAt":"2026-07-18T12:00:00Z","stale":false,"providers":[{"id":"demo","name":"Demo","windows":[]}],"pollIntervalMinutes":5}"#.utf8)
    let snapshot = try JSONCoding.decoder.decode(Snapshot.self, from: data)
    XCTAssertFalse(snapshot.providers[0].stale)
}
```

Add: one fixture with `"stale": true` asserting `providers[0].stale == true` while `snapshot.stale` stays independent; a `deliveryHealth` decode test tolerating its absence on legacy payloads; and a `SnapshotStore` cached-age test.

- [ ] **Step 2: Run the iOS test and confirm the failure**

```bash
cd ios
xcodebuild -project UsageWidget.xcodeproj -scheme UsageWidget -destination 'platform=iOS Simulator,name=iPhone 17 Pro' test
```

Expected: compile failure because `Provider.stale` does not exist, or a decoding assertion failure.

- [ ] **Step 3: Add backward-compatible decoding**

Add `public var stale: Bool` to `Provider` with `stale = try c.decodeIfPresent(Bool.self, forKey: .stale) ?? false` in the existing custom `init(from:)`, extending `CodingKeys`. Add an optional `deliveryHealth: String?` to both `Snapshot` and the health model, each decoded with `decodeIfPresent`. Legacy server payloads must continue to decode.

- [ ] **Step 4: Surface stale, delivery health, and cached age**

In `SnapshotStore.swift`, expose the persisted snapshot age for offline/stale display. In `DashboardView.swift`, show the existing stale warning only on the affected provider row and a non-blocking delivery-health indicator when `deliveryHealth == "degraded"`; label cached data with its age during offline. In `ProviderWidget.swift`, show the same provider-scoped stale indicator. Do not promote provider stale to the global snapshot banner and do not desaturate real providers.

- [ ] **Step 5: Add the foreground notification banner**

In `AppDelegate` (already the `UNUserNotificationCenterDelegate`), implement:

```swift
func userNotificationCenter(
    _ center: UNUserNotificationCenter,
    willPresent notification: UNNotification,
    withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
) {
    completionHandler([.banner, .sound, .list])
}
```

This closes the CEO plan's foreground visible-alert gap so a push shows a banner and plays a sound while the app is foregrounded.

- [ ] **Step 6: Run tests and an unsigned build, then commit**

```bash
cd ios
xcodebuild -project UsageWidget.xcodeproj -scheme UsageWidget -destination 'platform=iOS Simulator,name=iPhone 17 Pro' test
xcodebuild -project UsageWidget.xcodeproj -scheme UsageWidget -destination 'generic/platform=iOS' CODE_SIGNING_ALLOWED=NO build
```

Expected: PASS and BUILD SUCCEEDED.

```bash
curl -s api.ipify.org
git add ios/Sources/Core/Models.swift ios/Sources/Core/SnapshotStore.swift \
        ios/Sources/App/DashboardView.swift ios/Sources/Widget/ProviderWidget.swift \
        ios/Sources/App/UsageWidgetApp.swift ios/Tests/CoreTests/ModelsAndStoreTests.swift
git commit -m "Render provider staleness and foreground alerts on iOS"
```

---

### Task 11: Evidence Gates, Deploy, and Documentation

**Files:**
- Modify: `server/deploy/redeploy.sh`
- Modify: `server/deploy/README.md`
- Modify: `README.md`
- Modify: `HUMANS.md`
- Create: `docs/designs/demo-dashboard.html` (promote the approved mockup)
- Create: `docs/evidence/index.md` (candidate/gate evidence index)

**Interfaces:**
- Consumes: all prior tasks.
- Produces: default-off reproducible deploy, Access-before-Tunnel human steps, candidate-tag gates, the tracked approved design, three diagrams, and the evidence pack.

- [ ] **Step 1: Track the approved design source**

Copy `.superpowers/brainstorm/79315-1784346965/content/lab-console.html` to `docs/designs/demo-dashboard.html` so the approved visual source of truth is version-tracked.

- [ ] **Step 2: Update the deploy script for default-off deploy**

Before the Go build:

```bash
cd "$REPO_DIR/server/web"
bun test
bun run build

cd "$REPO_DIR/server"
go test ./...
```

Then cross-compile/install/restart as today. The demo listener stays off unless `USAGEWIDGET_DEMO_ENABLED=true`. After restart, keep the existing `:8377` bearer smoke check; add demo checks only when the flag is enabled:

```bash
curl --fail --silent http://127.0.0.1:8378/ >/dev/null
curl --fail --silent http://127.0.0.1:8378/v1/demo >/dev/null
test "$(curl --silent --output /dev/null --write-out '%{http_code}' http://127.0.0.1:8378/v1/health)" = "404"
ss -ltn | grep -F '127.0.0.1:8378'
```

Do not send alerts or mutate demo state during deployment.

- [ ] **Step 3: Document Access-before-Tunnel and the identity header**

Add to `server/deploy/README.md` and `HUMANS.md`:

1. Confirm the Access identity header against the live tenant and current docs before enabling the flag; set `ACCESS_IDENTITY_HEADER` (default `Cf-Access-Authenticated-User-Email`).
2. Create a Cloudflare Access self-hosted application for `demo.example.com`.
3. Add a narrow Allow policy for the operator's exact identity/group; never `Everyone`.
4. Configure the IdP, session duration, and MFA policy.
5. Create/select the server Tunnel and publish the hostname to `http://127.0.0.1:8378`.
6. Enable Protect with Access before exposing the route.
7. Set `USAGEWIDGET_DEMO_ENABLED=true` and `DEMO_DEVICE_IDS` only after Access is confirmed.
8. Verify unauthenticated denial, unauthorized denial, authorized access, healthy connector, and same-origin mutation without a browser-held token.

- [ ] **Step 4: Run the full automated gate (Gate 1) against one candidate**

Immediately before Gate 1, commit all intended candidate content and create the immutable candidate tag; every gate below records that exact tag, commit, binary checksum, and build identifiers. Do not create the tag after Gate 1.

```bash
curl -s api.ipify.org
# If required by policy, enable the approved private-network exit node temporarily.
git tag -a portfolio-candidate-N -m "Portfolio candidate N"
git rev-parse portfolio-candidate-N
```

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

Any fix creates a new candidate commit/tag and reruns Gate 1 plus every affected later gate; never attach new evidence to a moved or reused tag.

- [ ] **Step 5: Run Gate 2 (deterministic delivery)**

Use an APNs test server/transport to deterministically verify transient retry/backoff, permanent failure, invalid-token permanent-failure without device mutation, per-device independence, outbox restart recovery, and every crash boundary. Label fixture evidence simulated. Record results under `docs/evidence/` naming the candidate tag, builds, expected vs actual, and artifact paths.

- [ ] **Step 6: Run Gates 3 and 4 (physical device and browser)**

Physical iPhone matrix from the Cloudflare dashboard: threshold, danger, credits increase, surprise reset, normal reset, demo stale isolation, provider error isolation, restart dedup, test alert, repeated-poll no-duplicate, offline cached-age, and real-provider isolation. For each distinct alert scenario, prove and timestamp both (a) app foreground presentation and (b) device locked/background presentation, with both alerts received within two minutes of the console mutation. Record device model, iOS version/build, candidate tag/build, notification permission state, Background App Refresh state, Low Power Mode state, Focus state if enabled, foreground/locked/background state, network condition (Wi-Fi/cellular/offline), mutation time, APNs receipt/presentation time, and widget refresh observation time. Record widget latency through 30 minutes after mutation, including no-update observations; do not mark a case complete merely because the alert arrived. Browser matrix on current Safari and Chromium: authentication, every route/control, loading/empty/error/success/stale states, CSRF/origin/rate-limit enforcement, idempotency replay, revision conflict, keyboard-only, responsive one-column, reduced motion, demo-device allowlisting, audit logging, and error redaction. Allow one retry per real-device scenario.

- [ ] **Step 7: Produce README, three diagrams, and the evidence manifest (Gate 5)**

Only after Gates 1–4 pass on one candidate without rebuild: update `README.md` with the problem, architecture, trust boundaries, delivery-health, setup reality, screenshots, demo link, known limitations (including the documented at-least-once acceptance/update crash window), and the later-release roadmap. Produce three diagrams matching the captured commit: architecture plus trust boundaries; runtime data plus error/state flow; deployment plus rollback. Promote the passing candidate to the final tag and record the evidence manifest commit. Capture the required video (real signed client + synthetic dashboard control + real pipeline outcome + one failure/recovery path) last.

- [ ] **Step 8: Commit and deploy**

```bash
curl -s api.ipify.org
git add server/deploy/redeploy.sh server/deploy/README.md README.md HUMANS.md \
        docs/designs/demo-dashboard.html docs/evidence/index.md server/web/app.js
git commit -m "Document, gate, and deploy the demo dashboard"
# Apply the required exit-node routing if needed, then:
git push origin master
```

Expected: push succeeds after all automated and physical-device gates pass.

---

### Task 12: Remove Temporary Demo Surfaces After the Hackathon

**Files:**
- Modify: `server/cmd/usagewidgetd/main.go` (drop demo listener construction)
- Delete: `server/demo_api.go`, `server/demo_api_test.go`, `server/demo_guard.go`, `server/demo_guard_test.go`
- Delete: `server/demo.go`, `server/demo_test.go`, `server/demo_store.go`, `server/demo_store_test.go`
- Delete: `server/web/` demo assets (`index.html`, `styles.css`, `app.ts`, `app.js`, `model.ts`, `model.test.ts`, `package.json`)
- Modify: `server/api.go`, `server/api_test.go`, `server/poller.go`, `server/poller_test.go`, `server/events.go`, and their tests (remove the bearer `:8377` demo-alert route/helper and every demo-only interface/call path)
- Modify: `server/store.go` and `server/store_test.go` (retain the additive demo/outbox table migration without any demo-facing API)
- Modify: `server/config.go` (remove `DemoEnabled`, `DemoListenAddr`, `DemoDeviceIDs`, and `AccessIdentityHeader` handling) and `server/config_test.go`
- Modify: `HUMANS.md` (Cloudflare removal checklist)

**Interfaces:**
- Consumes: the running deployment.
- Produces: a personal product with no demo surface, route, helper, configuration, or reachable interface; only additive demo/outbox tables remain for rollback unless a later explicit migration removes them.
- Preserves: the `:8377` bearer API, Tailscale path, real snapshots, outbox delivery, and iOS behavior.

- [ ] **Step 1: Disable the flag and remove the Cloudflare surface (human)**

Set `USAGEWIDGET_DEMO_ENABLED=false` (or unset), disable/remove the Tunnel published route for `demo.example.com`, confirm the hostname no longer reaches `127.0.0.1:8378`, and remove the Access application/DNS. Record these in `HUMANS.md`.

- [ ] **Step 2: Remove demo code, routes, interfaces, configuration, and assets**

Delete the demo API, guard, domain/store files, and frontend files listed above; drop demo listener construction from `main.go`; remove `POST /v1/demo/alert` and `demoEvent()` from `:8377`; remove demo polling/state/action/audit interfaces; and remove `DemoEnabled`, `DemoListenAddr`, `DemoDeviceIDs`, `AccessIdentityHeader`, and their env parsing/tests. Move only the additive `CREATE TABLE IF NOT EXISTS`/column-exists migration statements for `demo_state`, `demo_runs`, `demo_event_log`, `demo_audit`, and `event_deliveries` into unexported `retainedAdditiveSchema` in `server/store.go`; it exposes no demo method, route, type, or configuration. Do not leave a test-only demo helper or an unreachable exported demo API. Do not drop those tables unless a later explicit numbered migration removes them.

- [ ] **Step 3: Verify the private product still works and commit**

```bash
cd server
go test ./...
go test -race ./...
go vet ./...
GOOS=linux GOARCH=amd64 go build -o /tmp/usagewidgetd ./cmd/usagewidgetd
```

Confirm the `:8377` bearer API, Tailscale URL, real snapshots, and outbox delivery still pass.

Add removal assertions: `TestNoDemoRoutesOrConfig` proves `/v1/demo/alert` and all demo listener routes are 404, and a repository route/config check proves no exported demo interface or `USAGEWIDGET_DEMO_`/`DEMO_DEVICE_IDS`/`ACCESS_IDENTITY_HEADER` parsing remains outside retained additive schema migration code.

```bash
curl -s api.ipify.org
git add -A server/ HUMANS.md
git commit -m "Remove temporary demo dashboard surfaces"
```
