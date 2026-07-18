# Deploy UsageWidget to edServe

The companion Go service and CodexBar collector run on **edServe**
(`100.83.252.53`, MagicDNS `edserve.tail125275.ts.net`). Connect over SSH as
**root**.

The canonical first-time installation reference is
[`server/deploy/README.md`](server/deploy/README.md).

## Preconditions

- Tailscale is running and `edserve` appears in `tailscale status`.
- `ssh -o BatchMode=yes root@100.83.252.53 true` succeeds.
- The local Go toolchain can cross-compile with `GOOS=linux GOARCH=amd64`.
- Never print or commit `/etc/usagewidget/env`, `/etc/usagewidget/collector.env`,
  or an APNs `.p8` key.

## Redeploy

From the repository root, run:

```bash
./server/deploy/redeploy.sh
```

The helper cross-compiles and installs both `usagewidgetd` and
`usagewidget-collector`, updates the CLI and systemd units, restarts both
services, and verifies health plus a live poll.

The Mac CLI exposes the same workflow after linking it into the local path:

```bash
ln -sfn "$PWD/cli/usagewidget" ~/.local/bin/usagewidget
usagewidget deploy
usagewidget health
usagewidget poll
usagewidget demo
```

Mac configuration lives at `~/.config/usagewidget/env` and can be populated
with `usagewidget env sync`.

To deploy to a different host:

```bash
USAGEWIDGET_DEPLOY_HOST=root@example ./server/deploy/redeploy.sh
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
  systemctl status usagewidget usagewidget-collector --no-pager | head -40
'
```

Over Tailscale Serve, the path-based health endpoint is:

```text
https://edserve.tail125275.ts.net/usagewidget/v1/health
```

Expect `service=ok`, `polling=true`, and `POST /v1/poll` and
`POST /v1/demo/alert` to return `200`. A provider-specific failure may still be
present in an otherwise successful poll.

## Logs

```bash
ssh root@100.83.252.53 'journalctl -u usagewidget -u usagewidget-collector -n 50 --no-pager'
ssh root@100.83.252.53 'journalctl -u usagewidget -u usagewidget-collector -f'
```

## First-time installation

Follow [`server/deploy/README.md`](server/deploy/README.md) to create the
service account, environment files, systemd units, and Tailscale Serve route.
These steps are not needed for routine redeploys.

## Safety constraints

- Keep port `8377` bound to localhost and expose it only through Tailscale
  Serve.
- Do not overwrite production environment files unless explicitly requested.
- Do not commit tokens, production environment files, or APNs keys.
- Cross-compile on the Mac; edServe does not have the Go build toolchain.
