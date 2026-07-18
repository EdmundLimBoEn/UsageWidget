# usagewidgetd — deploy on edServe

Companion service that polls CodexBar on localhost, stores snapshots, evaluates
usage events, and pushes APNs alerts + WidgetKit refreshes to the iPhone app.

| | |
|--|--|
| Host | `edserve` · Tailscale `100.83.252.53` · `edserve.tail125275.ts.net` |
| SSH | `root@100.83.252.53` (BatchMode pubkey) |
| Binary | `/usr/local/bin/usagewidgetd` |
| Unit | `usagewidget.service` |
| Env | `/etc/usagewidget/env` (mode 600, never commit) |
| Data | `/var/lib/usagewidget/usagewidget.db` |
| Listen | `127.0.0.1:8377` (Tailscale Serve path `/usagewidget`) |
| Agent skill | `.grok/skills/usagewidget-deploy/SKILL.md` (`/usagewidget-deploy`) |
| CLI | `cli/usagewidget` → `~/.local/bin/usagewidget` locally and `/usr/local/bin/usagewidget` on edServe |
| Mac config | `~/.config/usagewidget/env` (token + URL; mode 600) |

## Mac CLI

```bash
usagewidget env sync    # write ~/.config/usagewidget/env from edServe token
usagewidget health
usagewidget poll
usagewidget demo        # POST /v1/demo/alert
usagewidget demo-provider on   # enable synthetic provider + poll on edServe
usagewidget demo-provider off  # disable synthetic provider + poll on edServe
usagewidget deploy      # same as redeploy.sh
usagewidget logs -f
usagewidget help
```

The redeploy script also installs the same command on edServe. There it reads
`/etc/usagewidget/env` automatically and connects directly to
`http://127.0.0.1:8377`, so `usagewidget demo-provider on` works without a
per-user config file.

## Redeploy (day-to-day)

edServe has **no Go toolchain**. Always cross-compile on the Mac, then install
and restart. Prefer this path over a full reinstall.

```bash
# from repo root
./server/deploy/redeploy.sh
```

Manual equivalent:

```bash
cd server
GOOS=linux GOARCH=amd64 go build -o /tmp/usagewidgetd ./cmd/usagewidgetd
scp /tmp/usagewidgetd root@100.83.252.53:/tmp/usagewidgetd.new
ssh root@100.83.252.53 '
  install -o root -g root -m 755 /tmp/usagewidgetd.new /usr/local/bin/usagewidgetd
  rm -f /tmp/usagewidgetd.new
  systemctl restart usagewidget
  systemctl is-active usagewidget
'
```

Verify:

```bash
ssh root@100.83.252.53 '
  set -a; source /etc/usagewidget/env; set +a
  curl -sS -H "Authorization: Bearer $USAGEWIDGET_TOKEN" http://127.0.0.1:8377/v1/health
  echo
  curl -sS -X POST -H "Authorization: Bearer $USAGEWIDGET_TOKEN" http://127.0.0.1:8377/v1/poll
  echo
'
```

Public (tailnet) URL:

```text
https://edserve.tail125275.ts.net/usagewidget/v1/health
```

Override deploy host: `USAGEWIDGET_DEPLOY_HOST=root@… ./server/deploy/redeploy.sh`.

## Build

From the repo root (or `server/`):

```bash
cd server
GOOS=linux GOARCH=amd64 go build -o usagewidgetd ./cmd/usagewidgetd
GOOS=linux GOARCH=amd64 go build -o usagewidget-collector ./cmd/usagewidget-collector
```

## Install (first time only)

```bash
sudo useradd --system --home /var/lib/usagewidget --shell /usr/sbin/nologin usagewidget
sudo mkdir -p /var/lib/usagewidget /etc/usagewidget
sudo cp usagewidgetd /usr/local/bin/usagewidgetd
sudo cp usagewidget-collector /usr/local/bin/usagewidget-collector
sudo chown root:root /usr/local/bin/usagewidgetd
sudo chmod 755 /usr/local/bin/usagewidgetd
sudo cp deploy/usagewidget.service /etc/systemd/system/usagewidget.service
sudo cp deploy/usagewidget-collector.service /etc/systemd/system/usagewidget-collector.service
sudo chown usagewidget:usagewidget /var/lib/usagewidget
```

## Environment file

Create `/etc/usagewidget/env` (mode `600`, owned by root):

```bash
USAGEWIDGET_TOKEN=replace-with-long-random-token
COLLECTOR_SOCKET=/run/usagewidget/codexbar.sock
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

The collector runs as the login account that owns the working CodexBar session.
Create `/etc/usagewidget/collector.env` (mode `600`) when `codexbar` is not on
the unit's configured PATH:

```bash
CODEXBAR_BIN=/home/linuxbrew/.linuxbrew/Cellar/codexbar/0.43.0/libexec/CodexBarCLI
COLLECTOR_SOCKET=/run/usagewidget/codexbar.sock
```

The sidecar exposes only `GET /usage` on that Unix socket. It accepts no command
or provider arguments from `usagewidgetd`.

## Enable systemd

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now usagewidget-collector usagewidget
sudo systemctl status usagewidget-collector
sudo systemctl status usagewidget
```

Logs:

```bash
journalctl -u usagewidget-collector -u usagewidget -f
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
