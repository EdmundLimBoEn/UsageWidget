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

- [x] APNs Auth Key created (Key ID `YK47N2PQ54`); installed on edServe as `/etc/usagewidget/AuthKey_YK47N2PQ54.p8` (mode 640, group usagewidget). ASC API key is separate — do not reuse for push.
- [x] Team ID `DUU8J39BA7`; `APNS_ENV=sandbox` for Debug builds.
- [x] App IDs + App Group registered; device has registered APNs + widget tokens.
- [ ] Automatic signing / keychain unlock if device CodeSign fails.
- [ ] On phone: grant notification permission; confirm demo alert appears (Settings → Testing → Send test alert, or `usagewidget demo`).

## edServe / Linux

- [x] `usagewidgetd` installed + systemd enabled (`usagewidget.service`); redeploy with `./server/deploy/redeploy.sh` or `/usagewidget-deploy`.
- [x] `/etc/usagewidget/env` exists (token + CodexBar URL); mode 600 on host only.
- [x] Tailscale Serve: `https://edserve.tail125275.ts.net/usagewidget` → `127.0.0.1:8377`.
- [ ] Ensure CodexBar `serve` is running on localhost and `/usage` returns live provider data.
- [x] APNs `.p8` + `APNS_*` env on edServe (health shows `"apns":true`; live send succeeds).
- [ ] From the phone (or any tailnet device), hit `GET /v1/health` with the bearer token (or `usagewidget health`).

## Demo validation

- [ ] Dashboard shows live Codex / Claude / Grok (or whatever CodexBar returns).
- [ ] Widget renders up to four provider rows with data age.
- [ ] Settings → **Poll server now** forces an immediate CodexBar sample (health lastPoll updates).
- [ ] Settings → **Send test alert** delivers a synthetic APNs notification + widget refresh (plumbing check; does not mutate usage).
- [ ] Trigger a real usage threshold / reset on CodexBar and confirm real event APNs + widget refresh.
- [ ] Restart usagewidgetd; confirm no duplicate real alerts and baseline is not re-fired.
- [ ] Briefly lose Tailscale; app/widget still show last cached snapshot as stale.

## Hackathon portfolio closeout

- [ ] After automated gates pass, run the named physical-device matrix and retain redacted screenshots/logs under `docs/evidence/`.
- [ ] Capture and publish the required demo video only after dashboard browser QA and physical-device QA pass.
- [ ] After the hackathon, set `USAGEWIDGET_DEMO_ENABLED=false`, disable the Cloudflare demo hostname/Tunnel/Access policy, and remove temporary demo-only surfaces as planned.
