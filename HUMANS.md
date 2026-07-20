# Human-only setup

These steps require an account owner, physical device, private-network
administrator, or release maintainer. They cannot be safely automated or
committed to the repository.

## OpenAI Build Week submission

Checked against the live Devpost requirements and announcements on 20 July
2026 (Singapore time). The deadline is **Tuesday, 21 July at 5:00 PM Pacific
Time**, which is **Wednesday, 22 July at 8:00 AM Singapore time**. The current
Devpost project is a draft with a tagline, but no description or video URL.

Use [the technical brief](docs/TECHNICAL_BRIEF.md) for factual talking points
before recording. It is intentionally not a script or submission write-up.

### Submission blockers

- [ ] Finish and save the Devpost project description in your own voice. Cover
  the real problem, intended user, what works, and how the system works; do not
  paste AI-generated submission prose.
- [ ] Choose one category. **Apps for Your Life** is the clearest fit for a
  personal usage-capacity companion; use **Developer Tools** only if the demo
  centers on installation, operations, and developer workflow.
- [ ] Retrieve the `/feedback` Session ID from the Codex task where most core
  functionality was built and add it to the required Devpost field.
- [ ] Add the public repository URL:
  `https://github.com/EdmundLimBoEn/UsageWidget`.
- [ ] Add a relevant open-source `LICENSE` file. Devpost explicitly requires a
  relevant license when the submitted repository is public.
- [x] Add a short, factual README section explaining where Codex accelerated
  the workflow, which important decisions you made, and exactly which part used
  GPT-5.6. Do not imply that the running app calls GPT-5.6: it consumes
  CodexBar usage data; Codex and GPT-5.6 were build tools.
- [x] Fix `.github/workflows/release.yml` so its shell-syntax step no longer
  names archived, absent demo scripts.
- [ ] Confirm the updated Linux, macOS, and Windows GitHub Actions checks are
  green.
- [ ] Add every teammate to the Devpost project and have each person accept the
  invitation before the deadline, or confirm this is an individual submission.
- [ ] Fill the required Devpost fields: submitter type, country of residence,
  category, repository URL, `/feedback` Session ID, and a truthful **Built
  with** technology list.

### Demo and recording

- [ ] Prepare a stable physical-device demo with the server already collecting
  fresh data. Keep a safe fallback recording in case networking, CodexBar, APNs,
  or WidgetKit timing misbehaves.
- [ ] Keep the YouTube video under three minutes. It may be public or unlisted,
  but it must be viewable without signing in; verify the final URL in a private
  browser window.
- [ ] Include spoken audio covering all three required points: what was built,
  how Codex was used, and how GPT-5.6 was used. A music-only screencast is not
  eligible.
- [ ] Show the product working, not just slides: app capacity view, Home Screen
  widget, multiple providers/windows, reset timing or forecast, and one
  notification/readiness path.
- [ ] Avoid exposing the setup QR, bearer token, APNs/device tokens, private
  hostname, raw provider payloads, or personal account data. Use cropped,
  redacted, or purpose-made demo data where necessary.
- [ ] Upload the final video, add its URL to Devpost, and re-watch the uploaded
  version for legibility, audio, and the under-three-minute limit.

### Final verification and submission

- [x] `go test ./...` passes in `server/` (verified 20 July 2026 SGT).
- [x] `bash tests/installer_test.sh` passes (verified 20 July 2026 SGT).
- [x] The unsigned generic-device Xcode build succeeds (verified 20 July 2026
  SGT).
- [ ] Run the three verification commands again from the exact commit linked in
  Devpost and record the commit SHA for yourself.
- [ ] Follow the physical-device checks below, including fresh install, widget,
  background refresh, audible alert, quiet-hours behavior, and readiness test.
- [ ] Test the README setup path from a clean checkout. Ensure a judge can
  understand supported platforms and run the server and iOS project without
  relying on undocumented local state.
- [ ] Add a project thumbnail and any useful screenshots on Devpost.
- [ ] Review the submission against the four judging criteria: technological
  implementation, coherent product design, specific real-world impact, and
  novelty.
