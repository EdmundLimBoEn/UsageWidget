---
name: usagewidget-deploy
description: >
  Build and redeploy usagewidgetd to edServe over Tailscale. Use when the user
  says deploy, redeploy, ship the server, update usagewidgetd, restart the
  companion service, or runs /usagewidget-deploy.
---

# Deploy usagewidgetd to edServe

Companion Go service for this repo. Live host: **edServe** (`100.83.252.53`,
MagicDNS `edserve.tail125275.ts.net`). SSH as **root**.

Canonical docs: `server/deploy/README.md`.

## Preconditions

- Tailscale up; `edserve` shows in `tailscale status`
- `ssh -o BatchMode=yes root@100.83.252.53 true` succeeds
- Local Go toolchain can cross-compile (`GOOS=linux GOARCH=amd64`)
- Do **not** print or commit `/etc/usagewidget/env` or any `.p8` key

## Redeploy (update existing install)

From repo root:

```bash
cd server
GOOS=linux GOARCH=amd64 go build -o /tmp/usagewidgetd ./cmd/usagewidgetd
scp /tmp/usagewidgetd root@100.83.252.53:/tmp/usagewidgetd.new
ssh root@100.83.252.53 '
  set -e
  install -o root -g root -m 755 /tmp/usagewidgetd.new /usr/local/bin/usagewidgetd
  rm -f /tmp/usagewidgetd.new
  systemctl restart usagewidget
  systemctl is-active usagewidget
'
```

Or run the helper:

```bash
./server/deploy/redeploy.sh
```

## Verify

```bash
ssh root@100.83.252.53 '
  set -a; source /etc/usagewidget/env; set +a
  curl -sS -H "Authorization: Bearer $USAGEWIDGET_TOKEN" http://127.0.0.1:8377/v1/health
  echo
  curl -sS -o /dev/null -w "poll:%{http_code}\n" -X POST \
    -H "Authorization: Bearer $USAGEWIDGET_TOKEN" http://127.0.0.1:8377/v1/poll
  curl -sS -o /dev/null -w "demo:%{http_code}\n" -X POST \
    -H "Authorization: Bearer $USAGEWIDGET_TOKEN" http://127.0.0.1:8377/v1/demo/alert
  systemctl status usagewidget --no-pager | head -20
'
```

Over Tailscale Serve (path-based):

```text
https://edserve.tail125275.ts.net/usagewidget/v1/health
```

Expect: `service=ok`, `polling=true`, `POST /v1/poll` → **200** or **502** (not 404), `POST /v1/demo/alert` → **200**.

## First-time install

Follow `server/deploy/README.md` (useradd, env file, systemd unit, Tailscale Serve).
Only needed once; day-to-day is the redeploy path above.

## Logs

```bash
ssh root@100.83.252.53 'journalctl -u usagewidget -n 50 --no-pager'
ssh root@100.83.252.53 'journalctl -u usagewidget -f'
```

## Do not

- Open `8377` on the public internet (localhost + Tailscale Serve only)
- Overwrite `/etc/usagewidget/env` unless the user asked
- Commit tokens, `.p8` keys, or production env files
- Rebuild on edServe (no Go toolchain there — always cross-compile from the Mac)
