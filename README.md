# UsageWidget (CodexBar)

Polished iOS 27 hackathon app: a large Home Screen widget plus companion app that
shows live Codex / Claude / Grok (and any other) usage from CodexBar on edServe
over Tailscale HTTPS, with APNs alerts for thresholds, scheduled resets, surprise
“Tibo” resets, and reset-credit increases.

```
┌─────────────┐   Tailscale HTTPS    ┌──────────────────┐   localhost   ┌──────────┐
│  iPhone app │ ───────────────────► │  usagewidgetd    │ ────────────► │ CodexBar │
│  + widget   │ ◄── APNs / WidgetKit │  (Go + SQLite)   │               │  /usage  │
└─────────────┘                      └──────────────────┘               └──────────┘
```

## Components

| Path | Role |
|------|------|
| `server/` | Go service: poll CodexBar, normalize providers, SQLite, events, APNs, HTTP API |
| `ios/` | SwiftUI app + WidgetKit extension (XcodeGen) |
| `docs/plans/` | Implementation plan |

Provider lists are **data-driven** from whatever CodexBar returns. No provider
credentials ever leave the Linux host.

## Server quick start

```bash
cd server
export USAGEWIDGET_TOKEN="$(openssl rand -hex 32)"
go run ./cmd/usagewidgetd
# listens on :8377 by default
```

Both sources honor CodexBar's in-app provider toggles, so only enabled
providers are returned — no error rows for disabled ones:

- Default: poll `CODEXBAR_URL` (`http://127.0.0.1:8765/usage` — fast, cached
  by CodexBar serve; do not append `?provider=all`).
- Alternative: set `CODEXBAR_CMD="codexbar usage --json"` to shell out to the
  CLI instead (slower — a full live fetch per poll).

Providers hidden in the app's Settings are also stripped from `/v1/snapshot`
server-side, so the phone never receives them.

Deploy docs: [`server/deploy/README.md`](server/deploy/README.md).

### API

All routes require `Authorization: Bearer <USAGEWIDGET_TOKEN>`.

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/v1/health` | Service, CodexBar, DB, polling, APNs readiness |
| `GET` | `/v1/snapshot` | Normalized providers, windows, freshness |
| `GET`/`PUT` | `/v1/settings` | Poll interval, order/visibility, thresholds |
| `POST` | `/v1/devices` | Register/rotate APNs + WidgetKit push tokens |
| `DELETE` | `/v1/devices/{deviceID}` | Invalidate a device |
| `POST` | `/v1/poll` | Force one poll cycle now (real CodexBar path) |
| `POST` | `/v1/demo/alert` | Send a synthetic test APNs + widget refresh |

### Demo flow

1. CodexBar serves `/usage` on localhost on edServe.
2. `usagewidgetd` polls on the configured interval, stores SQLite snapshots, fires
   APNs alerts + widget refreshes when data changes.
3. iPhone (Tailscale on) connects with HTTPS URL + bearer token.
4. Dashboard + large widget show live providers; hide/reorder in Settings.
5. For on-stage demos: Settings → **Poll server now** (or `POST /v1/poll`) to
   sample CodexBar immediately; **Send test alert** (or `POST /v1/demo/alert`) to
   fire a synthetic notification without waiting for a real threshold. Usage data
   itself is never faked — only the demo alert payload is synthetic.

## iOS quick start

```bash
cd ios
xcodegen generate
open UsageWidget.xcodeproj
```

Set your Development Team in Xcode (or `DEVELOPMENT_TEAM`). In the app: enter the
Tailscale HTTPS base URL and the same bearer token, then **Test connection**.
Add the **Usage** systemLarge widget from the Home Screen gallery.

Build without signing (CI / smoke):

```bash
xcodebuild -scheme UsageWidget -destination 'generic/platform=iOS' \
  CODE_SIGNING_ALLOWED=NO build
```

## Configuration defaults

- Poll intervals: 1 / 5 / 15 / 30 / 60 minutes (server-side)
- Early alert: 10% used · Danger alert: 10% remaining
- Initial provider order: codex → claude → grok; newly discovered providers are
  appended and hidden until enabled
- App Group: `group.systems.edmundlim.usagewidget`
- Bundle ID: `systems.edmundlim.UsageWidget` (widget: `.widget`)

WidgetKit push and timeline delivery are system-budgeted — a 1-minute poll
interval drives server sampling and push attempts, not a guaranteed one-minute
render on the Home Screen.

## Human setup checklist

See [`HUMANS.md`](HUMANS.md) for Apple Developer / APNs / edServe / Tailscale steps.
