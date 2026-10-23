#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf -- "$TEST_DIR"' EXIT
export USAGEWIDGET_CONFIG="$TEST_DIR/env" TMPDIR="$TEST_DIR/tmp"
mkdir "$TMPDIR"
printf 'USAGEWIDGET_URL=http://localhost:8377\nUSAGEWIDGET_TOKEN=%s\n' \
  "$(printf 'ab%.0s' {1..32})" >"$USAGEWIDGET_CONFIG"

curl() {
  local connect='' total='' output='' line
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --connect-timeout) connect=$2; shift 2 ;;
      --max-time) total=$2; shift 2 ;;
      -o) output=$2; shift 2 ;;
      *) shift ;;
    esac
  done
  [[ $connect == 5 && $total == "$EXPECTED_TIMEOUT" ]] || return 99
  IFS= read -r line
  [[ $line == 'Authorization: Bearer '* ]] || return 99
  [[ -f $output ]] || return 99
  if [[ $SIMULATE_TIMEOUT == true ]]; then
    printf 'partial response' >"$output"
    return 28
  fi
  printf '{"ok":true}' >"$output"
  printf '%s' "$HTTP_CODE"
}
export -f curl
export EXPECTED_TIMEOUT SIMULATE_TIMEOUT HTTP_CODE
for cmd in health snapshot settings poll; do
  EXPECTED_TIMEOUT=30
  [[ $cmd != poll ]] || EXPECTED_TIMEOUT=330
  SIMULATE_TIMEOUT=false
  HTTP_CODE=200
  "$ROOT/cli/usagewidget" "$cmd" >"$TEST_DIR/result"
  grep -Fxq '{"ok":true}' "$TEST_DIR/result"
  if [[ $cmd == poll ]]; then
    HTTP_CODE=502
    "$ROOT/cli/usagewidget" "$cmd" >"$TEST_DIR/result"
    grep -Fxq '{"ok":true}' "$TEST_DIR/result"
  fi
  SIMULATE_TIMEOUT=true
  if "$ROOT/cli/usagewidget" "$cmd" >"$TEST_DIR/result" 2>"$TEST_DIR/error"; then
    echo "CLI should fail on curl timeout" >&2
    exit 1
  fi
  grep -q 'request failed:' "$TEST_DIR/error"
  [[ ! -s "$TEST_DIR/result" ]]
  [[ -z $(ls -A "$TMPDIR") ]]
done
echo 'cli timeout tests passed'
