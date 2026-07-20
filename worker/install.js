const PUBLIC_URL = "https://usagewidget.edmundlim.systems";

const unixInstallScript = String.raw`#!/usr/bin/env bash
set -euo pipefail

REPO="\${USAGEWIDGET_REPO:-EdmundLimBoEn/UsageWidget}"
VERSION="\${USAGEWIDGET_VERSION:-latest}"
COLLECTOR_USER="\${USAGEWIDGET_COLLECTOR_USER:-}"
CODEXBAR_SOURCE_URL="\${CODEXBAR_URL:-}"
EXTRA_ARGS=()

usage() {
  cat <<'EOF'
Usage:
  curl -fsSL https://usagewidget.edmundlim.systems/install.sh | bash

The installer detects Linux or macOS and asks for any required setup values.
Optional environment variables: USAGEWIDGET_VERSION, USAGEWIDGET_REPO,
USAGEWIDGET_COLLECTOR_USER, CODEXBAR_URL, and USAGEWIDGET_INSTALL_DIR.
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

say() { printf 'usagewidget: %s\n' "$*"; }
die() { printf 'usagewidget: %s\n' "$*" >&2; exit 1; }
prompt() {
  [[ -r /dev/tty && -w /dev/tty ]] || die "interactive terminal unavailable; rerun from a terminal"
  printf '%s' "$1" >/dev/tty
  IFS= read -r "$2" </dev/tty
}

OS_NAME="$(uname -s)"
case "$OS_NAME" in
  Linux) OS=linux ;;
  Darwin) OS=darwin ;;
  *) die "unsupported operating system: $OS_NAME (use install.ps1 on Windows)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "supported architectures are amd64 and arm64" ;;
esac

SUDO=()
if [[ $OS == linux && \${EUID:-$(id -u)} -ne 0 ]]; then
  command -v sudo >/dev/null 2>&1 || die "sudo is required for the Linux service install"
  SUDO=(sudo)
fi

missing_tools=()
for tool in curl tar uname mktemp; do
  command -v "$tool" >/dev/null 2>&1 || missing_tools+=("$tool")
done
if [[ $OS == linux ]]; then
  command -v sha256sum >/dev/null 2>&1 || missing_tools+=(sha256sum)
else
  command -v shasum >/dev/null 2>&1 || missing_tools+=(shasum)
fi
if (( \${#missing_tools[@]} )); then
  [[ $OS == linux ]] || die "missing required tools: \${missing_tools[*]}"
  command -v apt-get >/dev/null 2>&1 || die "missing tools (\${missing_tools[*]}); supported Linux hosts use apt"
  say "installing download prerequisites"
  "\${SUDO[@]}" apt-get update
  "\${SUDO[@]}" env DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl coreutils tar
fi

if [[ $VERSION == latest ]]; then
  VERSION="$(curl -fsSL "https://api.github.com/repos/\${REPO}/releases/latest" |
    sed -n 's/.*"tag_name":[[:space:]]*"v\{0,1\}\([^"]*\)".*/\1/p' | head -1)"
  [[ -n $VERSION ]] || die "could not resolve the latest GitHub release"
fi
[[ $VERSION =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || die "invalid release version: $VERSION"

ASSET="usagewidget-\${VERSION}-\${OS}-\${ARCH}.tar.gz"
BASE_URL="https://github.com/\${REPO}/releases/download/v\${VERSION}"
TMP="$(mktemp -d)"
trap 'rm -rf -- "$TMP"' EXIT

say "downloading $ASSET"
curl -fL --retry 3 -o "$TMP/$ASSET" "$BASE_URL/$ASSET"
curl -fL --retry 3 -o "$TMP/$ASSET.sha256" "$BASE_URL/$ASSET.sha256"
if [[ $OS == linux ]]; then
  (cd "$TMP" && sha256sum -c "$ASSET.sha256" >/dev/null)
else
  (cd "$TMP" && shasum -a 256 -c "$ASSET.sha256" >/dev/null)
fi
tar -xzf "$TMP/$ASSET" -C "$TMP"
BUNDLE="$TMP/usagewidget-\${VERSION}-\${OS}-\${ARCH}"

if [[ $OS == linux ]]; then
  if [[ -z $COLLECTOR_USER ]]; then
    default_user="\${SUDO_USER:-$(id -un)}"
    [[ $default_user != root ]] || default_user=""
    if [[ -n $default_user ]]; then
      prompt "Linux user that owns the CodexBar session [$default_user]: " COLLECTOR_USER
      COLLECTOR_USER="\${COLLECTOR_USER:-$default_user}"
    else
      prompt "Linux user that owns the CodexBar session: " COLLECTOR_USER
    fi
  fi
  [[ -n $COLLECTOR_USER && $COLLECTOR_USER != root ]] || die "an unprivileged collector user is required"
  say "installing the managed Linux services"
  exec "\${SUDO[@]}" "$BUNDLE/server-install.sh" install --collector-user "$COLLECTOR_USER" "\${EXTRA_ARGS[@]}"
fi

DATA_DIR="\${USAGEWIDGET_DATA_DIR:-$HOME/Library/Application Support/UsageWidget}"
INSTALL_DIR="\${USAGEWIDGET_INSTALL_DIR:-$DATA_DIR/App}"
mkdir -p "$INSTALL_DIR"
cp -R "$BUNDLE/." "$INSTALL_DIR/"
chmod 0755 "$INSTALL_DIR/start-server.sh" "$INSTALL_DIR/bin/usagewidgetd"

if [[ ! -f "$DATA_DIR/server.env" ]] &&
   ! command -v codexbar >/dev/null 2>&1 &&
   ! command -v CodexBarCLI >/dev/null 2>&1 &&
   [[ -z $CODEXBAR_SOURCE_URL ]]; then
  prompt "Private CodexBar usage URL: " CODEXBAR_SOURCE_URL
  [[ -n $CODEXBAR_SOURCE_URL ]] || die "a CodexBar CLI or private CodexBar URL is required"
fi

say "installed macOS files in $INSTALL_DIR"
say "starting UsageWidget; press Control-C to stop it"
if [[ -n $CODEXBAR_SOURCE_URL ]]; then export CODEXBAR_URL="$CODEXBAR_SOURCE_URL"; fi
exec "$INSTALL_DIR/start-server.sh"
`.replaceAll("\\${", "${");

