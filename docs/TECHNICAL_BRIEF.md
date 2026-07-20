# UsageWidget technical brief

This is a factual study sheet for understanding and demonstrating the app. It
is deliberately a set of key points, not a recording script or a Devpost
write-up.

## The product in one sentence

UsageWidget turns usage-limit data from CodexBar into a private, self-hosted
iPhone dashboard, Home Screen widget, forecasts, and timely capacity/reset
notifications across multiple AI providers.

## Problem and user

- AI tools expose several rate windows with different reset times; checking
  them individually interrupts work and makes it hard to plan capacity.
- The primary user is a person who actively uses Codex, Claude, Grok, or other
  CodexBar-supported providers and wants one glanceable view on their phone.
- The value is not merely displaying a percentage. UsageWidget preserves the
  last known state, explains freshness, predicts likely exhaustion, and alerts
  on meaningful changes without moving provider credentials onto the phone.

## System shape

```text
CodexBar sessions
      │
      ▼
isolated CLI collector ── Unix socket ──► Go daemon ──► SQLite
                                            │  │
                              Tailscale HTTPS  └── APNs alerts/widget pushes
                                            │
                                            ▼
                                  iOS app + WidgetKit
```

- The production deployment intentionally separates two Linux processes.
- The collector runs as the unprivileged account that owns the CodexBar login
  sessions. It executes one fixed usage command, serializes requests, validates
  JSON, bounds output size, and exposes only `GET /usage` over a restricted Unix
  socket.
- The main Go daemon has no need to read that account's home directory. It
  polls the collector, normalizes the response, stores state and history in
  SQLite, evaluates events, serves the phone API, and sends APNs messages.
- The iOS app and widget reach the daemon through Tailscale HTTPS. The daemon
  stays bound to loopback rather than being exposed directly to the public
  internet.
- The daemon also builds natively for macOS and Windows. Desktop mode uses an
  exact CodexBar CLI path or a private CodexBar HTTP endpoint and stores SQLite
  data in the signed-in user's application-data directory. It trades Linux's
  account isolation and systemd supervision for easier personal-machine setup.

## Data flow, step by step

1. The poller asks CodexBar for all currently enabled providers.
2. The normalizer converts provider-specific shapes into one model: provider,
   usage windows, percent used/remaining, reset time, optional credits, error,
   and stale state.
3. Unknown providers and extra windows are data-driven instead of rejected by
   a hard-coded provider list.
4. The server preserves a previously good provider value when only that
   provider fails, marks it stale, and avoids replacing useful data with an
   empty error state.
5. SQLite stores the latest snapshot, bounded poll outcomes, window baselines,
   forecast samples, notification deduplication keys, settings, and registered
   device tokens.
6. The event engine compares the new normalized state with the saved baseline.
   It can emit threshold, scheduled-reset, surprise-reset, or reset-credit
   events. First-seen data establishes a baseline and does not notify.
7. If visible data changed, the server can send a WidgetKit refresh push. If an
   alert rule fires, it sends an APNs alert subject to notification settings and
   quiet hours.
8. The app fetches snapshot, health, and settings concurrently, caches display
   state for failure tolerance, and asks WidgetKit to reload. The widget also
   has a short network fetch path and falls back to cached stale data.

## Important implementation choices

### Provider-agnostic normalization

- Codex, Claude, Grok, and newly discovered providers share the same internal
  `Provider` and `Window` model.
- Primary, secondary, tertiary, and additional named rate windows can all be
  represented.
- Provider ordering and visibility are settings. Hiding a provider removes it
  from phone-facing snapshots and suppresses its alerts.

### Alerts that track transitions, not snapshots

- Early and danger notifications are based on threshold crossings, so the same
  state does not repeatedly alert on every poll.
- Scheduled resets and materially early usage drops are treated separately.
- Event keys include the relevant reset cycle, which prevents duplicates while
  allowing a new alert in the next cycle.
- Rules inherit from global to provider to individual window. Danger reminders
  can be disabled or repeated hourly, every three hours, or every six hours.
- Quiet hours make automatic alerts passive; the explicit readiness test stays
  audible because the user initiated it.

### Forecasting

- Forecasts use recent samples from the current reset cycle rather than all
  historical data.
- A forecast appears only after at least three increasing samples span at least
  30 minutes and one percentage point. This avoids presenting a confident
  estimate from noise or an idle window.
- The server estimates the burn rate and whether usage will reach 100% before
  reset. Forecasts are removed when data is stale.

### Failure and freshness behavior

- A failed poll does not silently look current. Health records last poll, last
  success, failure count, next poll, duration, and a bounded redacted error.
- Partial upstream failure is isolated per provider; good providers remain
  fresh while failed providers can show their last known values as stale.
