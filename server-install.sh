#!/usr/bin/env bash
# UsageWidget lifecycle installer for Ubuntu/Debian systemd hosts.
set -euo pipefail
umask 077

PREFIX=/opt/usagewidget
CONFIG_DIR=/etc/usagewidget
DATA_DIR=/var/lib/usagewidget
ENV_FILE="$CONFIG_DIR/env"
COLLECTOR_ENV="$CONFIG_DIR/collector.env"
BACKUP_DIR="$DATA_DIR/backups"
LOCAL_URL=http://127.0.0.1:8377
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFEST="$SCRIPT_DIR/release-manifest.json"
ACTION=install
REQUESTED_VERSION=""
PUBLIC_URL=""
COLLECTOR_USER=""
JSON=false
PURGE=false
YES=false
RESTORE_FILE=""
INCLUDE_APNS=false

say() { printf '%s\n' "$*"; }
warn() { printf 'usagewidget: warning: %s\n' "$*" >&2; }
die() { printf 'usagewidget: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "required tool not found: $1"; }
require_root() { [[ ${EUID:-$(id -u)} -eq 0 ]] || die "run this command as root (sudo server-install.sh ${ACTION})"; }

usage() {
  cat <<'EOF'
Usage: sudo ./server-install.sh [install] [--public-url URL] [--collector-user USER]
       sudo ./server-install.sh update [--version VERSION]
       sudo ./server-install.sh doctor [--json]
       sudo ./server-install.sh backup [--include-apns-key]
       sudo ./server-install.sh restore [--file ARCHIVE]
       sudo ./server-install.sh rotate-token
       sudo ./server-install.sh qr
       sudo ./server-install.sh uninstall [--purge --yes]
EOF
}

parse_args() {
  if [[ $# -gt 0 && $1 != --* ]]; then ACTION=$1; shift; fi
  case "$ACTION" in install|update|doctor|backup|restore|rotate-token|qr|uninstall) ;; *) usage; die "unknown action: $ACTION" ;; esac
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --version) [[ $# -ge 2 ]] || die "--version needs a value"; REQUESTED_VERSION=$2; shift 2 ;;
      --public-url) [[ $# -ge 2 ]] || die "--public-url needs a value"; PUBLIC_URL=${2%/}; shift 2 ;;
      --collector-user) [[ $# -ge 2 ]] || die "--collector-user needs a value"; COLLECTOR_USER=$2; shift 2 ;;
      --json) JSON=true; shift ;;
      --purge) PURGE=true; shift ;;
      --yes) YES=true; shift ;;
      --file) [[ $# -ge 2 ]] || die "--file needs a value"; RESTORE_FILE=$2; shift 2 ;;
      --include-apns-key) INCLUDE_APNS=true; shift ;;
      -h|--help) usage; exit 0 ;;
      *) die "unknown option: $1" ;;
    esac
  done
}

check_platform() {
  [[ -r /etc/os-release ]] || die "cannot identify operating system"
  # shellcheck disable=SC1091
  . /etc/os-release
  case "${ID:-}:${VERSION_ID:-}" in ubuntu:22.04|ubuntu:24.04|debian:12) ;; *) die "supported hosts are Ubuntu 22.04/24.04 and Debian 12 (found ${ID:-unknown} ${VERSION_ID:-unknown})" ;; esac
  map_arch "$(uname -m)"
  [[ -d /run/systemd/system ]] || die "systemd is required"
  for tool in curl jq sha256sum tar install openssl systemctl ss; do need "$tool"; done
  if ! ss -ltn 2>/dev/null | awk '{print $4}' | grep -Eq '(^|:)8377$'; then :; elif ! systemctl is-active --quiet usagewidget 2>/dev/null; then die "loopback port 8377 is already in use"; fi
}

map_arch() {
  case "$1" in x86_64) HOST_ARCH=amd64 ;; aarch64|arm64) HOST_ARCH=arm64 ;; *) die "supported architectures are amd64 and arm64" ;; esac
}

