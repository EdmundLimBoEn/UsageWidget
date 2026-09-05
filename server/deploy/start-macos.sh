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
REQUESTED_CROSSUSAGE_URL="${CROSSUSAGE_URL:-}"

die() { printf 'usagewidget: %s\n' "$*" >&2; exit 1; }
command -v openssl >/dev/null 2>&1 || die "openssl is required"
[[ -x "$DAEMON" ]] || die "server binary not found: $DAEMON"
mkdir -p "$DATA_DIR"

if [[ ! -f "$ENV_FILE" ]]; then
  crossusage="$(command -v crossusage-cli 2>/dev/null || true)"
  [[ -n "$crossusage" || -n $REQUESTED_CROSSUSAGE_URL ]] \
    || die "crossusage-cli was not found; install it, or set CROSSUSAGE_URL to http://127.0.0.1:6736/v1/limits"
  token="$(openssl rand -hex 32)"
  {
    printf 'USAGEWIDGET_TOKEN=%q\n' "$token"
    if [[ -n "$crossusage" ]]; then
      printf 'CROSSUSAGE_BIN=%q\n' "$crossusage"
      resources="$(cd "$(dirname "$crossusage")" && pwd)/resources"
      if [[ -d $resources/bundled_plugins ]]; then
        printf 'CROSSUSAGE_RESOURCES=%q\n' "$resources"
      fi
    else
      printf 'CROSSUSAGE_URL=%q\n' "$REQUESTED_CROSSUSAGE_URL"
    fi
    printf 'DB_PATH=%q\n' "$DATA_DIR/usagewidget.db"
    printf 'LISTEN_ADDR=%q\n' '127.0.0.1:8377'
  } >"$ENV_FILE"
  printf 'Created private configuration: %s\n' "$ENV_FILE"
fi

unset CROSSUSAGE_CMD CROSSUSAGE_URL CROSSUSAGE_BIN CROSSUSAGE_RESOURCES
set -a
# This file is private, generated locally, and may be edited to add APNS_* vars.
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a

[[ ${#USAGEWIDGET_TOKEN} -ge 32 ]] || die "USAGEWIDGET_TOKEN must be at least 32 characters"
[[ -n ${CROSSUSAGE_BIN:-} || -n ${CROSSUSAGE_URL:-} || -n ${CROSSUSAGE_CMD:-} ]] \
  || die "configure CROSSUSAGE_BIN or CROSSUSAGE_URL in $ENV_FILE"
if [[ -n ${CROSSUSAGE_BIN:-} ]]; then
  [[ -x "$CROSSUSAGE_BIN" ]] || command -v "$CROSSUSAGE_BIN" >/dev/null 2>&1 || die "CrossUsage CLI not found: $CROSSUSAGE_BIN"
fi

printf 'UsageWidget is starting at http://%s (press Control-C to stop).\n' "${LISTEN_ADDR:-127.0.0.1:8377}"
exec "$DAEMON"
