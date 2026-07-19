# UsageWidget Implementation Plan

> **Status:** Historical implementation plan. The shipped implementation has
> evolved beyond this document, including iOS 26 support, the isolated Unix
> socket collector, setup-QR onboarding, readiness diagnostics, hierarchical
> alert rules, and usage forecasts. Use the top-level `README.md` and
> `server/deploy/README.md` for current behavior and operations; keep this file
> as implementation provenance.

## Global Constraints

- Repo layout: `server/` (Go module `usagewidget/server`), `ios/` (XcodeGen project `UsageWidget`).
- Go 1.26, stdlib-first. SQLite via `modernc.org/sqlite` (pure Go, no cgo). No other third-party deps unless a task says so.
- iOS 27 minimum deployment target, SwiftUI, WidgetKit extension, App Group `group.systems.edmundlim.usagewidget`, bundle ID `systems.edmundlim.UsageWidget` (widget: `.widget`).
- Auth: every `/v1` route requires `Authorization: Bearer <token>`; token from env `USAGEWIDGET_TOKEN`. 401 on missing/wrong.
- APNs config entirely from env: `APNS_KEY_PATH` (.p8), `APNS_KEY_ID`, `APNS_TEAM_ID`, `APNS_BUNDLE_ID`, `APNS_ENV` (sandbox|production). No credentials in repo.
- Never hard-code provider lists in normalization or notification logic. Providers/windows are data-driven from the CodexBar response.
- Stable window ID = `<providerID>.<windowKey>` where windowKey ∈ {primary, secondary, tertiary} or the extra window's own key/name slug.
- Preserve unknown JSON fields where the plan says forward-compatible: keep the raw provider JSON blob alongside normalized fields.
- Default thresholds: early alert at 10% used, danger alert at 10% remaining. Configurable globally via settings.
- Threshold alerts fire only on crossings after a baseline exists — never on first poll after install/restart.
- Tibo-blessed reset: exact usage drops before expected reset by ≥50 percentage points, OR from ≥20% used to ≤5% used.
- Dedup: persist event keys in SQLite; an event key is never notified twice.
- Notifications include: provider, window title, used/remaining %, reset time, event type.
- Polling interval settings: one of 1, 5, 15, 30, 60 minutes.
- Initial provider order: codex, claude, grok. Newly discovered providers appended, hidden by default.
- Stale handling: on refresh failure keep last successful snapshot, mark stale with last update time.
- Commits: simple human-sounding messages, NO Co-Authored-By trailer.
- Code style: no comments unless the WHY is genuinely non-obvious.

## CodexBar upstream contract (assumed, defined here as canonical for this repo)

CodexBar `GET http://127.0.0.1:8765/usage` returns:

```json
{
  "providers": [
    {
      "id": "codex",
      "name": "Codex",
      "primary":   {"title": "5h limit",  "usedPercent": 42.0, "resetsAt": "2026-07-17T20:00:00Z"},
      "secondary": {"title": "Weekly",    "usedPercent": 11.5, "resetsAt": "2026-07-21T00:00:00Z"},
      "tertiary":  null,
      "extraRateWindows": [
        {"key": "opus", "title": "Opus weekly", "usedPercent": 3.0, "resetsAt": "2026-07-21T00:00:00Z"}
      ],
      "codexResetCredits": {"availableCount": 2},
      "error": null
    }
  ]
}
```

Any window may be null/absent. `resetsAt` may be null. Unknown providers and extra fields must pass through. A provider may appear with `"error": "..."` and no windows.

## Task 1: Go service core — config, storage, CodexBar client, normalization

Create `server/` Go module `usagewidget/server`.

- `config.go`: read env — `USAGEWIDGET_TOKEN` (required), `CODEXBAR_URL` (default `http://127.0.0.1:8765/usage`), `DB_PATH` (default `./usagewidget.db`), `LISTEN_ADDR` (default `:8377`), APNs vars per Global Constraints (all optional; APNs disabled if missing).
- `store.go`: SQLite via `modernc.org/sqlite`. Tables:
  - `snapshots(id INTEGER PK, fetched_at TEXT, payload TEXT)` — keep only the latest successful one (plus insert new; prune old).
  - `settings(key TEXT PK, value TEXT)` — poll_interval_minutes (default 5), provider_order (JSON array), hidden_providers (JSON array), notifications_enabled (default true), early_threshold_pct (default 10), danger_threshold_pct (default 10).
  - `devices(device_id TEXT PK, apns_token TEXT, widget_token TEXT, updated_at TEXT)`.
  - `events(event_key TEXT PK, created_at TEXT)`.
  - `window_state(window_id TEXT PK, used_percent REAL, resets_at TEXT, credits_available INTEGER, updated_at TEXT)` — baseline for crossing detection.
