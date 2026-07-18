# usagewidgetd — deploy on edServe

Companion service that polls CodexBar on localhost, stores snapshots, evaluates
usage events, and pushes APNs alerts + WidgetKit refreshes to the iPhone app.

## Build

From the repo root (or `server/`):

```bash
cd server
GOOS=linux GOARCH=amd64 go build -o usagewidgetd ./cmd/usagewidgetd
```

On the host (if building natively):

```bash
go build -o /usr/local/bin/usagewidgetd ./cmd/usagewidgetd
```

## Install

```bash
sudo useradd --system --home /var/lib/usagewidget --shell /usr/sbin/nologin usagewidget
sudo mkdir -p /var/lib/usagewidget /etc/usagewidget
sudo cp usagewidgetd /usr/local/bin/usagewidgetd
sudo chown root:root /usr/local/bin/usagewidgetd
sudo chmod 755 /usr/local/bin/usagewidgetd
sudo cp deploy/usagewidget.service /etc/systemd/system/usagewidget.service
sudo chown usagewidget:usagewidget /var/lib/usagewidget
```

## Environment file

Create `/etc/usagewidget/env` (mode `600`, owned by root):

```bash
USAGEWIDGET_TOKEN=replace-with-long-random-token
# Default source: CodexBar serve (honors in-app provider toggles; don't
# append ?provider=all). Alternatively set CODEXBAR_CMD to shell out to the
# CLI per poll (slower): CODEXBAR_CMD=codexbar usage --json
CODEXBAR_URL=http://127.0.0.1:8765/usage
DB_PATH=/var/lib/usagewidget/usagewidget.db
LISTEN_ADDR=127.0.0.1:8377

# Optional — omit all APNs_* vars to run with log-only no-op pushes
APNS_KEY_PATH=/etc/usagewidget/AuthKey_XXXXXXXXXX.p8
APNS_KEY_ID=XXXXXXXXXX
APNS_TEAM_ID=XXXXXXXXXX
APNS_BUNDLE_ID=systems.edmundlim.UsageWidget
APNS_ENV=sandbox
```

Never commit this file or the `.p8` key.

## Enable systemd

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now usagewidget
sudo systemctl status usagewidget
```

Logs:

```bash
journalctl -u usagewidget -f
```

## Tailscale Serve (HTTPS)

Expose only via Tailscale MagicDNS — never open the port publicly:

```bash
# Proxy path /usagewidget → local service (adjust to your preferred path/host layout)
tailscale serve --bg --https=443 --set-path=/usagewidget http://127.0.0.1:8377
```

Or serve the whole host on HTTPS and reverse-proxy yourself. The iOS app should
use a URL like:

```text
https://edserve.<tailnet>.ts.net/usagewidget
```

(if path-based) or `https://edserve.<tailnet>.ts.net:8377` if you bind Serve
directly to the service. Prefer path-based Serve with the service on localhost only.

Verify:

```bash
curl -sS -H "Authorization: Bearer $USAGEWIDGET_TOKEN" \
  https://edserve.<tailnet>.ts.net/usagewidget/v1/health
```

## CodexBar

CodexBar's `serve` must already be listening on localhost (default
`http://127.0.0.1:8765/usage`). Do not expose CodexBar itself over Tailscale.

## API summary

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/v1/health` | Service readiness |
| GET | `/v1/snapshot` | Latest normalized providers |
| GET/PUT | `/v1/settings` | Poll interval, order, thresholds |
| POST | `/v1/devices` | Register/rotate APNs + widget tokens |
| DELETE | `/v1/devices/{deviceID}` | Invalidate device |
| POST | `/v1/poll` | Force one poll cycle now |
| POST | `/v1/demo/alert` | Synthetic test APNs + widget refresh |

All `/v1/*` routes require `Authorization: Bearer <USAGEWIDGET_TOKEN>`.
