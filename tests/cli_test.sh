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

echo "cli tests passed"