- `codexbar.go`: HTTP client fetching CodexBar URL, 10s timeout, decode per upstream contract.
- `normalize.go`: convert upstream response → normalized snapshot:
  ```go
  type Snapshot struct {
      FetchedAt time.Time  `json:"fetchedAt"`
      Stale     bool       `json:"stale"`
      Providers []Provider `json:"providers"`
      PollIntervalMinutes int `json:"pollIntervalMinutes"`
  }
  type Provider struct {
      ID      string   `json:"id"`
      Name    string   `json:"name"`
      Error   string   `json:"error,omitempty"`
      Windows []Window `json:"windows"`
      Credits *Credits `json:"credits,omitempty"`
      Raw     json.RawMessage `json:"raw,omitempty"`
  }
  type Window struct {
      ID          string     `json:"id"`     // providerID.windowKey
      Key         string     `json:"key"`    // primary|secondary|tertiary|<extra key>
      Title       string     `json:"title"`
      UsedPercent float64    `json:"usedPercent"`
      RemainingPercent float64 `json:"remainingPercent"`
      ResetsAt    *time.Time `json:"resetsAt,omitempty"`
  }
  type Credits struct{ AvailableCount int `json:"availableCount"` }
  ```
  Extra windows without a `key` derive one from a slug of the title; collisions get numeric suffix. Raw = original provider JSON.
- Unit tests (table-driven): codex/claude/grok fixtures, missing windows, extra named windows (with and without key), unknown provider, null resetsAt, provider-level error, malformed JSON rejected.

Commit when green.

## Task 2: Go HTTP API + bearer auth

In `server/`:

- `api.go` using stdlib `net/http` (Go 1.22+ mux patterns):
  - Middleware: bearer token check on all `/v1/` routes (constant-time compare).
  - `GET /v1/health` → `{service:"ok", codexbar: bool, database: bool, polling: bool, apns: bool, lastPollAt, lastSuccessAt}`.
  - `GET /v1/snapshot` → latest normalized Snapshot from store (stale flag + fetchedAt set from stored data; pollIntervalMinutes from settings).
  - `PUT /v1/settings` → accepts `{pollIntervalMinutes, providerOrder, hiddenProviders, notificationsEnabled, earlyThresholdPct, dangerThresholdPct}` — all optional, validate interval ∈ {1,5,15,30,60}, thresholds in (0,100). Returns updated settings. `GET /v1/settings` returns current.
  - `POST /v1/devices` → `{deviceID, apnsToken?, widgetToken?}` upsert. `DELETE /v1/devices/{deviceID}`.
- Tests: 401 on missing/wrong token, settings validation rejects bad interval/thresholds, device upsert + rotation (same deviceID new tokens), delete, snapshot returns stored data, health shape.

Commit when green.

## Task 3: Go notification engine + APNs + poller

In `server/`:

- `apns.go`: minimal APNs HTTP/2 client using `golang.org/x/net/http2` NOT needed — stdlib http.Client speaks h2 over TLS to `api.push.apple.com`/`api.sandbox.push.apple.com`. JWT ES256 provider-token auth from .p8 (stdlib crypto/ecdsa + x509; JWT assembled manually — no jwt lib). Cache token 40 min. Two send kinds: alert push (to apnsToken) and widget-refresh push (to widgetToken, `apns-push-type: widgets`, topic `<bundleID>.push-type.widgets`). If APNs env unset, sender is a no-op that logs.
- `events.go`: given previous window_state + new snapshot + settings, produce events:
  - `early_threshold`: crossed from < early% used to ≥ early% used.
  - `danger_threshold`: crossed from remaining > danger% to remaining ≤ danger%.
  - `reset`: previous resetsAt is in the past relative to new fetch time and window shows a new cycle (resetsAt changed or usage dropped). Event key includes the old resetsAt so one per cycle.
  - `tibo_reset`: providerID == "codex" style detection is NOT hard-coded to codex — applies to any window whose usage drops materially before its expected resetsAt per Global Constraints rule. Title the event "Tibo blessed" only when provider id is codex; generic "surprise reset" otherwise.
  - `credits_increase`: credits.availableCount increased vs stored.
  - No events at all for a window with no stored baseline (first sight) — just record baseline.
  - Hidden providers produce no events.
  - Event keys: deterministic strings e.g. `early:<windowID>:<resetsAt|epoch>`; check/insert in `events` table atomically.
- `poller.go`: ticker at settings interval (re-read after each tick), fetch → normalize → persist snapshot → run events → send APNs alerts to all devices + widget-refresh push when snapshot changed. On fetch failure: mark stale, keep old snapshot, health reflects it.
- `main.go`: wire config, store, poller, API server; graceful shutdown.
- Tests: every crossing direction (below→above early, above→below is no-op, danger crossing, both at once), reset cycle fires once (dedup across restarts — reuse store), tibo boundary cases (50pp drop exactly, 49pp no, 20%→5% yes, 19%→5% no, 20%→6% no), credits increase/decrease/same, baseline suppression after fresh DB, hidden provider suppression, stale fallback, duplicate poll produces no repeat events. APNs client: JWT shape test (parse header/claims), no-op mode.

Commit when green.

## Task 4: Deployment artifacts

