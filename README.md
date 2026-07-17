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

Deploy docs: [`server/deploy/README.md`](server/deploy/README.md).

## iOS quick start

```bash
cd ios
xcodegen generate
open UsageWidget.xcodeproj
```

In the app: enter the Tailscale HTTPS base URL and the same bearer token, then
Test connection. Add the **UsageWidget** large widget from the Home Screen
gallery.

## Configuration defaults

- Poll intervals: 1 / 5 / 15 / 30 / 60 minutes (server-side)
- Early alert: 10% used · Danger alert: 10% remaining
- Initial provider order: codex → claude → grok; newly discovered providers are
  appended and hidden until enabled
- App Group: `group.systems.edmundlim.usagewidget`
- Bundle ID: `systems.edmundlim.UsageWidget` (widget: `.widget`)

## Human setup checklist

See [`HUMANS.md`](HUMANS.md) for Apple Developer / APNs / edServe / Tailscale steps.
