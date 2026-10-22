#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/server-install.sh"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf -- "$TEST_DIR"' EXIT
CONFIG_DIR="$TEST_DIR"
ENV_FILE="$TEST_DIR/env"
export ARGV_TEST_OLD="$(printf 'ab%.0s' {1..32})"
export ARGV_TEST_NEW="$(printf 'cd%.0s' {1..32})"
export ARGV_TEST_FAILURE="$TEST_DIR/leak"
export ARGV_TEST_CALLS="$TEST_DIR/calls"

check_argv() {
  local arg
  for arg in "$@"; do
    if [[ $arg == *"$ARGV_TEST_OLD"* || $arg == *"$ARGV_TEST_NEW"* ]]; then
      touch "$ARGV_TEST_FAILURE"
      return 1
    fi
  done
}
curl() {
  check_argv "$@" || return 1
  local output="" code=false header=false line
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -H) [[ $2 != @- ]] || header=true; shift 2 ;;
      -o) output=$2; shift 2 ;;
      -w) code=true; shift 2 ;;
      *) shift ;;
    esac
  done
  $header || return 1
  IFS= read -r line
  [[ $line == "Authorization: Bearer $ARGV_TEST_OLD" ]] || return 1
  echo curl >>"$ARGV_TEST_CALLS"
  local body='{"service":"ok","database":true,"schemaVersion":1,"version":"test","collector":{"status":"ok"},"apns":false}'
  if [[ -n $output ]]; then printf '%s' "$body" >"$output"; else printf '%s' "$body"; fi
  if $code; then printf '200'; fi
}
awk() {
  check_argv "$@" || return 1
  echo awk >>"$ARGV_TEST_CALLS"
  command awk "$@"
}
export -f check_argv curl
# Keep all installer side effects inside the temporary directory.
chown() { :; }
systemctl() { :; }
openssl() { printf '%s\n' "$ARGV_TEST_NEW"; }
print_qr() { :; }
wait_for_health() { [[ $case_name != rollback ]]; }

failed=false
for case_name in cli health doctor rotate rollback; do
  set +e
  (
    set -e
    rm -f "$ARGV_TEST_FAILURE"
    : >"$ARGV_TEST_CALLS"
    printf 'USAGEWIDGET_URL=http://localhost:8377\nUSAGEWIDGET_TOKEN=%s\nOTHER=preserved\n' "$ARGV_TEST_OLD" >"$ENV_FILE"
    case "$case_name" in
      cli)
        USAGEWIDGET_CONFIG="$ENV_FILE" "$ROOT/cli/usagewidget" health >"$TEST_DIR/result"
        jq -e '.service == "ok"' "$TEST_DIR/result" >/dev/null
        [[ $(wc -l <"$ARGV_TEST_CALLS") -eq 1 ]] ;;
      health) health_ok; [[ $(wc -l <"$ARGV_TEST_CALLS") -eq 1 ]] ;;
      doctor)
        do_doctor >"$TEST_DIR/result"
        grep -Fxq 'database: true (schema 1)' "$TEST_DIR/result"
        [[ $(wc -l <"$ARGV_TEST_CALLS") -eq 2 ]] ;;
      rotate)
        do_rotate_token >"$TEST_DIR/result"
        [[ $(read_env USAGEWIDGET_TOKEN) == "$ARGV_TEST_NEW" ]]
        [[ $(stat -c %a "$ENV_FILE") == 600 ]]
        [[ $(wc -l <"$ARGV_TEST_CALLS") -eq 1 ]] ;;
      rollback)
        if (do_rotate_token) >"$TEST_DIR/result" 2>&1; then exit 1; fi
        grep -q 'token rotation failed and was rolled back' "$TEST_DIR/result"
        [[ $(read_env USAGEWIDGET_TOKEN) == "$ARGV_TEST_OLD" ]]
        [[ $(wc -l <"$ARGV_TEST_CALLS") -eq 2 ]] ;;
    esac
    grep -Fxq 'OTHER=preserved' "$ENV_FILE"
    [[ ! -e "$ARGV_TEST_FAILURE" ]]
  )
  status=$?
  set -e
  if [[ $status -eq 0 ]]; then
    echo "token argv: $case_name passed"
  else
    echo "token argv: $case_name FAILED" >&2
    failed=true
  fi
done
! $failed
