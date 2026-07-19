# Human-only setup

These steps require an account owner, physical device, private-network
administrator, or release maintainer. They cannot be safely automated or
committed to the repository.

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

## Linux server and Tailscale

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

## Release publication

- [ ] Update `release-manifest.json` only after verifying the pinned CodexBar
  assets and SHA-256 values for both supported architectures.
- [ ] Run the full verification commands documented in `README.md` on a clean
  checkout.
- [ ] Create a `v*` tag, verify the GitHub Actions release workflow, and inspect
  both amd64 and arm64 archives plus their checksum files before announcing the
  release.
