#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf -- "$TEST_DIR"' EXIT
# Redirect fixed system paths so the real installer only writes inside the fixture.
source <(sed -e "s|/etc/systemd/system|$TEST_DIR/systemd|g" \
  -e "s|/usr/local/bin|$TEST_DIR/local-bin|g" "$ROOT/server-install.sh")
PREFIX="$TEST_DIR/prefix"
SCRIPT_DIR="$TEST_DIR/bundle"
MANIFEST="$SCRIPT_DIR/release-manifest.json"
ENV_FILE="$TEST_DIR/env"
COLLECTOR_USER=fixture
OPERATIONS="$TEST_DIR/operations"
RUNNING="$TEST_DIR/running"
mkdir -p "$SCRIPT_DIR/bin" "$SCRIPT_DIR/deploy" "$TEST_DIR/systemd" "$TEST_DIR/local-bin" "$PREFIX/releases/1.0.0"
printf 'USAGEWIDGET_TOKEN=%032d\n' 0 >"$ENV_FILE"
printf '{"version":"2.0.0"}\n' >"$MANIFEST"
for name in bin/usagewidgetd bin/usagewidget-collector usagewidget server-install.sh; do
  printf '#!/bin/sh\nexit 0\n' >"$SCRIPT_DIR/$name"
  chmod +x "$SCRIPT_DIR/$name"
done
for name in usagewidget.service usagewidget-collector.service; do
  printf '[Service]\n' >"$SCRIPT_DIR/deploy/$name"
done

# Model systemd: enabling an active unit does not replace its running executable.
systemctl() {
  printf '%s\n' "$*" >>"$OPERATIONS"
  if [[ $1 == restart ]]; then
    [[ $* == 'restart usagewidget-collector usagewidget' ]]
    local version
    version="$(basename "$(readlink -f "$PREFIX/current")")"
    if [[ $version == 2.0.0 ]]; then
      case "$MODE" in
        restart-failure) return 1 ;;
        stale) return 0 ;;
      esac
    fi
    printf '%s' "$version" >"$RUNNING"
  fi
}
curl() {
  local output=""
  while [[ $# -gt 0 ]]; do
    case "$1" in -o) output=$2; shift 2 ;; *) shift ;; esac
  done
  cat >/dev/null # Consume the synthetic authorization header without logging it.
  [[ -n $output ]]
  local version database=true
  version=$(cat "$RUNNING")
  [[ $MODE != unhealthy ]] || database=false
  if [[ $MODE == missing-version ]]; then
    printf '{"service":"ok","database":true}' >"$output"
  else
    jq -n --arg version "$version" --argjson database "$database" \
      '{service:"ok",database:$database,version:$version}' >"$output"
  fi
  printf '200'
}
sleep() { :; }

for MODE in success stale missing-version unhealthy restart-failure fresh; do
  : >"$OPERATIONS"
  printf '1.0.0' >"$RUNNING"
  rm -f "$PREFIX/current" "$PREFIX/previous"
  REQUESTED_VERSION=2.0.0
  if [[ $MODE == fresh ]]; then
    REQUESTED_VERSION="" # Fresh installs must check the manifest version too.
  else
    ln -s "$PREFIX/releases/1.0.0" "$PREFIX/current"
  fi
  # A separate shell retains errexit semantics for the installer under test.
  set +e
  (set -e; install_release) >"$TEST_DIR/output" 2>&1
  result=$?
  set -e
  case "$MODE" in
    success|fresh)
      [[ $result == 0 ]]
      [[ $(readlink -f "$PREFIX/current") == "$PREFIX/releases/2.0.0" ]]
      [[ $(cat "$RUNNING") == 2.0.0 ]]
      [[ $(grep -c '^restart usagewidget-collector usagewidget$' "$OPERATIONS") == 1 ]]
      ;;
    *)
      [[ $result != 0 ]]
      [[ $(readlink -f "$PREFIX/current") == "$PREFIX/releases/1.0.0" ]]
      [[ $(cat "$RUNNING") == 1.0.0 ]]
      [[ $(grep -c '^restart usagewidget-collector usagewidget$' "$OPERATIONS") == 2 ]]
      grep -q 'rolling back' "$TEST_DIR/output"
      ;;
  esac
done

# Existing non-install callers intentionally use health without a version gate.
MODE=stale
printf '1.0.0' >"$RUNNING"
health_ok
! health_ok 2.0.0
health_ok 1.0.0

echo 'update tests passed'
