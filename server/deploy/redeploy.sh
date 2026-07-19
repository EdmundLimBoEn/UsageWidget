#!/usr/bin/env bash
# Cross-compile usagewidgetd and restart it on a configured server.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
HOST="${USAGEWIDGET_DEPLOY_HOST:-}"
BIN_LOCAL="${TMPDIR:-/tmp}/usagewidgetd"
COLLECTOR_LOCAL="${TMPDIR:-/tmp}/usagewidget-collector"
COLLECTOR_OVERRIDE_LOCAL="${TMPDIR:-/tmp}/usagewidget-collector-user.conf"

if [[ -z "$HOST" ]]; then
  echo "USAGEWIDGET_DEPLOY_HOST is required (for example: deploy@example-host)" >&2
  exit 1
fi

REMOTE_ARCH="$(ssh -o BatchMode=yes "$HOST" uname -m)"
case "$REMOTE_ARCH" in
  x86_64) GO_ARCH=amd64 ;;
  aarch64|arm64) GO_ARCH=arm64 ;;
  *) echo "unsupported server architecture: $REMOTE_ARCH" >&2; exit 1 ;;
esac

COLLECTOR_USER="${USAGEWIDGET_COLLECTOR_USER:-$(ssh -o BatchMode=yes "$HOST" '
  configured=$(systemctl show usagewidget-collector.service -p User --value 2>/dev/null || true)
  if [[ -n "$configured" ]] && getent passwd "$configured" >/dev/null; then
    printf "%s" "$configured"
  else
    getent passwd | awk -F: '\''$3>=1000 && $3<65534 && $7 !~ /(nologin|false)$/ {print $1; exit}'\''
  fi
')}"
[[ -n "$COLLECTOR_USER" ]] || { echo "could not resolve an existing unprivileged collector user" >&2; exit 1; }
COLLECTOR_HOME="$(ssh -o BatchMode=yes "$HOST" "getent passwd '$COLLECTOR_USER' | cut -d: -f6")"
printf '[Service]\nUser=%s\nGroup=usagewidget\nEnvironment=PATH=%s/.linuxbrew/bin:/home/linuxbrew/.linuxbrew/bin:/usr/local/bin:/usr/bin:/bin\n' \
  "$COLLECTOR_USER" "$COLLECTOR_HOME" >"$COLLECTOR_OVERRIDE_LOCAL"

cd "$ROOT/server"
echo "Building linux/${GO_ARCH} usagewidgetd..."
GOOS=linux GOARCH="$GO_ARCH" go build -o "$BIN_LOCAL" ./cmd/usagewidgetd
GOOS=linux GOARCH="$GO_ARCH" go build -o "$COLLECTOR_LOCAL" ./cmd/usagewidget-collector

echo "Copying to ${HOST}..."
scp -o BatchMode=yes "$BIN_LOCAL" "${HOST}:/tmp/usagewidgetd.new"
scp -o BatchMode=yes "$COLLECTOR_LOCAL" "${HOST}:/tmp/usagewidget-collector.new"
scp -o BatchMode=yes "$ROOT/cli/usagewidget" "${HOST}:/tmp/usagewidget-cli.new"
scp -o BatchMode=yes "$ROOT/server/deploy/usagewidget.service" "${HOST}:/tmp/usagewidget.service.new"
scp -o BatchMode=yes "$ROOT/server/deploy/usagewidget-collector.service" "${HOST}:/tmp/usagewidget-collector.service.new"
scp -o BatchMode=yes "$COLLECTOR_OVERRIDE_LOCAL" "${HOST}:/tmp/usagewidget-collector-user.conf.new"

echo "Installing + restarting..."
ssh -o BatchMode=yes "$HOST" '
  set -e
  install -o root -g root -m 755 /tmp/usagewidgetd.new /usr/local/bin/usagewidgetd
  install -o root -g root -m 755 /tmp/usagewidget-collector.new /usr/local/bin/usagewidget-collector
  install -o root -g root -m 755 /tmp/usagewidget-cli.new /usr/local/bin/usagewidget
  install -o root -g root -m 644 /tmp/usagewidget.service.new /etc/systemd/system/usagewidget.service
  install -o root -g root -m 644 /tmp/usagewidget-collector.service.new /etc/systemd/system/usagewidget-collector.service
  install -d -o root -g root -m 755 /etc/systemd/system/usagewidget-collector.service.d
  install -o root -g root -m 644 /tmp/usagewidget-collector-user.conf.new /etc/systemd/system/usagewidget-collector.service.d/user.conf
  rm -f /tmp/usagewidgetd.new /tmp/usagewidget-collector.new /tmp/usagewidget-cli.new /tmp/usagewidget.service.new /tmp/usagewidget-collector.service.new /tmp/usagewidget-collector-user.conf.new
  systemctl daemon-reload
  systemctl enable usagewidget-collector.service
  systemctl restart usagewidget-collector.service
  systemctl restart usagewidget
  for attempt in $(seq 1 20); do
    if systemctl is-active --quiet usagewidget-collector.service && systemctl is-active --quiet usagewidget; then
      exit 0
    fi
    sleep 1
  done
  systemctl status usagewidget-collector.service usagewidget --no-pager
  exit 1
'

echo "Verifying..."
ssh -o BatchMode=yes "$HOST" '
  set -e
  set -a; source /etc/usagewidget/env; set +a
  code=$(curl -sS -o /tmp/uw-poll.json -w "%{http_code}" -X POST \
    -H "Authorization: Bearer $USAGEWIDGET_TOKEN" http://127.0.0.1:8377/v1/poll)
  echo "poll HTTP $code: $(cat /tmp/uw-poll.json)"
  [[ "$code" == 200 ]]
  usagewidget health
'

echo "Deploy OK."