- The iOS interface distinguishes collecting, current, stale, and unavailable
  states. This is part of the product experience, not just diagnostics.
- The Release Readiness screen checks database, polling, collector freshness,
  snapshot freshness, APNs configuration, device registration, and alert/widget
  tokens. A targeted test reports APNs acceptance without falsely claiming that
  iOS displayed the notification.

## Security and privacy model

- Provider credentials remain on the machine running CodexBar and never move
  to the iPhone. Linux keeps them in the isolated collector account; desktop
  mode runs the daemon as the trusted signed-in user.
- The phone API uses a bearer token and exposes normalized display data, not raw
  CodexBar payloads.
- The app and widget share the bearer token through a Keychain access group.
  App Group storage contains cached display data and preferences, not the
  credential.
- The service is designed for one operator on a private network. Tailscale Serve
  provides the HTTPS path; the raw Go port should remain loopback-only.
- Setup QR codes are credentials. They must never appear in the demo recording,
  screenshots, logs, or repository.

## Main code areas

- `server/collector.go`: restricted CodexBar CLI bridge.
- `server/normalize.go`: upstream JSON to stable provider/window model.
- `server/poller.go`: polling lifecycle, partial-failure preservation, event
  processing, and notification/widget dispatch.
- `server/events.go`: transition detection and event deduplication.
- `server/forecast.go`: usage-rate regression and exhaustion forecast.
- `server/store.go`: SQLite migrations and persistent state.
- `server/api.go`: authenticated API, settings, device registration, health,
  and readiness checks.
- `server/apns.go`: APNs token auth, alert payloads, and WidgetKit pushes.
- `ios/Sources/App/AppModel.swift`: app state, concurrent refresh, cached
  fallback, settings, token registration, and readiness actions.
- `ios/Sources/App/DashboardView.swift`: capacity and freshness experience.
- `ios/Sources/Widget/ProviderWidget.swift`: Home Screen rendering, network
  refresh, cached fallback, and widget push token handling.
- `server-install.sh`, `server/deploy/start-*`, and `cli/usagewidget`:
  installation and operator workflow across Linux, macOS, and Windows.

## Demo beats to understand

These are independent beats to choose from, not a prescribed order or script.

- The glance: show multiple providers and rate windows in one place, including
  remaining capacity and reset horizon.
- The Home Screen value: show that the information is useful without opening
  each AI service or even opening UsageWidget.
- The planning value: point to a forecast that says whether capacity is likely
  to run out before reset.
- The resilience value: point out the explicit freshness state and cached
  fallback rather than pretending a failed collection is live data.
- The control value: show provider ordering/visibility and an alert override or
  quiet-hours setting.
- The end-to-end proof: trigger the readiness test or a safe demo notification,
  then show the server/device checks.
- The trust value: explain the collector/daemon separation and why credentials
  never need to be placed in the phone app.

## Judge-aligned key points

- **Technological implementation:** a working cross-platform system spanning
  SwiftUI/WidgetKit, Go, SQLite, APNs, a Unix-socket privilege boundary,
  Tailscale, installers, operational tooling, and automated tests.
- **Design:** the dashboard, widget, setup flow, settings, alert rules, cached
  states, and readiness diagnostics form a coherent product rather than a
  single API proof of concept.
- **Potential impact:** it reduces context switching and helps heavy AI-tool
  users decide when to work, switch providers, or wait for a reset.
- **Quality of idea:** it treats AI usage capacity as an ambient personal signal
  with forecasts and transition-aware alerts, not merely another usage page.

## Codex and GPT-5.6: keep the claim precise

- UsageWidget does **not** call GPT-5.6 at runtime based on the current code.
  CodexBar is a usage-data source; it is not an inference dependency.
- The Build Week requirement concerns how the project was built with Codex and
  GPT-5.6. Describe only work you personally verified in the relevant Codex
  task: for example planning, cross-language implementation, test generation,
  debugging, security review, or release hardening.
- Be ready to name one concrete piece built with GPT-5.6, the constraint or
  tradeoff involved, what you changed or rejected, and how you verified the
  result. The `/feedback` Session ID should point to the task containing most of
  that core work.
- Keep authorship clear: Codex accelerated implementation and review; you chose
  the product problem, accepted or changed the design decisions, tested the
  physical system, and own the final behavior.

## Current verification snapshot

Verified locally on 20 July 2026 SGT:

- Go server tests pass.
- Native macOS and Windows daemon cross-builds pass.
- Installer tests pass.
- The unsigned iOS generic-device build succeeds.

Still human-dependent: signed physical-device behavior, production APNs,
Tailscale deployment, fresh-install onboarding, GitHub Actions after correcting
its archived-demo references, and the final public video and Devpost links.