- `server/deploy/usagewidget.service` — systemd unit (After=network, Environment file `/etc/usagewidget/env`, ExecStart, Restart=on-failure, DynamicUser or dedicated user).
- `server/deploy/README.md` — build (`GOOS=linux GOARCH=amd64 go build`), install steps, Tailscale Serve command (`tailscale serve --bg --set-path /usagewidget http://127.0.0.1:8377` or https proxy), env file template (token, APNs vars).
- Top-level `README.md` — project overview, both components, setup.
- `HUMANS.md` — checklist of human-only steps (Apple Developer APNs key creation, provisioning, server deployment, Tailscale Serve enablement).

No tests. Commit.

## Task 5: iOS project scaffold + shared core

Create `ios/` with XcodeGen `project.yml`:

- Targets: `UsageWidget` (iOS app, SwiftUI, deployment target 27.0), `UsageWidgetWidget` (widget extension, deployment 27.0), `UsageWidgetCoreTests` (unit tests). App Group + push notification capabilities in entitlements files (`aps-environment: development`, app group per Global Constraints). Signing: automatic, team ID placeholder via `DEVELOPMENT_TEAM` setting read from environment or left blank.
- Shared source folder `ios/Sources/Core/` compiled into both app and widget targets:
  - `Models.swift`: Codable mirror of server Snapshot/Provider/Window/Credits (dates ISO8601, tolerate unknown fields).
  - `APIClient.swift`: async URLSession client — `fetchSnapshot()`, `fetchHealth()`, `updateSettings(_:)`, `registerDevice(_:)`, `deleteDevice(_:)`, bearer auth header, base URL injectable.
  - `Keychain.swift`: store/load server URL + bearer token (kSecClassGenericPassword, accessible after first unlock, shared access group not required — app only; URL+token also mirrored to App Group defaults? NO — token stays in Keychain with access group shared to widget: use keychain access group = app group so the widget can read it).
  - `SnapshotStore.swift`: App Group container — persist latest decoded snapshot JSON + display prefs (order, hidden) + last refresh date; load for widget.
- Tests in `UsageWidgetCoreTests`: decoding fixtures (full snapshot, null resets, provider error, unknown fields), SnapshotStore round-trip (use temp defaults suite), APIClient request building (URLProtocol stub: auth header, paths, method).
- Verify: `xcodegen generate` then `xcodebuild build` for app scheme (generic iOS destination, CODE_SIGNING_ALLOWED=NO) and test target on simulator if available; otherwise build-only is acceptable — report which.

Commit when green.

## Task 6: iOS app UI

In `ios/Sources/App/`:

- `UsageWidgetApp.swift`: app entry, AppDelegate for APNs registration — request notification permission, register for remote notifications, forward device token to server via APIClient; also register WidgetKit push token (iOS 27 `WidgetCenter` push token API) and send as widgetToken.
- `DashboardView.swift`: list of all providers from latest snapshot — every window as labeled progress bar (used%), remaining %, reset time (relative), provider error state, stale banner with data age, pull-to-refresh.
- `SetupView.swift`: server URL text field (Tailscale HTTPS), token field, "Test connection" button hitting /v1/health with result display; saves to Keychain on success. Shown first-run (no stored URL) and reachable from settings.
- `SettingsView.swift`: polling interval picker (1/5/15/30/60), early/danger threshold steppers, notifications toggle — all PUT to server; provider list with drag-to-reorder + show/hide toggles (EditMode), persisted server-side via providerOrder/hiddenProviders and locally to SnapshotStore for the widget; notification permission status + request button; server health section (health fields, last refresh, stale).
- `AppModel.swift`: observable state — snapshot, health, settings, refresh(), applying visibility/order.
- After any settings/order change or successful refresh: `WidgetCenter.shared.reloadAllTimelines()`.
- Keep views straightforward; system styling, no custom design system.
- Build must pass (same xcodebuild invocation as Task 5). No new unit tests required beyond compiling; AppModel ordering/visibility logic gets a small test in CoreTests if it lands in Core.

Commit when green.

## Task 7: WidgetKit extension

In `ios/Sources/Widget/`:

- `UsageWidgetBundle.swift`, `ProviderWidget.swift`: systemLarge widget.
- TimelineProvider: on timeline request, attempt live `APIClient.fetchSnapshot()` (short timeout) — on success cache to SnapshotStore; on failure render cached snapshot marked with its age. Entry refresh policy `.after(now + pollInterval)`.
- iOS 27 WidgetKit push: adopt the WidgetKit push-token API so server pushes trigger reloads; token surfaced to app via App Group for upload (and/or `WidgetPushHandler` per iOS 27 API — implementer verifies exact API via Apple docs and uses it).
- View: up to 4 provider rows (visible providers in user order), each row: provider name, primary progress bar, secondary progress bar if present, remaining % of primary, nearest resetsAt (relative). If more visible providers than fit: last row shows "+N more". Footer: data age ("Updated 3m ago"), stale indicator.
- Accessibility: rows have sensible labels; supports dynamic type without truncating critical numbers (test smallest reasonable sizes).
- Build must pass for widget target.

Commit when green.

## Task 8: Docs polish + final verification

- Ensure README covers: architecture diagram (text), server API table, iOS setup, demo flow.
- HUMANS.md up to date.
- Run full Go test suite + swift build one more time; fix anything broken.

Commit.