read_env() { local key=$1; [[ -r $ENV_FILE ]] || return 1; sed -n "s/^${key}=//p" "$ENV_FILE" | tail -1; }
health_ok() {
  local token code
  token="$(read_env USAGEWIDGET_TOKEN || true)"; [[ ${#token} -ge 32 ]] || return 1
  code="$(curl -s --max-time 12 -o /tmp/usagewidget-health.$$ -w '%{http_code}' -H "Authorization: Bearer $token" "$LOCAL_URL/v1/health" || true)"
  [[ $code == 200 ]] && jq -e '.service=="ok" and .database==true' /tmp/usagewidget-health.$$ >/dev/null 2>&1
  local result=$?; rm -f /tmp/usagewidget-health.$$; return "$result"
}

wait_for_health() {
  local attempts="${1:-30}"
  for _ in $(seq 1 "$attempts"); do
    if health_ok; then return 0; fi
    sleep 1
  done
  return 1
}

select_collector_user() {
  if [[ -z $COLLECTOR_USER && -n ${SUDO_USER:-} && ${SUDO_USER} != root ]]; then COLLECTOR_USER=$SUDO_USER; fi
  if [[ -z $COLLECTOR_USER ]]; then COLLECTOR_USER="$(getent passwd | awk -F: '$3>=1000 && $3<65534 && $7 !~ /(nologin|false)$/ {print $1; exit}')"; fi
  [[ -n $COLLECTOR_USER ]] || die "no unprivileged collector user found; pass --collector-user USER"
  local uid; uid="$(id -u "$COLLECTOR_USER" 2>/dev/null || true)"; [[ -n $uid && $uid -ne 0 ]] || die "collector user must be an existing unprivileged account"
}

detect_public_url() {
  if [[ -n $PUBLIC_URL ]]; then [[ $PUBLIC_URL == https://* ]] || die "--public-url must be HTTPS"; return; fi
  need tailscale
  local dns; dns="$(tailscale status --json 2>/dev/null | jq -r '.Self.DNSName // empty' | sed 's/\.$//')"
  [[ -n $dns ]] || die "Tailscale must already be logged in; run tailscale up first or pass --public-url"
  PUBLIC_URL="https://${dns}/usagewidget"
}

install_codexbar_if_needed() {
  local existing=""; existing="$(command -v codexbar 2>/dev/null || command -v CodexBarCLI 2>/dev/null || true)"
  if [[ -n $existing ]]; then CODEXBAR_BIN=$existing; return; fi
  [[ -r $MANIFEST ]] || die "release manifest is missing: $MANIFEST"
  local version asset digest url tmp
  version="$(jq -er '.codexbar.version' "$MANIFEST")"
  asset="$(jq -er --arg a "$HOST_ARCH" '.codexbar.linux[$a].asset' "$MANIFEST")"
  digest="$(jq -er --arg a "$HOST_ARCH" '.codexbar.linux[$a].sha256' "$MANIFEST")"
  url="https://github.com/steipete/CodexBar/releases/download/v${version}/${asset}"
  tmp="$(mktemp -d)"
  say "Downloading pinned CodexBar ${version} for ${HOST_ARCH}…"
  curl -fL --retry 3 -o "$tmp/$asset" "$url"
  printf '%s  %s\n' "$digest" "$tmp/$asset" | sha256sum -c - >/dev/null || die "CodexBar checksum verification failed"
  install -d -m 0755 "$PREFIX/dependencies/codexbar-$version-$HOST_ARCH"
  tar -xzf "$tmp/$asset" -C "$PREFIX/dependencies/codexbar-$version-$HOST_ARCH"
  CODEXBAR_BIN="$PREFIX/dependencies/codexbar-$version-$HOST_ARCH/codexbar"
  [[ -x $CODEXBAR_BIN ]] || CODEXBAR_BIN="$PREFIX/dependencies/codexbar-$version-$HOST_ARCH/CodexBarCLI"
  [[ -x $CODEXBAR_BIN ]] || die "CodexBar archive did not contain an executable"
  ln -sfn "$CODEXBAR_BIN" "$PREFIX/dependencies/codexbar"
  rm -rf -- "$tmp"
}

configure_apns() {
  grep -q '^APNS_KEY_PATH=' "$ENV_FILE" && return 0
  [[ -t 0 ]] || return 0
  local answer key_path key_id team_id bundle_id environment
  read -r -p "Configure APNs push notifications now? [y/N] " answer
  [[ $answer == y || $answer == Y ]] || return 0
  read -r -p "Path to AuthKey_*.p8: " key_path
  read -r -p "APNs key ID: " key_id
  read -r -p "Apple developer team ID: " team_id
  read -r -p "App bundle ID: " bundle_id
  read -r -p "APNs environment [sandbox/production] (sandbox): " environment
  environment=${environment:-sandbox}
  [[ -r $key_path && -n $key_id && -n $team_id && -n $bundle_id ]] || die "APNs values are incomplete"
  [[ $environment == sandbox || $environment == production ]] || die "APNs environment must be sandbox or production"
  install -m 0640 -o root -g usagewidget "$key_path" "$CONFIG_DIR/AuthKey.p8"
  {
    printf 'APNS_KEY_PATH=%s/AuthKey.p8\n' "$CONFIG_DIR"
    printf 'APNS_KEY_ID=%s\n' "$key_id"
    printf 'APNS_TEAM_ID=%s\n' "$team_id"
    printf 'APNS_BUNDLE_ID=%s\n' "$bundle_id"
    printf 'APNS_ENV=%s\n' "$environment"
  } >>"$ENV_FILE"
}

validate_collector() {
  local home; home="$(getent passwd "$COLLECTOR_USER" | cut -d: -f6)"
  if ! runuser -u "$COLLECTOR_USER" -- env HOME="$home" "$CODEXBAR_BIN" config validate >/dev/null; then die "CodexBar configuration is invalid for $COLLECTOR_USER"; fi
  if ! runuser -u "$COLLECTOR_USER" -- env HOME="$home" "$CODEXBAR_BIN" usage --format json >/dev/null; then
    die "CodexBar found no usable provider session. Log in to Codex/Claude as '$COLLECTOR_USER', then rerun the installer; credentials are never collected here"
  fi
}

write_initial_env() {
  install -d -m 0750 -o usagewidget -g usagewidget "$CONFIG_DIR" "$DATA_DIR"
  if [[ ! -e $ENV_FILE ]]; then
    local token; token="$(openssl rand -hex 32)"
    install -m 0600 -o root -g usagewidget /dev/null "$ENV_FILE"
    {
      printf 'USAGEWIDGET_TOKEN=%s\n' "$token"
      printf 'USAGEWIDGET_PUBLIC_URL=%s\n' "$PUBLIC_URL"
      printf 'DB_PATH=%s/usagewidget.db\n' "$DATA_DIR"
      printf 'LISTEN_ADDR=127.0.0.1:8377\n'
      printf 'COLLECTOR_SOCKET=/run/usagewidget/codexbar.sock\n'
    } >>"$ENV_FILE"
  elif ! grep -q '^USAGEWIDGET_PUBLIC_URL=' "$ENV_FILE"; then printf 'USAGEWIDGET_PUBLIC_URL=%s\n' "$PUBLIC_URL" >>"$ENV_FILE"; fi
  if [[ ! -e $COLLECTOR_ENV ]]; then printf 'CODEXBAR_BIN=%s\nCOLLECTOR_SOCKET=/run/usagewidget/codexbar.sock\n' "$CODEXBAR_BIN" >"$COLLECTOR_ENV"; chmod 0640 "$COLLECTOR_ENV"; chown root:usagewidget "$COLLECTOR_ENV"; fi
}

install_release() {
  [[ -r $MANIFEST ]] || die "release manifest missing"
  local version release_dir previous_target=""
  version="${REQUESTED_VERSION:-$(jq -r '.version' "$MANIFEST")}"; [[ $version != null && -n $version ]] || die "release version is missing"
  [[ -x $SCRIPT_DIR/bin/usagewidgetd && -x $SCRIPT_DIR/bin/usagewidget-collector ]] || die "run the installer from an extracted UsageWidget release bundle"
  if [[ -r $SCRIPT_DIR/CHECKSUMS.sha256 ]]; then (cd "$SCRIPT_DIR" && sha256sum -c CHECKSUMS.sha256 >/dev/null) || die "release bundle checksum verification failed"; fi
  release_dir="$PREFIX/releases/$version"; install -d -m 0755 "$release_dir/bin" "$release_dir/deploy"
  install -m 0755 "$SCRIPT_DIR/bin/usagewidgetd" "$SCRIPT_DIR/bin/usagewidget-collector" "$release_dir/bin/"
  install -m 0755 "$SCRIPT_DIR/usagewidget" "$release_dir/bin/usagewidget"
  install -m 0644 "$SCRIPT_DIR/deploy/usagewidget.service" "$SCRIPT_DIR/deploy/usagewidget-collector.service" "$release_dir/deploy/"
  install -m 0755 "$SCRIPT_DIR/server-install.sh" "$release_dir/server-install.sh"
  install -m 0644 "$MANIFEST" "$release_dir/release-manifest.json"
  [[ -L $PREFIX/current ]] && previous_target="$(readlink -f "$PREFIX/current")"
  [[ -n $previous_target ]] && ln -sfn "$previous_target" "$PREFIX/previous"
  ln -sfn "$release_dir" "$PREFIX/current.new"; mv -Tf "$PREFIX/current.new" "$PREFIX/current"
  ln -sfn "$PREFIX/current/bin/usagewidgetd" /usr/local/bin/usagewidgetd
  ln -sfn "$PREFIX/current/bin/usagewidget-collector" /usr/local/bin/usagewidget-collector
  ln -sfn "$PREFIX/current/bin/usagewidget" /usr/local/bin/usagewidget
  ln -sfn "$PREFIX/current/server-install.sh" /usr/local/bin/usagewidget-admin
  install -m 0644 "$release_dir/deploy/usagewidget.service" /etc/systemd/system/usagewidget.service
  install -m 0644 "$release_dir/deploy/usagewidget-collector.service" /etc/systemd/system/usagewidget-collector.service
  install -d -m 0755 /etc/systemd/system/usagewidget-collector.service.d
  printf '[Service]\nUser=%s\nGroup=usagewidget\n' "$COLLECTOR_USER" >/etc/systemd/system/usagewidget-collector.service.d/user.conf
  systemctl daemon-reload; systemctl enable --now usagewidget-collector usagewidget
  if ! wait_for_health 30; then
    if [[ -n $previous_target ]]; then warn "health check failed; rolling back to $previous_target"; ln -sfn "$previous_target" "$PREFIX/current.new"; mv -Tf "$PREFIX/current.new" "$PREFIX/current"; systemctl restart usagewidget-collector usagewidget; fi
    die "installed version failed its local health check"
  fi
}

configure_serve() {
  [[ $PUBLIC_URL == *".ts.net/usagewidget" ]] || return 0
  tailscale serve --bg --yes --https=443 --set-path=/usagewidget http://127.0.0.1:8377 >/dev/null
}

print_qr() {
  need jq; need qrencode
  local url token encoded_url encoded_token payload
  url="$(read_env USAGEWIDGET_PUBLIC_URL || true)"; token="$(read_env USAGEWIDGET_TOKEN || true)"
  [[ $url == https://* && ${#token} -ge 32 ]] || die "server URL or bearer token is missing"
  encoded_url="$(printf '%s' "$url" | jq -sRr @uri)"
  encoded_token="$(printf '%s' "$token" | jq -sRr @uri)"
  payload="usagewidget://configure?v=1&server=${encoded_url}&token=${encoded_token}"
  warn "This QR grants full single-operator access to the server. Keep it private."
  printf '%s' "$payload" | qrencode -t ANSIUTF8
  unset payload token
}

do_install() {
  check_platform; select_collector_user; detect_public_url
  getent group usagewidget >/dev/null || groupadd --system usagewidget
  id usagewidget >/dev/null 2>&1 || useradd --system --gid usagewidget --home-dir "$DATA_DIR" --shell /usr/sbin/nologin usagewidget
  install -d -m 0755 "$PREFIX/releases" "$PREFIX/dependencies"
  install_codexbar_if_needed; validate_collector; write_initial_env; configure_apns; install_release; configure_serve
  say "UsageWidget is healthy at $PUBLIC_URL"
  if command -v qrencode >/dev/null; then print_qr; else warn "install qrencode, then run: sudo usagewidget-admin qr"; fi
  if ! grep -q '^APNS_KEY_PATH=' "$ENV_FILE"; then warn "APNs is not configured; installed in dashboard-only mode. Add APNS_KEY_PATH, APNS_KEY_ID, APNS_TEAM_ID, and APNS_BUNDLE_ID when ready."; fi
}

do_update() {
  check_platform
  local version url tmp arch_name asset
  if [[ -z $REQUESTED_VERSION ]]; then REQUESTED_VERSION="$(curl -fsSL https://api.github.com/repos/EdmundLimBoEn/UsageWidget/releases/latest | jq -er '.tag_name' | sed 's/^v//')"; fi
  version=$REQUESTED_VERSION; arch_name=$HOST_ARCH
  asset="usagewidget-${version}-linux-${arch_name}.tar.gz"
  url="${USAGEWIDGET_RELEASE_BASE_URL:-https://github.com/EdmundLimBoEn/UsageWidget/releases/download/v${version}}/${asset}"
  tmp="$(mktemp -d)"; trap 'rm -rf -- "$tmp"' EXIT
  curl -fL --retry 3 -o "$tmp/$asset" "$url"
  curl -fL --retry 3 -o "$tmp/$asset.sha256" "$url.sha256"
  (cd "$tmp" && sha256sum -c "$asset.sha256" >/dev/null) || die "release archive checksum verification failed"
  tar -xzf "$tmp/$asset" -C "$tmp"
  exec "$tmp/usagewidget-$version-linux-$arch_name/server-install.sh" install --version "$version" --collector-user "$(systemctl show usagewidget-collector -p User --value)" --public-url "$(read_env USAGEWIDGET_PUBLIC_URL)"
}

do_doctor() {
  local service=false database=false schema=0 version=unknown collector=unknown apns=false
  systemctl is-active --quiet usagewidget 2>/dev/null && service=true
  if health_ok; then local token; token="$(read_env USAGEWIDGET_TOKEN)"; local health; health="$(curl -fsS -H "Authorization: Bearer $token" "$LOCAL_URL/v1/health")"; schema="$(jq -r '.schemaVersion // 0' <<<"$health")"; version="$(jq -r '.version // "unknown"' <<<"$health")"; collector="$(jq -r '.collector.status // "unknown"' <<<"$health")"; apns="$(jq -r '.apns' <<<"$health")"; database=true; fi
  if $JSON; then jq -n --argjson service "$service" --argjson database "$database" --argjson schema "$schema" --arg version "$version" --arg collector "$collector" --argjson apns "$apns" '{service:$service,database:$database,schemaVersion:$schema,version:$version,collector:$collector,apns:$apns,credentials:"redacted"}'
  else say "service: $service"; say "database: $database (schema $schema)"; say "version: $version"; say "collector: $collector"; say "apns: $apns"; say "credentials: redacted"; fi
  $service && $database
}

do_backup() {
  need sqlite3; install -d -m 0700 "$BACKUP_DIR"; local stamp work archive; stamp="$(date -u +%Y%m%dT%H%M%SZ)"; work="$(mktemp -d)"; archive="$BACKUP_DIR/usagewidget-$stamp.tar.gz"
  sqlite3 "$DATA_DIR/usagewidget.db" ".backup '$work/usagewidget.db'"; install -m 0600 "$ENV_FILE" "$work/env"
  if $INCLUDE_APNS; then local key; key="$(read_env APNS_KEY_PATH || true)"; [[ -n $key && -r $key ]] && install -m 0600 "$key" "$work/apns-key.p8"; fi
  tar -czf "$archive" -C "$work" .; rm -rf -- "$work"; say "$archive"
}

do_restore() {
  [[ -n $RESTORE_FILE ]] || RESTORE_FILE="$(find "$BACKUP_DIR" -maxdepth 1 -type f -name 'usagewidget-*.tar.gz' -print | sort | tail -1)"
  [[ -f $RESTORE_FILE ]] || die "backup archive not found"
  if tar -tzf "$RESTORE_FILE" | grep -Eq '(^/|(^|/)\.\.(/|$))'; then die "backup contains unsafe paths"; fi
  local work; work="$(mktemp -d)"; tar -xzf "$RESTORE_FILE" -C "$work"; [[ -f $work/usagewidget.db ]] || die "backup has no database"
  systemctl stop usagewidget; install -m 0600 -o usagewidget -g usagewidget "$work/usagewidget.db" "$DATA_DIR/usagewidget.db"; [[ -f $work/env ]] && install -m 0600 -o root -g usagewidget "$work/env" "$ENV_FILE"; [[ -f $work/apns-key.p8 ]] && install -m 0640 -o root -g usagewidget "$work/apns-key.p8" "$CONFIG_DIR/AuthKey.p8"; systemctl start usagewidget; rm -rf -- "$work"; wait_for_health 30 || die "restored server failed health check"; say "Restore complete"
}

do_rotate_token() {
  local old new tmp; old="$(read_env USAGEWIDGET_TOKEN)"; new="$(openssl rand -hex 32)"; tmp="$(mktemp "$CONFIG_DIR/env.XXXXXX")"
  awk -v token="$new" 'BEGIN{done=0} /^USAGEWIDGET_TOKEN=/{print "USAGEWIDGET_TOKEN=" token; done=1; next} {print} END{if(!done) print "USAGEWIDGET_TOKEN=" token}' "$ENV_FILE" >"$tmp"; chown root:usagewidget "$tmp"; chmod 0600 "$tmp"; mv -f "$tmp" "$ENV_FILE"; systemctl restart usagewidget
  if ! wait_for_health 30; then awk -v token="$old" '/^USAGEWIDGET_TOKEN=/{print "USAGEWIDGET_TOKEN=" token; next} {print}' "$ENV_FILE" >"$tmp"; mv -f "$tmp" "$ENV_FILE"; systemctl restart usagewidget; die "token rotation failed and was rolled back"; fi
  say "Token rotated. Every existing phone is now invalidated; scan the replacement QR."
  print_qr
}

do_uninstall() {
  systemctl disable --now usagewidget usagewidget-collector 2>/dev/null || true
  rm -f /etc/systemd/system/usagewidget.service /etc/systemd/system/usagewidget-collector.service /etc/systemd/system/usagewidget-collector.service.d/user.conf
  rmdir /etc/systemd/system/usagewidget-collector.service.d 2>/dev/null || true
  rm -f /usr/local/bin/usagewidgetd /usr/local/bin/usagewidget-collector /usr/local/bin/usagewidget /usr/local/bin/usagewidget-admin
  systemctl daemon-reload
  if $PURGE; then
    if ! $YES; then read -r -p "Permanently delete /opt/usagewidget, /etc/usagewidget, and /var/lib/usagewidget? [y/N] " answer; [[ $answer == y || $answer == Y ]] || die "purge cancelled"; fi
    rm -rf -- /opt/usagewidget /etc/usagewidget /var/lib/usagewidget
    say "Purged exact UsageWidget configuration, releases, and data paths; this is not recoverable unless backed up."
  else rm -rf -- /opt/usagewidget; say "Uninstalled binaries. Configuration and data were preserved in /etc/usagewidget and /var/lib/usagewidget."; fi
}

main() {
  parse_args "$@"; require_root
  case "$ACTION" in install) do_install ;; update) do_update ;; doctor) do_doctor ;; backup) do_backup ;; restore) do_restore ;; rotate-token) do_rotate_token ;; qr) print_qr ;; uninstall) do_uninstall ;; esac
}
if [[ ${BASH_SOURCE[0]} == "$0" ]]; then main "$@"; fi
