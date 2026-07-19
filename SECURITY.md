# Security policy

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Use GitHub private
vulnerability reporting for this repository, or contact the maintainer through
the address on their GitHub profile.

Include affected versions, reproduction steps, impact, and any suggested
mitigation. Never include a real bearer token, APNs key, device token, setup QR,
raw provider payload, private hostname, or database in the report.

## Trust model

UsageWidget is a single-operator service for a private network. The main API is
bearer authenticated but must still remain bound to `127.0.0.1:8377` and be
exposed only through an authenticated private-network proxy such as Tailscale
Serve. It is not designed to be placed directly on the public Internet.

The production collector runs as the unprivileged account that owns CodexBar's
provider sessions and exposes only `GET /usage` on a group-restricted Unix
socket. The main daemon must not run with that account's home-directory access.

The optional Lab Console is a separate, loopback-only listener. It has no bearer
token because it trusts an identity-aware reverse proxy to authenticate the
operator and supply the configured identity header. Enabling that listener
without the proxy makes the trust model invalid. Console routes are restricted
to synthetic demo state and explicit server-side device targets.

## Secrets and sensitive data

- Generate a unique `USAGEWIDGET_TOKEN` of at least 32 characters. Token
  rotation invalidates every connected phone; distribute the replacement only
  through a newly generated private QR or another secure channel.
- Keep `/etc/usagewidget/env`, `/etc/usagewidget/collector.env`, the APNs `.p8`,
  SQLite database, and backups readable only by their intended service or
  administrative accounts.
- Treat setup QRs as bearer credentials. Do not put them in screenshots, issue
  reports, logs, chat transcripts, or release assets.
- Backups include the database and bearer-token environment file. APNs material
  is excluded unless `--include-apns-key` is explicitly used.
- The phone-facing snapshot removes raw upstream provider JSON and hidden
  providers. Health, readiness, and installer diagnostics must stay redacted.

## Releases and updates

Release bundles contain an internal checksum manifest and are accompanied by a
SHA-256 file. These detect corruption but are not a substitute for verifying
the GitHub repository, release tag, and publishing account. Review release
workflow output and the pinned CodexBar asset checksums before installing or
updating a sensitive host.

## Before making the repository public

Scanning only the current checkout is insufficient because Git hosts publish
reachable history and tags. Inspect all of them. If sensitive data was ever
committed, rotate the credential first, rewrite or replace the affected history,
and require collaborators to re-clone before changing repository visibility.
