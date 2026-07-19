#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=../server-install.sh
source "$ROOT/server-install.sh"

map_arch x86_64; [[ $HOST_ARCH == amd64 ]]
map_arch aarch64; [[ $HOST_ARCH == arm64 ]]
map_arch arm64; [[ $HOST_ARCH == arm64 ]]

TEST_DIR="$(mktemp -d)"
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

echo "installer tests passed"
