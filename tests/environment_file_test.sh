#!/usr/bin/env bash
# These fixtures intentionally preserve shell expressions literally.
# shellcheck disable=SC2016
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/server-install.sh"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf -- "$TEST_DIR"' EXIT
ENV_FILE="$TEST_DIR/env"

check_value() {
  printf '%s' "$1" >"$ENV_FILE"
  [[ $(read_systemd_env DB_PATH) == "$2" ]]
}
check_value 'DB_PATH="/var/lib/custom database.db"' '/var/lib/custom database.db'
check_value "DB_PATH='/var/lib/custom database.db'" '/var/lib/custom database.db'
check_value $'  DB_PATH =  relative database.db \t\r\n' 'relative database.db'
check_value 'DB_PATH=relative\ database.db' 'relative database.db'
check_value $'DB_PATH=/var/lib/\\\ncustom.db\n' '/var/lib/custom.db'
check_value $'DB_PATH="/var/lib/\\\ncustom.db"\n' '/var/lib/custom.db'
check_value $'DB_PATH=\'/var/lib/custom\ndatabase.db\'\n' $'/var/lib/custom\ndatabase.db'
check_value 'DB_PATH="a\qb\$c\`d\\e\"f"' 'a\qb$c`d\e"f'
check_value 'DB_PATH=a"b"' 'a"b"'
check_value 'DB_PATH="  spaced  "  ' '  spaced  '
check_value $'# DB_PATH=ignored\n; DB_PATH=ignored\nDB_PATH=first\nDB_PATH="last"\n' last
check_value $'OTHER="multiline\nDB_PATH=ignored\n"\nDB_PATH=actual\n' actual
check_value $'DB_PATH=first\nDB_PATH=\n' ''
check_value 'OTHER=only' ''
check_value 'DB_PATH="$(touch sentinel);$HOME/`id`"' '$(touch sentinel);$HOME/`id`'
echo 'environment file tests passed'
