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
DATA_DIR="$TEST_DIR/default-data"
BACKUP_DIR="$TEST_DIR/backups"
DB_FILE="$TEST_DIR/custom database.db"
COLLECTOR_USER=fixture
OPERATIONS="$TEST_DIR/operations"
RUNNING="$TEST_DIR/running"
mkdir -p "$SCRIPT_DIR/bin" "$SCRIPT_DIR/deploy" "$TEST_DIR/systemd" "$TEST_DIR/local-bin" "$PREFIX/releases/1.0.0"
printf 'USAGEWIDGET_TOKEN=%032d\n' 0 >"$ENV_FILE"
printf 'DB_PATH=%s\n' "$DB_FILE" >>"$ENV_FILE"
printf '{"version":"2.0.0"}\n' >"$MANIFEST"
for name in bin/usagewidgetd bin/usagewidget-collector usagewidget server-install.sh; do
  printf '#!/bin/sh\nexit 0\n' >"$SCRIPT_DIR/$name"
  chmod +x "$SCRIPT_DIR/$name"
done
for name in usagewidget.service usagewidget-collector.service; do
  printf '[Service]\n' >"$SCRIPT_DIR/deploy/$name"
done

# Model systemd: enabling an active unit does not replace its running executable.
database_fixture() { python3 "$ROOT/tests/update_database_fixture.py" "$1" "$DB_FILE" "$ROOT"; }
systemctl() {
  printf '%s\n' "$*" >>"$OPERATIONS"
  if [[ $1 == stop ]]; then
    printf stopped >"$RUNNING"
  elif [[ $1 == start ]]; then
    [[ $* == 'start usagewidget' ]]
    [[ $(readlink -f "$PREFIX/current") == "$PREFIX/releases/1.0.0" ]]
    [[ $MODE != backup-start-failure ]] || return 1
    database_fixture old || return 1
    printf '1.0.0' >"$RUNNING"
  elif [[ $1 == restart ]]; then
    [[ $* == 'restart usagewidget-collector usagewidget' ]]
    local version
    version="$(basename "$(readlink -f "$PREFIX/current")")"
    if [[ $version == 2.0.0 ]]; then
      if [[ $MODE != fresh ]]; then
        [[ $(grep -c '^stop usagewidget$' "$OPERATIONS") == 1 ]] || return 1
      fi
      database_fixture migrate || return 1
      case "$MODE" in
        restart-failure) return 1 ;;
        stale) printf '1.0.0' >"$RUNNING"; return 0 ;;
      esac
    else
      [[ $(grep -c '^stop usagewidget$' "$OPERATIONS") == 2 ]] || return 1
      database_fixture old || return 1
      [[ $(stat -c %a "$DB_FILE") == 600 ]] || return 1
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
  printf 'health %s\n' "$version" >>"$OPERATIONS"
  [[ $MODE != unhealthy || $version != 2.0.0 ]] || database=false
  [[ $MODE != rollback-unhealthy ]] || database=false
  if [[ $MODE == missing-version && $version == 2.0.0 ]]; then
    printf '{"service":"ok","database":true}' >"$output"
  else
    jq -n --arg version "$version" --argjson database "$database" \
      '{service:"ok",database:$database,version:$version}' >"$output"
  fi
  printf '200'
}
sleep() { :; }
cp() {
  if [[ $MODE == backup-failure || $MODE == backup-start-failure ]] && [[ $3 == "$DB_FILE" ]]; then
    return 1
  elif [[ $MODE == backup-wal-failure && $3 == "$DB_FILE-wal" ]]; then
    return 1
  fi
  command cp "$@"
}

for PATH_STYLE in plain double single relative; do
  DB_FILE="$TEST_DIR/custom database.db"
  [[ $PATH_STYLE != relative ]] || DB_FILE="$DATA_DIR/custom database.db"
  mkdir -p "$DATA_DIR"
  printf 'USAGEWIDGET_TOKEN=%032d\n' 0 >"$ENV_FILE"
  case "$PATH_STYLE" in
    plain) printf 'DB_PATH=%s\n' "$DB_FILE" ;;
    double) printf 'DB_PATH="%s"\n' "$DB_FILE" ;;
    single) printf "DB_PATH='%s'\n" "$DB_FILE" ;;
    relative) printf 'DB_PATH="custom database.db"\n' ;;
  esac >>"$ENV_FILE"
for MODE in success stale missing-version unhealthy restart-failure fresh backup-failure backup-wal-failure backup-start-failure rollback-unhealthy; do
  rm -rf -- "$BACKUP_DIR"
  mkdir -p "$BACKUP_DIR"
  rm -f -- "$DB_FILE" "$DB_FILE-wal" "$DB_FILE-shm" "$DB_FILE-journal"
  database_fixture init
  chmod 0600 "$DB_FILE"
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
      database_fixture new
      ;;
    backup-*)
      [[ $result != 0 ]]
      [[ $(readlink -f "$PREFIX/current") == "$PREFIX/releases/1.0.0" ]]
      [[ $(cat "$OPERATIONS") == $'stop usagewidget\nstart usagewidget' ]]
      grep -q 'cannot preserve rollback database' "$TEST_DIR/output"
      if [[ $MODE == backup-start-failure ]]; then
        [[ $(cat "$RUNNING") == stopped ]]
        grep -q 'cannot restart existing daemon' "$TEST_DIR/output"
        database_fixture old
      else
        [[ $(cat "$RUNNING") == 1.0.0 ]]
      fi
      ;;
    *)
      [[ $result != 0 ]]
      [[ $(readlink -f "$PREFIX/current") == "$PREFIX/releases/1.0.0" ]]
      [[ $(cat "$RUNNING") == 1.0.0 ]]
      [[ $(grep -c '^restart usagewidget-collector usagewidget$' "$OPERATIONS") == 2 ]]
      grep -q 'rolling back' "$TEST_DIR/output"
      [[ $(tail -1 "$OPERATIONS") == 'health 1.0.0' ]]
      ;;
  esac
  if [[ $MODE == backup-start-failure ]]; then
    [[ -n $(find "$BACKUP_DIR" -name 'rollback.*' -print -quit) ]]
  elif [[ $MODE == rollback-unhealthy ]]; then
    grep -q 'rolled-back release failed health check' "$TEST_DIR/output"
    [[ -n $(find "$BACKUP_DIR" -name database -print -quit) ]]
  else
    [[ -z $(find "$BACKUP_DIR" -mindepth 1 -print -quit) ]]
  fi
done

done

# Existing non-install callers intentionally use health without a version gate.
MODE=stale
printf '1.0.0' >"$RUNNING"
health_ok
! health_ok 2.0.0
health_ok 1.0.0

echo 'update tests passed'
