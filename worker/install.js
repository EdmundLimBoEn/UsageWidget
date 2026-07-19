const REPO = "EdmundLimBoEn/UsageWidget";
const PUBLIC_URL = "https://usagewidget.edmundlim.systems";

const installScript = `#!/usr/bin/env bash
set -euo pipefail

REPO="\${USAGEWIDGET_REPO:-EdmundLimBoEn/UsageWidget}"
PUBLIC_URL="\${USAGEWIDGET_PUBLIC_URL:-https://usagewidget.edmundlim.systems}"
VERSION="\${USAGEWIDGET_VERSION:-latest}"
COLLECTOR_USER=""
EXTRA_ARGS=()

usage() {
  cat <<'EOF'
Usage:
  curl -fsSL https://usagewidget.edmundlim.systems/install.sh | sudo bash -s -- --collector-user YOUR_LOGIN

Options:
  --collector-user USER   Linux user that owns the CodexBar session
  --version VERSION       UsageWidget release version, without the leading v

Environment:
  USAGEWIDGET_PUBLIC_URL  Override the server URL passed to the installer
  USAGEWIDGET_VERSION     Install a specific release version
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --collector-user) [[ $# -ge 2 ]] || { usage >&2; exit 2; }; COLLECTOR_USER=$2; shift 2 ;;
    --version) [[ $# -ge 2 ]] || { usage >&2; exit 2; }; VERSION=$2; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) EXTRA_ARGS+=("$1"); shift ;;
  esac
done

[[ \${EUID:-$(id -u)} -eq 0 ]] || { echo "usagewidget: run through sudo" >&2; exit 1; }
[[ -n "$COLLECTOR_USER" ]] || { echo "usagewidget: pass --collector-user USER" >&2; usage >&2; exit 2; }

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "usagewidget: required tool not found: $1" >&2
    exit 1
  }
}

for tool in curl jq sha256sum tar uname mktemp; do need "$tool"; done

case "$(uname -m)" in
  x86_64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "usagewidget: supported architectures are amd64 and arm64" >&2; exit 1 ;;
esac

if [[ "$VERSION" == latest ]]; then
  VERSION="$(curl -fsSL "https://api.github.com/repos/\${REPO}/releases/latest" | jq -er '.tag_name' | sed 's/^v//')"
fi

ASSET="usagewidget-\${VERSION}-linux-\${ARCH}.tar.gz"
BASE_URL="https://github.com/\${REPO}/releases/download/v\${VERSION}"
TMP="$(mktemp -d)"
trap 'rm -rf -- "$TMP"' EXIT

echo "usagewidget: downloading \${ASSET}"
curl -fL --retry 3 -o "$TMP/$ASSET" "$BASE_URL/$ASSET"
curl -fL --retry 3 -o "$TMP/$ASSET.sha256" "$BASE_URL/$ASSET.sha256"
(cd "$TMP" && sha256sum -c "$ASSET.sha256" >/dev/null)
tar -xzf "$TMP/$ASSET" -C "$TMP"

exec "$TMP/usagewidget-\${VERSION}-linux-\${ARCH}/server-install.sh" install \
  --collector-user "$COLLECTOR_USER" \
  --public-url "$PUBLIC_URL" \
  "\${EXTRA_ARGS[@]}"
`;

const installCommand = `curl -fsSL ${PUBLIC_URL}/install.sh | sudo bash -s -- --collector-user YOUR_LOGIN`;

function text(body, status = 200, contentType = "text/plain; charset=utf-8", cacheControl = "public, max-age=300") {
  return new Response(body, {
    status,
    headers: {
      "content-type": contentType,
      "cache-control": cacheControl,
      "x-content-type-options": "nosniff"
    }
  });
}

export default {
  async fetch(request) {
    const url = new URL(request.url);

    if (url.pathname === "/install.sh") {
      return text(installScript, 200, "text/x-shellscript; charset=utf-8", "no-store");
    }

    if (url.pathname === "/" || url.pathname === "") {
      return text(`UsageWidget server installer

Run this on an Ubuntu 22.04/24.04 or Debian 12 host that has a CodexBar session:

${installCommand}

Use a concrete Linux login in place of YOUR_LOGIN.`, 200);
    }

    return text("Not found\n", 404);
  }
};
