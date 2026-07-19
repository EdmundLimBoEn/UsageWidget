# Source redeploy runbook

Use this runbook for day-to-day deployment of the current checkout to an
already installed UsageWidget server. For a first installation or release
upgrade, use [the Linux deployment guide](server/deploy/README.md).

## Preconditions

- `USAGEWIDGET_DEPLOY_HOST` is an SSH destination for an Ubuntu 22.04/24.04 or
  Debian 12 systemd host.
- Batch-mode SSH succeeds and the SSH account can install files and restart
  services without an interactive prompt.
- Go 1.26.5 or newer is available locally. The script detects amd64 or arm64 on
  the remote host and cross-compiles the matching binaries.
- UsageWidget has already created `/etc/usagewidget/env`, its service account,
  data directory, and systemd units.
- The selected collector user has a valid CodexBar session.

Do not print or commit `/etc/usagewidget/env`,
`/etc/usagewidget/collector.env`, the SQLite database, a backup, or an APNs
`.p8` key.

## Configure the local CLI

Create `~/.config/usagewidget/env` with mode `600`, or let an existing config
supply these values:

```bash
USAGEWIDGET_DEPLOY_HOST=deploy@example-host
USAGEWIDGET_URL=https://your-host.your-tailnet.ts.net/usagewidget
USAGEWIDGET_REPO=/absolute/path/to/UsageWidget
```

`usagewidget env sync` securely reads the bearer token from the server into the
same local file. The command never prints the token.

## Redeploy

From the repository root:

```bash
./server/deploy/redeploy.sh
```

Or through the CLI:

```bash
usagewidget deploy
```

The helper:

1. resolves the remote architecture and collector account;
2. cross-compiles `usagewidgetd` and `usagewidget-collector`;
3. uploads both binaries, the CLI, and systemd units;
4. installs a collector-user systemd override;
5. restarts both services; and
6. forces a poll and checks health.

Override the collector account only when automatic detection is wrong:

```bash
USAGEWIDGET_COLLECTOR_USER=codexbar \
  USAGEWIDGET_DEPLOY_HOST=deploy@example-host \
  ./server/deploy/redeploy.sh
```

This source workflow does not publish a release or replace the installed
`server-install.sh` lifecycle tool. Use a tagged release and
`usagewidget-admin update` for production release upgrades.

## Verify

```bash
usagewidget health
usagewidget poll
usagewidget snapshot
usagewidget status
```

Direct server verification:

```bash
ssh "$USAGEWIDGET_DEPLOY_HOST" '
  set -a; source /etc/usagewidget/env; set +a
  curl -fsS -H "Authorization: Bearer $USAGEWIDGET_TOKEN" \
    http://127.0.0.1:8377/v1/health
  curl -fsS -X POST -H "Authorization: Bearer $USAGEWIDGET_TOKEN" \
    http://127.0.0.1:8377/v1/poll
  systemctl is-active usagewidget usagewidget-collector
'
```

The private path-based endpoint is:

```text
https://your-host.your-tailnet.ts.net/usagewidget/v1/health
```

Healthy output has `service: "ok"`, `database: true`, and `polling: true`.
`codexbar` and collector details identify upstream-session failures; APNs may be
`false` for a deliberately dashboard-only installation.

## Logs and rollback

```bash
usagewidget logs
usagewidget logs -f

ssh "$USAGEWIDGET_DEPLOY_HOST" \
  'journalctl -u usagewidget -u usagewidget-collector -n 100 --no-pager'
```

Before a high-risk change, create a recoverable backup:

```bash
ssh "$USAGEWIDGET_DEPLOY_HOST" sudo usagewidget-admin backup
```

If the new checkout is unhealthy, diagnose the service and collector separately
before redeploying a known-good commit. A database/configuration rollback should
use `usagewidget-admin restore --file ARCHIVE`; source redeploys do not migrate
or replace secrets themselves.

## Safety constraints

- Keep the main listener on `127.0.0.1:8377` and expose it only through
  Tailscale Serve.
- Keep the optional demo listener on `127.0.0.1:8378` and disabled unless its
  identity-aware proxy is active.
- Do not overwrite production environment files or select a different collector
  account unless that change is intentional.
- Back up before schema-sensitive or live-demo changes.
