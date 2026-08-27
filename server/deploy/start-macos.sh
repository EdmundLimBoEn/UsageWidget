#!/usr/bin/env bash
# Start a native UsageWidget server on macOS in the foreground.
set -euo pipefail
umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ -x "$SCRIPT_DIR/bin/usagewidgetd" ]]; then
  ROOT="$SCRIPT_DIR"
else
  ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
fi
DAEMON="${USAGEWIDGET_DAEMON:-$ROOT/bin/usagewidgetd}"
DATA_DIR="${USAGEWIDGET_DATA_DIR:-$HOME/Library/Application Support/UsageWidget}"
ENV_FILE="${USAGEWIDGET_CONFIG:-$DATA_DIR/server.env}"
REQUESTED_OPENUSAGE_URL="${OPENUSAGE_URL:-}"
REQUESTED_CODEXBAR_URL="${CODEXBAR_URL:-}"

die() { printf 'usagewidget: %s\n' "$*" >&2; exit 1; }
command -v openssl >/dev/null 2>&1 || die "openssl is required"
[[ -x "$DAEMON" ]] || die "server binary not found: $DAEMON"
mkdir -p "$DATA_DIR"

if [[ ! -f "$ENV_FILE" ]]; then
  openusage="$(command -v openusage 2>/dev/null || true)"
  codexbar="$(command -v codexbar 2>/dev/null || command -v CodexBarCLI 2>/dev/null || true)"
  [[ -n "$openusage" || -n $REQUESTED_OPENUSAGE_URL || -n "$codexbar" || -n $REQUESTED_CODEXBAR_URL ]] \
    || die "OpenUsage or CodexBar was not found; install openusage, or set OPENUSAGE_URL / CODEXBAR_URL"
  token="$(openssl rand -hex 32)"
  {
    printf 'USAGEWIDGET_TOKEN=%q\n' "$token"
    printf 'USAGE_SOURCE=auto\n'
    if [[ -n "$openusage" ]]; then
      printf 'OPENUSAGE_BIN=%q\n' "$openusage"
    elif [[ -n $REQUESTED_OPENUSAGE_URL ]]; then
      printf 'OPENUSAGE_URL=%q\n' "$REQUESTED_OPENUSAGE_URL"
    elif [[ -n "$codexbar" ]]; then
      printf 'USAGE_SOURCE=codexbar\n'
      printf 'CODEXBAR_BIN=%q\n' "$codexbar"
    else
      printf 'USAGE_SOURCE=codexbar\n'
      printf 'CODEXBAR_URL=%q\n' "$REQUESTED_CODEXBAR_URL"
    fi
    printf 'DB_PATH=%q\n' "$DATA_DIR/usagewidget.db"
    printf 'LISTEN_ADDR=%q\n' '127.0.0.1:8377'
  } >"$ENV_FILE"
  printf 'Created private configuration: %s\n' "$ENV_FILE"
fi

unset CODEXBAR_CMD CODEXBAR_URL CODEXBAR_BIN OPENUSAGE_CMD OPENUSAGE_URL OPENUSAGE_BIN OPENUSAGE_SOCKET USAGE_SOURCE
set -a
# This file is private, generated locally, and may be edited to add APNS_* vars.
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a

[[ ${#USAGEWIDGET_TOKEN} -ge 32 ]] || die "USAGEWIDGET_TOKEN must be at least 32 characters"
[[ -n ${OPENUSAGE_BIN:-} || -n ${OPENUSAGE_URL:-} || -n ${OPENUSAGE_CMD:-} || -n ${OPENUSAGE_SOCKET:-} || -n ${CODEXBAR_BIN:-} || -n ${CODEXBAR_URL:-} || -n ${CODEXBAR_CMD:-} ]] \
  || die "configure OPENUSAGE_* or CODEXBAR_* in $ENV_FILE"
if [[ -n ${OPENUSAGE_BIN:-} ]]; then
  [[ -x "$OPENUSAGE_BIN" ]] || command -v "$OPENUSAGE_BIN" >/dev/null 2>&1 || die "OpenUsage CLI not found: $OPENUSAGE_BIN"
fi
if [[ -n ${CODEXBAR_BIN:-} ]]; then
  [[ -x "$CODEXBAR_BIN" ]] || command -v "$CODEXBAR_BIN" >/dev/null 2>&1 || die "CodexBar CLI not found: $CODEXBAR_BIN"
fi

printf 'UsageWidget is starting at http://%s (press Control-C to stop).\n' "${LISTEN_ADDR:-127.0.0.1:8377}"
exec "$DAEMON"