- [ ] Submit before the deadline, then reopen the Devpost project and confirm it
  says **Submitted** rather than **Draft**. Check every link one final time.

## Before publishing the repository

- [ ] Inspect every reachable commit and tag for tokens, APNs keys, databases,
  device tokens, private hostnames/IPs, and personal operational notes.
- [ ] Rotate any credential that ever entered Git before rewriting or replacing
  the affected history.
- [ ] Enable secret scanning, push protection, private vulnerability reporting,
  and branch protection on the hosting provider.
- [ ] Keep local environment files, signing material, backups, generated release
  bundles, and test evidence out of Git.

## Apple signing and APNs

- [ ] Register unique app, widget, App Group, and shared Keychain identifiers;
  update `ios/project.yml`, both entitlements files, and `AppConstants` together.
- [ ] The project is configured for team `DUU8J39BA7`. Release maintainers need
  the matching distribution certificate and the two App Store profiles named in
  `ios/ExportOptions.plist`; private keys and provisioning profiles stay local.
- [ ] Create a dedicated APNs authentication key. Store its `.p8` outside the
  repository with restrictive permissions and do not reuse an App Store Connect
  API key.
- [ ] Put `APNS_KEY_PATH`, `APNS_KEY_ID`, `APNS_TEAM_ID`, `APNS_BUNDLE_ID`, and
  the correct `APNS_ENV` only in `/etc/usagewidget/env`.
- [ ] On a physical iOS 26+ device, grant camera and notification permission,
  scan a freshly generated setup QR, and verify app launch, widget loading,
  background refresh, an audible automatic alert, a quiet-hours passive alert,
  and an audible readiness test.
- [ ] Before TestFlight or App Store distribution, complete privacy disclosures,
  support details, screenshots, production APNs validation, and a clean-install
  onboarding test.

## Server and Tailscale

- [ ] Log in to CodexBar as the unprivileged account selected for the collector;
  verify `CodexBarCLI config validate` and `CodexBarCLI usage --format json` as
  that exact account.
- [ ] Log Tailscale into the intended tailnet before installation and verify the
  server's MagicDNS name.
- [ ] Keep `LISTEN_ADDR=127.0.0.1:8377` and expose only the `/usagewidget` route
  through Tailscale Serve. Never publish port 8377 directly.
- [ ] Use a unique random bearer token of at least 32 characters and keep
  `/etc/usagewidget/env`, `/etc/usagewidget/collector.env`, the SQLite database,
  backups, and APNs key restricted to their intended accounts.
- [ ] Run `sudo usagewidget-admin doctor`, force a poll, inspect both systemd
  services, and confirm logs and health responses contain no credentials or raw
  provider payloads.
- [ ] Store the Mac CLI configuration at `~/.config/usagewidget/env` with mode
  `600`; set `USAGEWIDGET_DEPLOY_HOST`, `USAGEWIDGET_URL`, and
  `USAGEWIDGET_REPO` only when source redeploys are needed.
- [ ] Back up before upgrades and test restoring a backup on a
  non-production host.
- [ ] For a desktop host, verify the generated config and SQLite data are
  readable only by the signed-in user, keep the foreground process running,
  and confirm the private Tailscale route survives a reboot if desired.
- [ ] For Windows, provide a private `CODEXBAR_URL` unless you have independently
  verified a compatible native CodexBar CLI build; upstream standalone archives
  currently target macOS and Linux.

## Landing page deployment (Codex)

- [ ] Deploy `site/` to Cloudflare Pages:
  `bunx wrangler pages deploy site --project-name usagewidget-landing`
- [ ] Attach the chosen new custom domain to the Pages project in the
  Cloudflare dashboard, then verify the page renders on that domain.

## Release publication

- [ ] Update `release-manifest.json` only after verifying the pinned CodexBar
  assets and SHA-256 values for both supported architectures.
- [ ] Run the full verification commands documented in `README.md` on a clean
  checkout.
- [ ] Create a `v*` tag, verify the GitHub Actions release workflow, and inspect
  both amd64 and arm64 archives plus their checksum files before announcing the
  release.
