# UsageWidget server deployment

`usagewidgetd` polls CodexBar through an isolated local collector, stores
normalized snapshots and event state in SQLite, serves the private phone API,
and sends APNs alerts and WidgetKit refreshes.

Linux is the production, always-on deployment. The same daemon also ships as a
native macOS and Windows executable for personal desktop hosts.

## Supported Linux deployment

| Item | Value |
|------|-------|
| Operating systems | Ubuntu 22.04, Ubuntu 24.04, Debian 12 |
| Architectures | amd64, arm64 |
| Init system | systemd |
| Main listener | `127.0.0.1:8377` |
| Private route | Tailscale Serve path `/usagewidget` |
| Main service | `usagewidget.service` as user `usagewidget` |
| Collector | `usagewidget-collector.service` as the CodexBar login account |
| Configuration | `/etc/usagewidget/env` and `/etc/usagewidget/collector.env` |
| Data | `/var/lib/usagewidget/usagewidget.db` |
| Releases | `/opt/usagewidget/releases` |
| Server CLI | `/usr/local/bin/usagewidget` |
| Admin CLI | `/usr/local/bin/usagewidget-admin` |

The main API must remain private even though it uses bearer authentication.
Provider sessions remain in the collector account's home directory; the daemon
receives only fresh usage JSON over a group-restricted Unix socket.

## Prerequisites

Before installation:

1. Log Tailscale into the intended tailnet.
2. Create or select an unprivileged login account for CodexBar.
3. As that account, authenticate the desired providers and verify:

   ```bash
   CodexBarCLI config validate
   CodexBarCLI usage --format json
   ```

4. Ensure the installing account has root or sudo access.
5. Decide whether the first install should be dashboard-only or include APNs.

Supported hosts also need outbound HTTPS for dependency/release downloads and
APNs. The release installer verifies its host prerequisites; `server-setup.sh`
installs the normal host packages before invoking it.

## Recommended installation

Run the hosted bootstrap on the Linux server. It installs any missing standard
download tools through `apt`, selects the latest release for the host
architecture, verifies its checksum, and invokes the release installer. No
repository clone or local build is required:

```bash
curl -fsSL https://usagewidget.edmundlim.systems/install.sh | \
  sudo bash -s -- --collector-user YOUR_LOGIN
```

The download domain is not used as the phone API address. Unless you explicitly
pass a server URL override, the release installer discovers the host's private
Tailscale MagicDNS name, configures Tailscale Serve, and encodes that private
URL in the setup QR.

To install from an archive instead, download and extract the release bundle
matching the host architecture, then run inside it:

```bash
sudo ./server-install.sh install --collector-user YOUR_LOGIN
sudo usagewidget-admin doctor
sudo usagewidget-admin qr
```

Optional install arguments:

```text
--public-url https://host.example/usagewidget
--collector-user USER
--version VERSION
```

Without `--public-url`, the installer derives
`https://<tailscale-magicdns-name>/usagewidget`, then configures the Tailscale
Serve path. The public URL option must still point to a trusted private HTTPS
proxy; it does not authorize exposing port 8377 directly.

The install is rerunnable. It preserves an existing bearer token, environment
file, SQLite data, and APNs configuration, installs versioned binaries, updates
systemd units, starts both services, checks health, and prints the QR when
`qrencode` is present.

### Install from a development Mac

From the repository root:

```bash
./server-setup.sh
```

The script asks for the SSH user, host, and collector user, detects amd64 or
arm64, builds a checksummed local release, transfers it, installs host packages,
and invokes the same server-side installer. It supports root SSH or an account
with interactive sudo.

## Native macOS and Windows

Desktop bundles run `usagewidgetd` in the foreground as the signed-in user.
They use native SQLite files and require no systemd, sudo, WSL, Docker, or CGO.
Unlike the Linux service layout, desktop mode does not isolate provider-session
access in a second OS account.

### macOS

Extract the matching `darwin-amd64` or `darwin-arm64` release and run:

```bash
./start-server.sh
```

The launcher discovers `codexbar` or `CodexBarCLI` and creates
`~/Library/Application Support/UsageWidget/server.env` with mode restricted by
the user's umask. Override discovery with `CODEXBAR_BIN=/absolute/path` or set
`CODEXBAR_URL` before the first launch.

### Windows 10/11

Extract `windows-amd64` (or `windows-arm64` on Windows on Arm), open PowerShell
in that directory, and run:

```powershell
powershell -ExecutionPolicy Bypass -File .\start-server.ps1 `
  -CodexBarUrl http://PRIVATE-CODEXBAR-HOST:8765/usage
```

Configuration and data are created in `%LOCALAPPDATA%\UsageWidget`. A custom
compatible CLI executable can be used instead:

```powershell
.\start-server.ps1 -CodexBarBin "C:\Program Files\CodexBar\codexbar.exe"
```

Rerun the launcher with either source flag to update an existing configuration;
the two flags are mutually exclusive.

CodexBar's official standalone CLI releases currently target macOS and Linux,
so the URL form is the normal Windows setup. Keep that URL reachable only over
loopback or a private network.

### Connect a desktop host privately

Keep `LISTEN_ADDR=127.0.0.1:8377`. With Tailscale installed and signed in, map
the same private HTTPS route used by Linux:

```text
tailscale serve --bg --https=443 --set-path=/usagewidget http://127.0.0.1:8377
```

Use `https://<machine-magicdns-name>/usagewidget` and the generated token in the
iPhone app. The macOS token is in `server.env`; on Windows, inspect `server.json`
locally with `Get-Content "$env:LOCALAPPDATA\UsageWidget\server.json"`. Never
paste either private config into logs or issue reports.

## APNs configuration

An install without APNs is valid and reports dashboard-only mode. To enable
notifications on Linux, place a dedicated Apple `.p8` key on the server and add
all of these values to `/etc/usagewidget/env`:

```bash
APNS_KEY_PATH=/etc/usagewidget/AuthKey.p8
APNS_KEY_ID=XXXXXXXXXX
APNS_TEAM_ID=XXXXXXXXXX
APNS_BUNDLE_ID=systems.edmundlim.UsageWidget
APNS_ENV=sandbox
```

Desktop launchers accept the same keys in macOS `server.env` or Windows
`server.json`; use an absolute key path owned by the signed-in user.

Use `APNS_ENV=production` only for a distribution-signed app that uses the
production APNs environment. Keep the key and environment file restricted; do
not use an App Store Connect API key in place of a dedicated APNs key.

After changing APNs configuration:

```bash
sudo systemctl restart usagewidget
sudo usagewidget-admin doctor
```

The app must register on a physical device before the readiness endpoint can
report a usable APNs or WidgetKit token.

## Configuration reference

The installer owns `/etc/usagewidget/env`. Its core values are:

```bash
USAGEWIDGET_TOKEN=replace-with-at-least-32-random-characters
COLLECTOR_SOCKET=/run/usagewidget/codexbar.sock
DB_PATH=/var/lib/usagewidget/usagewidget.db
LISTEN_ADDR=127.0.0.1:8377
USAGEWIDGET_PUBLIC_URL=https://your-host.your-tailnet.ts.net/usagewidget
```

Server variables:

| Variable | Default | Purpose |
|----------|---------|---------|
| `USAGEWIDGET_TOKEN` | required | Main API bearer token, minimum 32 characters |
| `COLLECTOR_SOCKET` | `/run/usagewidget/codexbar.sock` | Production collector socket |
| `DB_PATH` | `./usagewidget.db` | SQLite path; installer sets the data-directory path |
| `LISTEN_ADDR` | `127.0.0.1:8377` | Main API listener |
| `CODEXBAR_URL` | unset | Development HTTP-source override |
| `CODEXBAR_BIN` | unset | Exact CodexBar CLI path; supports spaces and is the native macOS source |
| `CODEXBAR_CMD` | unset | Legacy command-source override |
| `APNS_*` | unset | APNs signing configuration; all required to enable push |

`/etc/usagewidget/collector.env` normally contains:

```bash
CODEXBAR_BIN=/absolute/path/to/CodexBarCLI
COLLECTOR_SOCKET=/run/usagewidget/codexbar.sock
```

Source precedence is `CODEXBAR_CMD`, `CODEXBAR_URL`, `CODEXBAR_BIN`, then the
Linux collector socket. Prefer `CODEXBAR_BIN` over the legacy command string on
desktop systems because it safely preserves paths containing spaces.

Do not point `CODEXBAR_CMD` at an account whose home must remain isolated from
the daemon. The sidecar is the production path and exposes only `GET /usage` on
its Unix socket.

## Tailscale Serve

The installer configures the equivalent of:

```bash
sudo tailscale serve --bg --https=443 --set-path=/usagewidget \
  http://127.0.0.1:8377
```

