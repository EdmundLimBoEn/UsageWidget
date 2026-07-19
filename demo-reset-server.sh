#!/usr/bin/env bash
# Reset synthetic demo state on an installed UsageWidget server.
set -euo pipefail

CONFIG_FILE="${USAGEWIDGET_CONFIG:-${XDG_CONFIG_HOME:-$HOME/.config}/usagewidget/env}"
HOST="${USAGEWIDGET_DEPLOY_HOST:-}"
YES=false
LOCAL=false

say() { printf 'usagewidget demo reset: %s\n' "$*"; }
die() { printf 'usagewidget demo reset: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Usage: ./demo-reset-server.sh [--host root@server] [--yes]

Creates a consistent backup, resets only synthetic demo state and its event
history, enables the demo provider, restarts both services, forces a fresh
poll, and verifies health. Phones, settings, rules, credentials, APNs keys,
CodexBar sessions, and real-provider state are preserved.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host) [[ $# -ge 2 ]] || die "--host needs root@server"; HOST=$2; shift 2 ;;
    --yes) YES=true; shift ;;
    --local) LOCAL=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage; die "unknown option: $1" ;;
  esac
done

reset_local() {
  [[ ${EUID:-$(id -u)} -eq 0 ]] || die "local reset must run as root"
  for tool in sqlite3 jq systemctl usagewidget usagewidget-admin; do
    command -v "$tool" >/dev/null 2>&1 || die "required server tool not found: $tool"
  done
  systemctl cat usagewidget.service usagewidget-collector.service >/dev/null 2>&1 || die "UsageWidget is not installed"
  [[ -r /etc/usagewidget/env ]] || die "/etc/usagewidget/env is missing"

  db_path="$(sed -n 's/^DB_PATH=//p' /etc/usagewidget/env | tail -1)"
  db_path="${db_path:-/var/lib/usagewidget/usagewidget.db}"
  [[ $db_path == /var/lib/usagewidget/*.db && -f $db_path ]] || die "refusing unexpected database path: $db_path"

  backup_path="$(usagewidget-admin backup)"
  [[ $backup_path == /var/lib/usagewidget/backups/usagewidget-*.tar.gz && -f $backup_path ]] || die "backup was not created in the expected directory"
  say "backup created at $backup_path"

  services_stopped=false
  poll_file="$(mktemp /tmp/usagewidget-demo-reset-poll.XXXXXX)"
  cleanup() {
    rm -f -- "$poll_file"
    if $services_stopped; then
      systemctl start usagewidget-collector.service >/dev/null 2>&1 || true
      systemctl start usagewidget.service >/dev/null 2>&1 || true
    fi
  }
  trap cleanup EXIT

  systemctl stop usagewidget.service usagewidget-collector.service
  services_stopped=true

  sqlite3 "$db_path" <<'SQL'
PRAGMA foreign_keys = ON;
BEGIN IMMEDIATE;
DELETE FROM demo_event_log;
DELETE FROM demo_runs;
DELETE FROM demo_audit;
DELETE FROM demo_state;
DELETE FROM events WHERE event_key LIKE 'demo.%';
DELETE FROM window_state WHERE window_id LIKE 'demo.%';
DELETE FROM window_samples WHERE provider_id = 'demo' OR window_id LIKE 'demo.%';
DELETE FROM alert_delivery_state WHERE window_id LIKE 'demo.%';
INSERT INTO settings(key, value) VALUES('demo_provider_enabled', 'true')
  ON CONFLICT(key) DO UPDATE SET value = excluded.value;
COMMIT;
SQL

  systemctl start usagewidget-collector.service
  systemctl start usagewidget.service
  services_stopped=false

  poll_ok=false
  for _ in $(seq 1 30); do
    if usagewidget poll >"$poll_file" 2>/dev/null && jq -e '.success == true' "$poll_file" >/dev/null 2>&1; then
      poll_ok=true
      break
    fi
    sleep 1
  done
  if ! $poll_ok; then
    say "fresh poll failed; inspect with: journalctl -u usagewidget -u usagewidget-collector -n 100"
    jq '{success,error}' "$poll_file" 2>/dev/null || true
    exit 1
  fi

  usagewidget-admin doctor --json | jq -e '.service == true and .database == true and .collector == "ok"' >/dev/null
  demo_revision="$(sqlite3 "$db_path" "SELECT json_extract(payload, '$.revision') FROM demo_state WHERE key='demo.state';")"
  [[ $demo_revision == 1 ]] || die "demo state did not return to revision 1"
  say "ready: default demo state restored, provider enabled, fresh poll successful"
  say "preserved phones, settings, alert rules, credentials, APNs, and real-provider state"
}

if $LOCAL; then
  $YES || die "internal local mode requires --yes"
  reset_local
  exit 0
fi

if [[ -z $HOST && -r $CONFIG_FILE ]]; then
  HOST="$(sed -n 's/^USAGEWIDGET_DEPLOY_HOST=//p' "$CONFIG_FILE" | tail -1)"
fi
[[ -n $HOST ]] || die "server is unknown; pass --host root@server or configure USAGEWIDGET_DEPLOY_HOST"
[[ $HOST =~ ^[A-Za-z_][A-Za-z0-9._-]*@[A-Za-z0-9][A-Za-z0-9.:-]*$ ]] || die "--host must look like root@server"

remote_uid="$(ssh "$HOST" id -u)" || die "could not connect to $HOST"
[[ $remote_uid == 0 ]] || die "use a root SSH target so the transactional reset cannot be interrupted by sudo prompts"

if ! $YES; then
  printf 'This will back up and reset synthetic demo state on %s. Type RESET to continue: ' "$HOST" >/dev/tty
  read -r answer </dev/tty
  [[ $answer == RESET ]] || die "cancelled"
fi

say "resetting $HOST"
ssh "$HOST" bash -s -- --local --yes <"${BASH_SOURCE[0]}"
