#!/usr/bin/env bash
# Cross-compile usagewidgetd and restart it on edServe.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
HOST="${USAGEWIDGET_DEPLOY_HOST:-root@100.83.252.53}"
BIN_LOCAL="${TMPDIR:-/tmp}/usagewidgetd"
COLLECTOR_LOCAL="${TMPDIR:-/tmp}/usagewidget-collector"

cd "$ROOT/server"
echo "Building linux/amd64 usagewidgetd..."
GOOS=linux GOARCH=amd64 go build -o "$BIN_LOCAL" ./cmd/usagewidgetd
GOOS=linux GOARCH=amd64 go build -o "$COLLECTOR_LOCAL" ./cmd/usagewidget-collector

echo "Copying to ${HOST}..."
scp -o BatchMode=yes "$BIN_LOCAL" "${HOST}:/tmp/usagewidgetd.new"
scp -o BatchMode=yes "$COLLECTOR_LOCAL" "${HOST}:/tmp/usagewidget-collector.new"
scp -o BatchMode=yes "$ROOT/cli/usagewidget" "${HOST}:/tmp/usagewidget-cli.new"
scp -o BatchMode=yes "$ROOT/server/deploy/usagewidget.service" "${HOST}:/tmp/usagewidget.service.new"
scp -o BatchMode=yes "$ROOT/server/deploy/usagewidget-collector.service" "${HOST}:/tmp/usagewidget-collector.service.new"

echo "Installing + restarting..."
ssh -o BatchMode=yes "$HOST" '
  set -e
  install -o root -g root -m 755 /tmp/usagewidgetd.new /usr/local/bin/usagewidgetd
  install -o root -g root -m 755 /tmp/usagewidget-collector.new /usr/local/bin/usagewidget-collector
  install -o root -g root -m 755 /tmp/usagewidget-cli.new /usr/local/bin/usagewidget
  install -o root -g root -m 644 /tmp/usagewidget.service.new /etc/systemd/system/usagewidget.service
  install -o root -g root -m 644 /tmp/usagewidget-collector.service.new /etc/systemd/system/usagewidget-collector.service
  rm -f /tmp/usagewidgetd.new /tmp/usagewidget-collector.new /tmp/usagewidget-cli.new /tmp/usagewidget.service.new /tmp/usagewidget-collector.service.new
  systemctl daemon-reload
  systemctl enable usagewidget-collector.service
  systemctl restart usagewidget-collector.service
  systemctl restart usagewidget
  systemctl is-active usagewidget-collector.service
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
