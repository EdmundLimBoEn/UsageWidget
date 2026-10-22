#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=../server-install.sh
source "$ROOT/server-install.sh"

map_arch x86_64; [[ $HOST_ARCH == amd64 ]]
map_arch aarch64; [[ $HOST_ARCH == arm64 ]]
map_arch arm64; [[ $HOST_ARCH == arm64 ]]

TEST_DIR="$(mktemp -d)"
trap 'rm -rf -- "$TEST_DIR"' EXIT
ENV_FILE="$TEST_DIR/env"
CAPTURE="$TEST_DIR/payload"
ARGS="$TEST_DIR/args"
TOKEN="$(printf 'ab%.0s' {1..32})"
printf 'USAGEWIDGET_PUBLIC_URL=https://phone.example.ts.net/usagewidget\nUSAGEWIDGET_TOKEN=%s\n' "$TOKEN" >"$ENV_FILE"
mkdir "$TEST_DIR/bin"
cat >"$TEST_DIR/bin/qrencode" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >"$QR_TEST_ARGS"
cat >"$QR_TEST_CAPTURE"
EOF
chmod +x "$TEST_DIR/bin/qrencode"
export QR_TEST_ARGS="$ARGS" QR_TEST_CAPTURE="$CAPTURE"
PATH="$TEST_DIR/bin:$PATH" print_qr >/dev/null 2>"$TEST_DIR/warning"
EXPECTED="usagewidget://configure?v=1&server=https%3A%2F%2Fphone.example.ts.net%2Fusagewidget&token=$TOKEN"
[[ $(<"$CAPTURE") == "$EXPECTED" ]]
! grep -q "$TOKEN" "$ARGS"
! grep -q "$TOKEN" "$TEST_DIR/warning"
grep -q 'grants full single-operator access' "$TEST_DIR/warning"

# Exercise real directory/file creation without requiring root or a service user.
# Record ownership requests, stripping only owner/group flags from real install.
OWNERSHIP_LOG="$TEST_DIR/ownership"
install() {
  printf 'install %s\n' "$*" >>"$OWNERSHIP_LOG"
  local -a args=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -o|-g) shift 2 ;;
      *) args+=("$1"); shift ;;
    esac
  done
  command install "${args[@]}"
}
chown() { printf 'chown %s\n' "$*" >>"$OWNERSHIP_LOG"; }
CONFIG_DIR="$TEST_DIR/config"
DATA_DIR="$TEST_DIR/data"
ENV_FILE="$CONFIG_DIR/env"
COLLECTOR_ENV="$CONFIG_DIR/collector.env"
PUBLIC_URL=https://example.test
CROSSUSAGE_BIN=/usr/local/bin/crossusage-cli
for pass in fresh existing repeated; do
  : >"$OWNERSHIP_LOG"
  write_initial_env
  grep -Fxq "install -d -m 0750 -o root -g usagewidget $CONFIG_DIR" "$OWNERSHIP_LOG"
  grep -Fxq "install -d -m 0750 -o usagewidget -g usagewidget $DATA_DIR" "$OWNERSHIP_LOG"
  grep -q "^chown root:usagewidget $CONFIG_DIR/env\\." "$OWNERSHIP_LOG"
  [[ $(stat -c %a "$CONFIG_DIR") == 750 ]]
  [[ $(stat -c %a "$DATA_DIR") == 750 ]]
  [[ $(stat -c %a "$ENV_FILE") == 600 ]]
  [[ $(stat -c %a "$COLLECTOR_ENV") == 640 ]]
  if [[ $pass == fresh ]]; then
    saved_token=$(read_env USAGEWIDGET_TOKEN)
    # Simulate legacy permissions and confirm rerunning repairs them.
    chmod 0777 "$CONFIG_DIR" "$DATA_DIR"
    chmod 0644 "$ENV_FILE"
  else
    [[ $(read_env USAGEWIDGET_TOKEN) == "$saved_token" ]]
  fi
done

echo "installer tests passed"
