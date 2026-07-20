#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${1:?usage: package-desktop-release.sh VERSION darwin|windows amd64|arm64}"
OS="${2:?usage: package-desktop-release.sh VERSION darwin|windows amd64|arm64}"
ARCH="${3:?usage: package-desktop-release.sh VERSION darwin|windows amd64|arm64}"
[[ $VERSION =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || { echo "invalid version: $VERSION" >&2; exit 1; }
case "$OS:$ARCH" in
  darwin:amd64|darwin:arm64|windows:amd64|windows:arm64) ;;
  *) printf 'unsupported target: %s/%s\n' "$OS" "$ARCH" >&2; exit 1 ;;
esac

NAME="usagewidget-${VERSION}-${OS}-${ARCH}"
DIST="${USAGEWIDGET_DIST_DIR:-$ROOT/dist}"
STAGE="$DIST/$NAME"
rm -rf -- "$STAGE"
mkdir -p "$STAGE/bin"

suffix=""
[[ $OS == windows ]] && suffix=".exe"
(cd "$ROOT/server" && CGO_ENABLED=0 GOOS="$OS" GOARCH="$ARCH" go build -trimpath \
  -ldflags "-s -w -X usagewidget/server.Version=$VERSION" \
  -o "$STAGE/bin/usagewidgetd$suffix" ./cmd/usagewidgetd)

install -m 0644 "$ROOT/README.md" "$ROOT/SECURITY.md" "$STAGE/"
if [[ $OS == darwin ]]; then
  install -m 0755 "$ROOT/server/deploy/start-macos.sh" "$STAGE/start-server.sh"
  (cd "$DIST" && tar -czf "$NAME.tar.gz" "$NAME")
  artifact="$DIST/$NAME.tar.gz"
else
  install -m 0644 "$ROOT/server/deploy/start-windows.ps1" "$STAGE/start-server.ps1"
  command -v zip >/dev/null 2>&1 || { echo "zip is required for Windows bundles" >&2; exit 1; }
  (cd "$DIST" && zip -qr "$NAME.zip" "$NAME")
  artifact="$DIST/$NAME.zip"
fi

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$DIST" && sha256sum "$(basename "$artifact")" >"$(basename "$artifact").sha256")
else
  (cd "$DIST" && shasum -a 256 "$(basename "$artifact")" >"$(basename "$artifact").sha256")
fi
printf '%s\n' "$artifact"