const windowsInstallScript = String.raw`param(
    [string]$Version = $env:USAGEWIDGET_VERSION,
    [string]$Repository = $(if ($env:USAGEWIDGET_REPO) { $env:USAGEWIDGET_REPO } else { "EdmundLimBoEn/UsageWidget" }),
    [string]$InstallDirectory = $env:USAGEWIDGET_INSTALL_DIR,
    [string]$CodexBarUrl = $env:CODEXBAR_URL
)

$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

function Write-UsageWidget([string]$Message) { Write-Host "usagewidget: $Message" }

$runtimeArch = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
switch ($runtimeArch) {
    "X64" { $arch = "amd64" }
    "Arm64" { $arch = "arm64" }
    default { throw "UsageWidget supports Windows amd64 and arm64; detected $runtimeArch" }
}

if ([string]::IsNullOrWhiteSpace($Version) -or $Version -eq "latest") {
    $release = Invoke-RestMethod -Headers @{ "User-Agent" = "UsageWidget-Installer" } -Uri "https://api.github.com/repos/$Repository/releases/latest"
    $Version = ([string]$release.tag_name) -replace '^v', ''
}
if ($Version -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]*$') { throw "Invalid release version: $Version" }

$asset = "usagewidget-$Version-windows-$arch.zip"
$baseUrl = "https://github.com/$Repository/releases/download/v$Version"
$tempDirectory = Join-Path ([IO.Path]::GetTempPath()) ("usagewidget-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tempDirectory | Out-Null

try {
    $archivePath = Join-Path $tempDirectory $asset
    $checksumPath = "$archivePath.sha256"
    Write-UsageWidget "downloading $asset"
    Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/$asset" -OutFile $archivePath
    Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/$asset.sha256" -OutFile $checksumPath
    $expectedHash = ((Get-Content -LiteralPath $checksumPath -Raw).Trim() -split '\s+')[0]
    $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash
    if ($actualHash -ne $expectedHash) { throw "SHA-256 checksum verification failed" }

    Expand-Archive -LiteralPath $archivePath -DestinationPath $tempDirectory -Force
    $bundleDirectory = Join-Path $tempDirectory "usagewidget-$Version-windows-$arch"
    if (-not (Test-Path -LiteralPath (Join-Path $bundleDirectory "start-server.ps1") -PathType Leaf)) {
        throw "Release archive did not contain the expected Windows bundle"
    }

    $dataDirectory = Join-Path $env:LOCALAPPDATA "UsageWidget"
    if ([string]::IsNullOrWhiteSpace($InstallDirectory)) { $InstallDirectory = Join-Path $dataDirectory "App" }
    New-Item -ItemType Directory -Force -Path $InstallDirectory | Out-Null
    Copy-Item -Path (Join-Path $bundleDirectory "*") -Destination $InstallDirectory -Recurse -Force
} finally {
    Remove-Item -LiteralPath $tempDirectory -Recurse -Force -ErrorAction SilentlyContinue
}

$configPath = Join-Path (Join-Path $env:LOCALAPPDATA "UsageWidget") "server.json"
if (-not (Test-Path -LiteralPath $configPath -PathType Leaf) -and [string]::IsNullOrWhiteSpace($CodexBarUrl)) {
    $codexBar = Get-Command codexbar, CodexBarCLI -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -eq $codexBar) {
        $CodexBarUrl = Read-Host "Private CodexBar usage URL"
        if ([string]::IsNullOrWhiteSpace($CodexBarUrl)) { throw "A compatible CodexBar CLI or private CodexBar URL is required" }
    }
}

Write-UsageWidget "installed Windows files in $InstallDirectory"
Write-UsageWidget "starting UsageWidget; press Control-C to stop it"
$launcher = Join-Path $InstallDirectory "start-server.ps1"
if ([string]::IsNullOrWhiteSpace($CodexBarUrl)) {
    & $launcher
} else {
    & $launcher -CodexBarUrl $CodexBarUrl
}
exit $LASTEXITCODE
`;

const unixInstallCommand = `curl -fsSL ${PUBLIC_URL}/install.sh | bash`;
const windowsInstallCommand = `irm ${PUBLIC_URL}/install.ps1 | iex`;

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
      return text(unixInstallScript, 200, "text/x-shellscript; charset=utf-8", "no-store");
    }
    if (url.pathname === "/install.ps1") {
      return text(windowsInstallScript, 200, "text/plain; charset=utf-8", "no-store");
    }
    if (url.pathname === "/" || url.pathname === "") {
      return text(`UsageWidget cross-platform installer

Linux or macOS:
${unixInstallCommand}

Windows PowerShell:
${windowsInstallCommand}

The installer detects your operating system and CPU architecture, verifies the
matching GitHub release, preserves existing configuration and data, and prompts
for required setup values.`, 200);
    }

    return text("Not found\n", 404);
  }
};
