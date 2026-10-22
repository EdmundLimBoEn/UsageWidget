#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf -- "$TEST_DIR"' EXIT

TOKEN="$(printf 'ab%.0s' {1..32})"
CONFIG="$TEST_DIR/env"
CAPTURE="$TEST_DIR/payload"
ARGS="$TEST_DIR/args"
printf 'USAGEWIDGET_URL=https://phone.example.ts.net/usagewidget\nUSAGEWIDGET_TOKEN=%s\n' "$TOKEN" >"$CONFIG"
mkdir "$TEST_DIR/bin"
cat >"$TEST_DIR/bin/qrencode" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >"$QR_TEST_ARGS"
cat >"$QR_TEST_CAPTURE"
EOF
chmod +x "$TEST_DIR/bin/qrencode"

export QR_TEST_ARGS="$ARGS" QR_TEST_CAPTURE="$CAPTURE"
USAGEWIDGET_CONFIG="$CONFIG" PATH="$TEST_DIR/bin:$PATH" "$ROOT/cli/usagewidget" qr >/dev/null 2>"$TEST_DIR/warning"

EXPECTED="usagewidget://configure?v=1&server=https%3A%2F%2Fphone.example.ts.net%2Fusagewidget&token=$TOKEN"
[[ $(<"$CAPTURE") == "$EXPECTED" ]]
[[ $(<"$ARGS") == "-t ANSIUTF8" ]]
grep -q 'grants full single-operator access' "$TEST_DIR/warning"
! grep -q "$TOKEN" "$TEST_DIR/warning"

# Server configuration is data, including shell syntax in allowed values.
SERVER_CONFIG="$TEST_DIR/server.env"
export USAGEWIDGET_CONFIG="$TEST_DIR/missing" USAGEWIDGET_SERVER_ENV="$SERVER_CONFIG"
export PWNED="$TEST_DIR/pwned"
for quote in "" "'" '"'; do
  printf 'USAGEWIDGET_TOKEN=%s%s%s\n' "$quote" "$TOKEN" "$quote" >"$SERVER_CONFIG"
  cat >>"$SERVER_CONFIG" <<'EOF'
X=$(touch "$PWNED")
touch "$PWNED"
USAGEWIDGET_PUBLIC_URL=https://ignored.example
DEFAULT_LOCAL_URL=https://ignored.example
APNS_KEY_ID=ignored
USAGEWIDGET_DEPLOY_HOST='host with spaces'
USAGEWIDGET_REPO="$(touch "$PWNED")"
EOF
  PATH="$TEST_DIR/bin:$PATH" "$ROOT/cli/usagewidget" qr > /dev/null 2>"$TEST_DIR/warning"
  [[ ! -e "$PWNED" ]]
  [[ $(<"$CAPTURE") == "usagewidget://configure?v=1&server=http%3A%2F%2F127.0.0.1%3A8377&token=$TOKEN" ]]
  # An allowed URL overrides the loopback default; also exercise EOF without newline.
  printf 'USAGEWIDGET_URL=%shttps://custom.example/%s' "$quote" "$quote" >>"$SERVER_CONFIG"
  "$ROOT/cli/usagewidget" env show >"$TEST_DIR/show"
  [[ ! -e "$PWNED" ]]
  grep -Fxq 'url:     https://custom.example' "$TEST_DIR/show"
  grep -Fxq 'host:    host with spaces' "$TEST_DIR/show"
  grep -Fxq 'repo:    $(touch "$PWNED")' "$TEST_DIR/show"
  ! grep -q '^apns:' "$TEST_DIR/show"
done

echo "cli tests passed"
