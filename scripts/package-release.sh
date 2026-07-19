#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${1:?usage: package-release.sh VERSION amd64|arm64}"
ARCH="${2:?usage: package-release.sh VERSION amd64|arm64}"
case "$ARCH" in amd64|arm64) ;; *) echo "unsupported architecture: $ARCH" >&2; exit 1 ;; esac
NAME="usagewidget-${VERSION}-linux-${ARCH}"
DIST="${USAGEWIDGET_DIST_DIR:-$ROOT/dist}"
STAGE="$DIST/$NAME"
rm -rf -- "$STAGE"
install -d "$STAGE/bin" "$STAGE/deploy"

(cd "$ROOT/server" && CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath -ldflags "-s -w -X usagewidget/server.Version=$VERSION" -o "$STAGE/bin/usagewidgetd" ./cmd/usagewidgetd)
(cd "$ROOT/server" && CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath -ldflags "-s -w -X usagewidget/server.Version=$VERSION" -o "$STAGE/bin/usagewidget-collector" ./cmd/usagewidget-collector)
install -m 0755 "$ROOT/cli/usagewidget" "$STAGE/usagewidget"
install -m 0755 "$ROOT/server-install.sh" "$STAGE/server-install.sh"
install -m 0644 "$ROOT/server/deploy/usagewidget.service" "$ROOT/server/deploy/usagewidget-collector.service" "$STAGE/deploy/"
jq --arg version "$VERSION" '.version=$version' "$ROOT/release-manifest.json" >"$STAGE/release-manifest.json"
(cd "$STAGE" && find . -type f ! -name CHECKSUMS.sha256 -print0 | sort -z | xargs -0 sha256sum >CHECKSUMS.sha256)
(cd "$DIST" && tar -czf "$NAME.tar.gz" "$NAME")
(cd "$DIST" && sha256sum "$NAME.tar.gz" >"$NAME.tar.gz.sha256")
echo "$DIST/$NAME.tar.gz"
