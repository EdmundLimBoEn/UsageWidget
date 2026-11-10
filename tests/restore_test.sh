#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=../server-install.sh
source "$ROOT/server-install.sh"
DEFAULT_BACKUP_DIR=$BACKUP_DIR

TEST_DIR="$(mktemp -d)"
trap 'rm -rf -- "$TEST_DIR"' EXIT
export TMPDIR="$TEST_DIR/tmp"
CONFIG_DIR="$TEST_DIR/config"
DATA_DIR="$TEST_DIR/data"
ENV_FILE="$CONFIG_DIR/env"
mkdir -p "$TMPDIR" "$CONFIG_DIR" "$DATA_DIR" "$TEST_DIR/archive/nested"
printf 'database fixture\n' >"$TEST_DIR/archive/usagewidget.db"
printf 'environment fixture\n' >"$TEST_DIR/archive/env"
printf 'key fixture\n' >"$TEST_DIR/archive/apns-key.p8"
printf 'private fixture\n' >"$TEST_DIR/private"
chmod 0600 "$TEST_DIR/private"

# Run real tar/install operations in temporary directories without root or services.
OPERATIONS="$TEST_DIR/operations"
install() {
  printf 'install %s\n' "$*" >>"$OPERATIONS"
  local -a args=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -o|-g) shift 2 ;;
      *) args+=("$1"); shift ;;
    esac
  done
  command install "${args[@]}"
}
systemctl() { printf 'systemctl %s\n' "$*" >>"$OPERATIONS"; }
wait_for_health() { return 0; }
tar() {
  case "$1" in -x*) printf 'extract\n' >>"$OPERATIONS" ;; esac
  command tar "$@"
}

RESTORE_FILE="$TEST_DIR/normal.tar.gz"
tar -czf "$RESTORE_FILE" -C "$TEST_DIR/archive" .
: >"$OPERATIONS"
(do_restore) >"$TEST_DIR/output" 2>&1
cmp "$TEST_DIR/archive/usagewidget.db" "$DATA_DIR/usagewidget.db"
cmp "$TEST_DIR/archive/env" "$ENV_FILE"
cmp "$TEST_DIR/archive/apns-key.p8" "$CONFIG_DIR/AuthKey.p8"
grep -Fxq 'systemctl stop usagewidget' "$OPERATIONS"
grep -Fxq 'systemctl start usagewidget' "$OPERATIONS"

assert_rejected() {
  : >"$OPERATIONS"
  if (do_restore) >"$TEST_DIR/output" 2>&1; then
    echo "restore accepted unsafe archive: $1" >&2
    exit 1
  fi
  grep -Fq 'backup contains unsupported entry types' "$TEST_DIR/output"
  # Rejection must happen before extraction, service changes, or installation.
  [[ ! -s $OPERATIONS ]]
}

for member in usagewidget.db env apns-key.p8; do
  mv "$TEST_DIR/archive/$member" "$TEST_DIR/saved"
  ln -s "$TEST_DIR/private" "$TEST_DIR/archive/$member"
  RESTORE_FILE="$TEST_DIR/symlink.tar.gz"
  tar -czf "$RESTORE_FILE" -C "$TEST_DIR/archive" .
  assert_rejected "symlink $member"
  rm "$TEST_DIR/archive/$member"
  mv "$TEST_DIR/saved" "$TEST_DIR/archive/$member"
done

ln "$TEST_DIR/archive/usagewidget.db" "$TEST_DIR/archive/hardlink"
RESTORE_FILE="$TEST_DIR/hardlink.tar.gz"
tar -czf "$RESTORE_FILE" -C "$TEST_DIR/archive" usagewidget.db hardlink
assert_rejected hardlink
rm "$TEST_DIR/archive/hardlink"

mkfifo "$TEST_DIR/archive/fifo"
RESTORE_FILE="$TEST_DIR/fifo.tar.gz"
tar -czf "$RESTORE_FILE" -C "$TEST_DIR/archive" .
assert_rejected fifo

[[ $DEFAULT_BACKUP_DIR == /var/backups/usagewidget ]]
BACKUP_DIR="$TEST_DIR/backups"
# Model sqlite3's backup command; archive extraction and installation stay real.
sqlite3() {
  local destination=${2#".backup '"}
  destination=${destination%"'"}
  cp -- "$1" "$destination"
}
for _ in fresh existing; do
  : >"$OPERATIONS"
  do_backup >"$TEST_DIR/output"
  grep -Fxq "install -d -m 0700 -o root -g root $BACKUP_DIR" "$OPERATIONS"
  [[ $(stat -c %a "$BACKUP_DIR") == 700 ]]
  chmod 0777 "$BACKUP_DIR"
done

# Exercise database selection with distinct contents at the default and custom paths.
for path_style in plain double single relative escaped unset empty; do
  configured_db="$TEST_DIR/custom database.db"
  case "$path_style" in
    plain) printf 'DB_PATH=%s\n' "$configured_db" ;;
    double) printf 'DB_PATH="%s"\n' "$configured_db" ;;
    single) printf "DB_PATH='%s'\n" "$configured_db" ;;
    relative) configured_db="$DATA_DIR/relative database.db"; printf 'DB_PATH="relative database.db"\n' ;;
    escaped) configured_db="$DATA_DIR/relative database.db"; printf '%s\n' 'DB_PATH=relative\ database.db' ;;
    unset) configured_db="$DATA_DIR/usagewidget.db"; printf 'OTHER=fixture\n' ;;
    empty) configured_db="$DATA_DIR/usagewidget.db"; printf 'DB_PATH=\n' ;;
  esac >"$ENV_FILE"
  cp "$ENV_FILE" "$TEST_DIR/expected-env"
  printf 'obsolete default\n' >"$DATA_DIR/usagewidget.db"
  printf 'configured database\n' >"$configured_db"
  do_backup >"$TEST_DIR/output"
  RESTORE_FILE=$(cat "$TEST_DIR/output")
  [[ $(command tar -xOzf "$RESTORE_FILE" ./usagewidget.db) == 'configured database' ]]

  # Restore must use the archived environment even when the current one differs.
  printf 'DB_PATH="%s/current database.db"\n' "$TEST_DIR" >"$ENV_FILE"
  printf 'current database\n' >"$TEST_DIR/current database.db"
  printf 'changed since backup\n' >"$configured_db"
  (do_restore) >"$TEST_DIR/output" 2>&1
  [[ $(cat "$configured_db") == 'configured database' ]]
  [[ $(cat "$TEST_DIR/current database.db") == 'current database' ]]
  cmp "$TEST_DIR/expected-env" "$ENV_FILE"
  if [[ $configured_db != "$DATA_DIR/usagewidget.db" ]]; then
    [[ $(cat "$DATA_DIR/usagewidget.db") == 'obsolete default' ]]
  fi
done

# Archives without an environment retain the current configured destination.
printf 'DB_PATH="%s/current database.db"\n' "$TEST_DIR" >"$ENV_FILE"
RESTORE_FILE="$TEST_DIR/database-only.tar.gz"
tar -czf "$RESTORE_FILE" -C "$TEST_DIR/archive" usagewidget.db
(do_restore) >"$TEST_DIR/output" 2>&1
cmp "$TEST_DIR/archive/usagewidget.db" "$TEST_DIR/current database.db"

echo 'restore tests passed'
