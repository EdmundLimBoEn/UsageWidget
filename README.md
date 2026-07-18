# UsageWidget (CodexBar)

Polished iOS 27 hackathon app: a large Home Screen widget plus companion app that
shows live Codex / Claude / Grok (and any other) usage from CodexBar on edServe
over Tailscale HTTPS, with APNs alerts for thresholds, scheduled resets, surprise
“Tibo” resets, and reset-credit increases.

```
┌─────────────┐   Tailscale HTTPS    ┌──────────────────┐  Unix socket  ┌──────────────┐
│  iPhone app │ ───────────────────► │  usagewidgetd    │ ────────────► │ CLI collector│
│  + widget   │ ◄── APNs / WidgetKit │  (Go + SQLite)   │               │  (CodexBar)  │
└─────────────┘                      └──────────────────┘               └──────────────┘
```

## Components

| Path | Role |
|------|------|
| `server/` | Go service: poll CodexBar, normalize providers, SQLite, events, APNs, HTTP API |
| `ios/` | SwiftUI app + WidgetKit extension (XcodeGen) |
| `docs/plans/` | Implementation plan |

Provider lists are **data-driven** from whatever CodexBar returns. No provider
credentials ever leave the Linux host.

The app and widget share the server bearer token through a Keychain access
group. App Group defaults contain cached normalized snapshots and preferences,
never the active token. Raw CodexBar payloads are not returned by the phone API.

## Server quick start

```bash
cd server
export USAGEWIDGET_TOKEN="$(openssl rand -hex 32)"
export CODEXBAR_URL=http://127.0.0.1:8765/usage # local-development override
go run ./cmd/usagewidgetd
# listens on :8377 by default
```

All sources honor CodexBar's provider toggles, so disabled providers do not
become permanent error rows:

- Production default: read fresh CLI output from the isolated collector at
  `COLLECTOR_SOCKET` (`/run/usagewidget/codexbar.sock`).
- Development override: set `CODEXBAR_URL` to an existing CodexBar serve URL.
- Legacy override: set `CODEXBAR_CMD="codexbar usage --json"` only when the
  daemon account itself owns the provider sessions.

Providers hidden in the app's Settings are also stripped from `/v1/snapshot`
server-side, so the phone never receives them.

Deploy docs: [`server/deploy/README.md`](server/deploy/README.md).
Day-to-day redeploy: `./server/deploy/redeploy.sh` (or agent skill `/usagewidget-deploy`).

### Mac CLI

```bash
# installed to ~/.local/bin/usagewidget (repo: cli/usagewidget)
usagewidget env sync    # pull bearer token from edServe → ~/.config/usagewidget/env
usagewidget health
usagewidget poll        # force server poll
usagewidget demo        # synthetic test alert
usagewidget demo-provider on  # enable synthetic provider and poll
usagewidget demo-provider off # disable it and poll
usagewidget deploy      # rebuild + restart on edServe
usagewidget logs -f
```

Config lives at `~/.config/usagewidget/env` (mode 600, not in git).
Deploys also install the CLI at `/usr/local/bin/usagewidget` on edServe, where
it automatically reads `/etc/usagewidget/env` and uses the local daemon.

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

The server-backed **Demo provider** toggle in iOS controls whether normal and
scheduled edServe polls inject the persisted synthetic provider. The same switch
is available through `usagewidget demo-provider on|off|poll`.

### Demo flow

1. The collector runs `CodexBarCLI usage --json` as the account that owns the provider sessions.
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
