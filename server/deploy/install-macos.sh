#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATA_DIR="${USAGEWIDGET_DATA_DIR:-$HOME/Library/Application Support/UsageWidget}"
ENV_FILE="$DATA_DIR/server.env"
PLIST="$HOME/Library/LaunchAgents/systems.edmundlim.usagewidget.server.plist"
CODEXBAR_SOURCE_URL="${CODEXBAR_URL:-}"

die() { printf 'usagewidget: %s\n' "$*" >&2; exit 1; }
command -v openssl >/dev/null 2>&1 || die "openssl is required"
tailscale="$(command -v tailscale 2>/dev/null || true)"
[[ -n $tailscale ]] || die "Tailscale must be installed and signed in on the target Mac"
dns="$($tailscale status --json | sed -n 's/.*"DNSName"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1 | sed 's/\.$//')"
[[ -n $dns ]] || die "could not determine the target Mac Tailscale name"
public_url="https://$dns/usagewidget"

mkdir -p "$DATA_DIR" "$HOME/Library/LaunchAgents"
if [[ ! -f $ENV_FILE ]]; then
  codexbar=""
  for candidate in "/Applications/CodexBar.app/Contents/Helpers/CodexBarCLI" "$(command -v codexbar 2>/dev/null || true)" "$(command -v CodexBarCLI 2>/dev/null || true)"; do
    if [[ -x $candidate ]] && "$candidate" config validate >/dev/null 2>&1; then codexbar="$candidate"; break; fi
  done
  [[ -n $codexbar || -n $CODEXBAR_SOURCE_URL ]] || die "no working CodexBar CLI found; rerun with CODEXBAR_URL set to a private CodexBar endpoint"
  token="$(openssl rand -hex 32)"
  {
    printf 'USAGEWIDGET_TOKEN=%q\n' "$token"
    printf 'USAGEWIDGET_PUBLIC_URL=%q\n' "$public_url"
    if [[ -n $codexbar ]]; then printf 'CODEXBAR_BIN=%q\n' "$codexbar"; else printf 'CODEXBAR_URL=%q\n' "$CODEXBAR_SOURCE_URL"; fi
    printf 'DB_PATH=%q\n' "$DATA_DIR/usagewidget.db"
    printf 'LISTEN_ADDR=%q\n' '127.0.0.1:8377'
  } >"$ENV_FILE"
elif [[ -n $CODEXBAR_SOURCE_URL ]]; then
  tmp_env="$(mktemp "$DATA_DIR/server.env.XXXXXX")"
  grep -vE '^(CODEXBAR_BIN|CODEXBAR_URL|USAGEWIDGET_PUBLIC_URL)=' "$ENV_FILE" >"$tmp_env"
  printf 'CODEXBAR_URL=%q\nUSAGEWIDGET_PUBLIC_URL=%q\n' "$CODEXBAR_SOURCE_URL" "$public_url" >>"$tmp_env"
  mv -f "$tmp_env" "$ENV_FILE"
elif ! grep -q '^USAGEWIDGET_PUBLIC_URL=' "$ENV_FILE"; then
  printf 'USAGEWIDGET_PUBLIC_URL=%q\n' "$public_url" >>"$ENV_FILE"
fi

cat >"$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>systems.edmundlim.usagewidget.server</string>
<key>ProgramArguments</key><array><string>/bin/bash</string><string>$ROOT/start-server.sh</string></array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
<key>StandardOutPath</key><string>$DATA_DIR/server.log</string>
<key>StandardErrorPath</key><string>$DATA_DIR/server-error.log</string>
</dict></plist>
EOF
launchctl bootout "gui/$(id -u)/systems.edmundlim.usagewidget.server" >/dev/null 2>&1 || true
launchctl bootstrap "gui/$(id -u)" "$PLIST"
launchctl kickstart -k "gui/$(id -u)/systems.edmundlim.usagewidget.server"
$tailscale serve --bg --https=443 --set-path=/usagewidget http://127.0.0.1:8377 >/dev/null

set -a; source "$ENV_FILE"; set +a
for _ in {1..30}; do
  curl -fsS -H "Authorization: Bearer $USAGEWIDGET_TOKEN" http://127.0.0.1:8377/v1/health >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS -H "Authorization: Bearer $USAGEWIDGET_TOKEN" http://127.0.0.1:8377/v1/health >/dev/null || die "installed server failed its health check"
printf 'UsageWidget is healthy at %s\n' "$public_url"
exec "$ROOT/bin/usagewidget-qr" -url "$public_url" -token "$USAGEWIDGET_TOKEN"
