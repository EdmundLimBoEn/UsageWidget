# Roadmap

## Pending

### Distribute the iOS client

**What:** Keep source builds as the current external path, then add TestFlight
and evaluate App Store distribution.

**Why:** Let users install the client without maintaining their own Xcode
signing setup.

**Before release:** Confirm production APNs, privacy disclosures, supported
onboarding and backend expectations, unique bundle identifiers, screenshots,
support contact details, and a clean-device installation test.

**Effort:** L
**Priority:** P2

## Completed

- Generic, rerunnable Linux installation for Ubuntu 22.04/24.04 and Debian 12
  on amd64 and arm64.
- Diagnostics, release updates, checksummed bundles, backup/restore, token
  rotation, setup QR generation, and preserving or purging uninstall paths.
- Interactive source installation, installer verification, and GitHub Actions
  release packaging.
- Shared app/widget Keychain access group with one-time migration from App Group
  defaults.
- Raw upstream provider payloads removed from phone-facing snapshot responses.
- CLI collector sidecar, passive collection health, bounded poll history, and
  widget delivery diagnostics.
- Per-provider/per-window alert rules, quiet hours, danger reminders, usage
  forecasts, QR onboarding, and device-readiness tests.
