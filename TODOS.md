# TODOS

## Release

### Secure credentials and minimize phone API data

**What:** Move app/widget bearer-token access to a shared Keychain group and remove raw provider payloads from phone API responses.

**Why:** Enforce secure credential storage and data minimization before any external user receives the app.

**Context:** The hackathon build accepts App Group `UserDefaults` token sharing and raw payload transport only because the endpoint is tailnet-restricted and the device is trusted. This work is a release gate for TestFlight, shared source builds, or public hosting. Start in `ios/Sources/Core/SnapshotStore.swift` and `server/normalize.go`/API response shaping.

**Effort:** M
**Priority:** P1
**Depends on:** Portfolio completion

### Package a generic self-hosted installation

**What:** Create a configurable install, diagnostics, update, and uninstall path for supported server hosts.

**Why:** Replace personal hostnames, repository paths, signing values, and deployment assumptions so another user can run UsageWidget without guided setup.

**Context:** Define supported platforms first, then document CodexBar, network, SQLite, APNs, and service prerequisites. Include health diagnostics and one external-user setup test before calling the release path supported.

**Effort:** L
**Priority:** P2
**Depends on:** Secure credentials and minimize phone API data

### Distribute the iOS client

**What:** Use source builds as the interim external path, then evaluate TestFlight and App Store distribution when the release prerequisites are ready.

**Why:** Let users install the client without maintaining their own Xcode signing setup.

**Context:** The Apple Developer account already exists, so submission can happen when useful. TestFlight/App Store still require privacy disclosures, supported onboarding, backend setup, credential handling, and support expectations. App Store review does not block early source-build testing.

**Effort:** L
**Priority:** P2
**Depends on:** Package a generic self-hosted installation; secure credentials and minimize phone API data
