#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
macos="$ROOT/server/deploy/install-macos.sh"
windows="$ROOT/server/deploy/install-windows.ps1"

# The macOS exec receives the token only through its environment.
grep -Fq 'USAGEWIDGET_TOKEN="$USAGEWIDGET_TOKEN" exec "$ROOT/bin/usagewidget-qr" -url "$public_url"' "$macos"
if grep -Eq -- '(^|[[:space:]])--?token([=[:space:]]|$)' "$macos" "$windows"; then
  echo 'QR launchers must not pass a token argument' >&2
  exit 1
fi

# Windows must restore the caller's environment even if invoking QR fails.
grep -Fq '$previousToken = $env:USAGEWIDGET_TOKEN' "$windows"
grep -Fq '$env:USAGEWIDGET_TOKEN = $config.USAGEWIDGET_TOKEN' "$windows"
grep -Fq '& $qr -url $publicUrl' "$windows"
grep -Fq '} finally {' "$windows"
grep -Fq '$env:USAGEWIDGET_TOKEN = $previousToken' "$windows"

echo 'QR launcher tests passed'