Use this base URL in the app:

```text
https://your-host.your-tailnet.ts.net/usagewidget
```

Verify from another authenticated tailnet device:

```bash
curl -fsS -H "Authorization: Bearer $USAGEWIDGET_TOKEN" \
  https://your-host.your-tailnet.ts.net/usagewidget/v1/health
```

Never bind the daemon to a public interface or proxy the collector socket.

## Lifecycle administration

The release installs `usagewidget-admin`, which accepts:

```bash
sudo usagewidget-admin doctor
sudo usagewidget-admin doctor --json
sudo usagewidget-admin update
sudo usagewidget-admin update --version VERSION
sudo usagewidget-admin backup
sudo usagewidget-admin backup --include-apns-key
sudo usagewidget-admin restore --file /var/lib/usagewidget/backups/ARCHIVE.tar.gz
sudo usagewidget-admin rotate-token
sudo usagewidget-admin qr
sudo usagewidget-admin uninstall
sudo usagewidget-admin uninstall --purge --yes
```

Important behavior:

- `update` downloads the matching GitHub release archive and SHA-256 file,
  verifies them, and reruns the preserving installer. Set
  `USAGEWIDGET_RELEASE_BASE_URL` only for a controlled release mirror.
- `backup` uses SQLite's online backup command and includes the bearer-token
  environment file. The APNs key is excluded unless explicitly requested.
- `restore` stops the main service, restores the database and saved environment,
  then checks health.
- `rotate-token` rolls back on failed health and invalidates every existing
  phone connection when successful.
- Plain `uninstall` removes services and binaries but preserves configuration
  and data. `--purge` permanently deletes the exact UsageWidget configuration,
  release, and data directories.

Backups are sensitive credentials. Move copies to encrypted storage and test a
restore away from production.

## Operations CLI

The same `usagewidget` command is installed on the server and can be linked from
`cli/usagewidget` on a Mac:

```bash
usagewidget env sync
usagewidget health
usagewidget snapshot
usagewidget settings
usagewidget poll
usagewidget deploy
usagewidget logs -f
usagewidget status
usagewidget ssh
```

Local configuration is `~/.config/usagewidget/env` with mode `600`. On the
server, the CLI automatically reads `/etc/usagewidget/env` and talks to
`http://127.0.0.1:8377`.

## Manual source deployment

Use [the top-level source redeploy runbook](../../deploy.md) for an existing
installation. A minimal build from the repository root is:

```bash
cd server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o usagewidgetd ./cmd/usagewidgetd
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o usagewidget-collector ./cmd/usagewidget-collector
```

Use `GOARCH=arm64` for an arm64 host. Prefer `server-setup.sh` or a release
bundle for first installation because those paths create accounts, directories,
environment files, systemd overrides, and the Tailscale route consistently.

## API summary

All main `/v1/*` routes require
`Authorization: Bearer <USAGEWIDGET_TOKEN>`.

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/v1/health` | Redacted service and dependency health |
| `GET` | `/v1/snapshot` | Latest visible normalized snapshot and forecasts |
| `GET` / `PUT` | `/v1/settings` | Polling, display, and alert settings |
| `POST` | `/v1/devices` | Register or rotate APNs and WidgetKit tokens |
| `DELETE` | `/v1/devices/{deviceID}` | Remove a device |
| `POST` | `/v1/poll` | Force one collection cycle |
| `GET` | `/v1/readiness/{deviceID}` | Redacted server/device readiness |
| `POST` | `/v1/readiness/{deviceID}/test` | Targeted audible delivery test |

## Troubleshooting

```bash
sudo usagewidget-admin doctor --json
sudo systemctl status usagewidget usagewidget-collector --no-pager
sudo journalctl -u usagewidget -u usagewidget-collector -n 100 --no-pager
sudo -u YOUR_LOGIN CodexBarCLI config validate
sudo -u YOUR_LOGIN CodexBarCLI usage --format json
```

- Collector unhealthy: confirm the systemd `User=`, the account's CodexBar
  session, `CODEXBAR_BIN`, and socket group permissions.
- Database unhealthy: inspect ownership and free space under
  `/var/lib/usagewidget`; restore a verified backup if needed.
- APNs false: configure every `APNS_*` value and restart the main service.
- App unauthorized: generate a new QR or sync the current token; token rotation
  intentionally invalidates old clients.
- Widget not updating immediately: WidgetKit delivery and timelines are
  system-budgeted even after APNs accepts a refresh.
