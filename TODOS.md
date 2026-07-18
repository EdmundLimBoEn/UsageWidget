# TODOS

## Release

### Package a generic self-hosted installation

**What:** Create a configurable install, diagnostics, update, and uninstall path for supported server hosts.

**Why:** Replace personal hostnames, repository paths, signing values, and deployment assumptions so another user can run UsageWidget without guided setup.

**Context:** Define supported platforms first, then document CodexBar, network, SQLite, APNs, and service prerequisites. Include health diagnostics and one external-user setup test before calling the release path supported.

**Effort:** L
**Priority:** P2
**Depends on:** Portfolio completion

### Distribute the iOS client

**What:** Use source builds as the interim external path, then evaluate TestFlight and App Store distribution when the release prerequisites are ready.

**Why:** Let users install the client without maintaining their own Xcode signing setup.

**Context:** The Apple Developer account already exists, so submission can happen when useful. TestFlight/App Store still require privacy disclosures, supported onboarding, backend setup, credential handling, and support expectations. App Store review does not block early source-build testing.

**Effort:** L
**Priority:** P2
**Depends on:** Package a generic self-hosted installation

## Completed

- Shared app/widget Keychain access group with one-time migration from App Group defaults.
- Raw upstream provider payloads removed from phone-facing snapshot responses.
- CLI collector sidecar, passive collection health, bounded poll history, and widget delivery diagnostics.
