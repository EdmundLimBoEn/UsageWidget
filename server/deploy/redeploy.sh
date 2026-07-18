#!/usr/bin/env bash
# Cross-compile usagewidgetd and restart it on edServe.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
HOST="${USAGEWIDGET_DEPLOY_HOST:-root@100.83.252.53}"
BIN_LOCAL="${TMPDIR:-/tmp}/usagewidgetd"

cd "$ROOT/server"
echo "Building linux/amd64 usagewidgetd..."
GOOS=linux GOARCH=amd64 go build -o "$BIN_LOCAL" ./cmd/usagewidgetd

echo "Copying to ${HOST}..."
scp -o BatchMode=yes "$BIN_LOCAL" "${HOST}:/tmp/usagewidgetd.new"
scp -o BatchMode=yes "$ROOT/cli/usagewidget" "${HOST}:/tmp/usagewidget-cli.new"

echo "Installing + restarting..."
ssh -o BatchMode=yes "$HOST" '
  set -e
  install -o root -g root -m 755 /tmp/usagewidgetd.new /usr/local/bin/usagewidgetd
  install -o root -g root -m 755 /tmp/usagewidget-cli.new /usr/local/bin/usagewidget
  rm -f /tmp/usagewidgetd.new /tmp/usagewidget-cli.new
  systemctl restart usagewidget
  systemctl is-active usagewidget
'

echo "Verifying..."
ssh -o BatchMode=yes "$HOST" '
  set -e
  set -a; source /etc/usagewidget/env; set +a
  usagewidget health
  curl -sS -H "Authorization: Bearer $USAGEWIDGET_TOKEN" http://127.0.0.1:8377/v1/health
  echo
  code=$(curl -sS -o /tmp/uw-poll.json -w "%{http_code}" -X POST \
    -H "Authorization: Bearer $USAGEWIDGET_TOKEN" http://127.0.0.1:8377/v1/poll)
  echo "poll HTTP $code: $(cat /tmp/uw-poll.json)"
'

echo "Deploy OK."
