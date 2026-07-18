# Human-only steps

Checklist items that cannot be fully automated from this repo.

## Repo / git

- [x] Re-auth GitHub CLI — done; pushes work.
- [x] Remote `EdmundLimBoEn/UsageWidget` exists and `origin` is pushed.

## Code signing (device builds)

Device `CodeSign` fails with `errSecInternalComponent` when the login keychain is locked or the Apple Development private key denies `codesign`. Simulator builds are fine (ad-hoc).

- [ ] Unlock login keychain: open **Keychain Access** → lock then unlock **login**, or sign out/in of the Mac session.
- [ ] When macOS prompts “codesign wants to access key …”, choose **Always Allow**.
- [ ] Optional one-liner in Terminal.app (uses your Mac login password once):
  `security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$PASSWORD" login.keychain-db`
- [ ] Xcode → Settings → Accounts → manage certificates: ensure **Apple Development** exists for team `DUU8J39BA7`.
- [ ] Project already sets `DEVELOPMENT_TEAM = DUU8J39BA7` in `ios/project.yml` (re-run `xcodegen generate` after edits).

## Apple Developer / device

- [ ] Create an APNs Auth Key (.p8) in Apple Developer → Keys (Apple Push Notifications service).
- [ ] Note Key ID, Team ID; download the `.p8` once and store it only on edServe under `/etc/usagewidget/` (mode 600).
- [ ] Register App ID `systems.edmundlim.UsageWidget` with Push Notifications + App Groups capability.
- [ ] Register App ID `systems.edmundlim.UsageWidget.widget` with App Groups (+ Push if using WidgetKit push).
- [ ] Register App Group `group.systems.edmundlim.usagewidget` and enable it for the app + widget extension.
- [ ] Automatic signing should pick team `DUU8J39BA7`; rebuild after keychain unlock.
- [ ] Install the app on a physical iPhone with Tailscale connected to the same tailnet as edServe.
- [ ] Grant notification permission when prompted; confirm APNs device token registers via server logs / devices table.

## edServe / Linux

- [ ] Ensure CodexBar `serve` is running on localhost and `/usage` returns live provider data.
- [ ] Build and install `usagewidgetd` per `server/deploy/README.md`.
- [ ] Create `/etc/usagewidget/env` with a strong `USAGEWIDGET_TOKEN` and APNs vars.
- [ ] Install the `.p8` at `APNS_KEY_PATH`; never commit it.
- [ ] `systemctl enable --now usagewidget` and confirm `journalctl -u usagewidget` is healthy.
- [ ] Enable Tailscale Serve HTTPS for the service (MagicDNS hostname).
- [ ] From the phone (or any tailnet device), hit `GET /v1/health` with the bearer token.

## Demo validation

- [ ] Dashboard shows live Codex / Claude / Grok (or whatever CodexBar returns).
- [ ] Widget renders up to four provider rows with data age.
- [ ] Trigger a real usage threshold / reset on CodexBar and confirm APNs + widget refresh (no simulated demo mode).
- [ ] Restart usagewidgetd; confirm no duplicate alerts and baseline is not re-fired.
- [ ] Briefly lose Tailscale; app/widget still show last cached snapshot as stale.
