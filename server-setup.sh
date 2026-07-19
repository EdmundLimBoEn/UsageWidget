#!/usr/bin/env bash
# Build locally, then install UsageWidget on a remote Ubuntu/Debian server.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/usagewidget/env"
LOCAL_WORK=""
REMOTE_WORK=""
TARGET=""
REMOTE_SUDO=""

say() { printf 'usagewidget setup: %s\n' "$*"; }
die() { printf 'usagewidget setup: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "required local tool not found: $1"; }

usage() {
  cat <<'EOF'
Usage: ./server-setup.sh

Interactively asks for the SSH user, server IP/hostname, and collector user,
then builds, transfers, verifies, and installs UsageWidget on that machine.
EOF
}

if [[ ${1:-} == -h || ${1:-} == --help ]]; then usage; exit 0; fi
[[ $# -eq 0 ]] || { usage; die "this command takes no arguments"; }
[[ -r /dev/tty ]] || die "run this command from an interactive terminal"
for tool in ssh scp go jq sha256sum tar; do need "$tool"; done

default_target="${USAGEWIDGET_DEPLOY_HOST:-}"
if [[ -z $default_target && -r $CONFIG_FILE ]]; then
  default_target="$(sed -n 's/^USAGEWIDGET_DEPLOY_HOST=//p' "$CONFIG_FILE" | tail -1)"
fi
default_ssh_user=root
default_server=""
if [[ $default_target == *@* ]]; then
  default_ssh_user="${default_target%%@*}"
  default_server="${default_target#*@}"
fi

read -r -p "SSH user [$default_ssh_user]: " ssh_user </dev/tty
ssh_user="${ssh_user:-$default_ssh_user}"
server_prompt="Server IP or hostname"
[[ -n $default_server ]] && server_prompt+=" [$default_server]"
read -r -p "$server_prompt: " server </dev/tty
server="${server:-$default_server}"

[[ $ssh_user =~ ^[A-Za-z_][A-Za-z0-9._-]*$ ]] || die "SSH user contains unsupported characters"
[[ $server =~ ^[A-Za-z0-9][A-Za-z0-9.:-]*$ ]] || die "enter a plain IP address or hostname"
TARGET="${ssh_user}@${server}"

LOCAL_WORK="$(mktemp -d /tmp/usagewidget-setup.XXXXXX)"
SSH_OPTIONS=(-o "ControlMaster=auto" -o "ControlPersist=120" -o "ControlPath=$LOCAL_WORK/ssh-%C")

cleanup() {
  if [[ -n $REMOTE_WORK && $REMOTE_WORK =~ ^/tmp/usagewidget-setup\.[A-Za-z0-9]+$ && -n $TARGET ]]; then
    ssh "${SSH_OPTIONS[@]}" "$TARGET" "${REMOTE_SUDO:+$REMOTE_SUDO }rm -rf -- '$REMOTE_WORK'" >/dev/null 2>&1 || true
  fi
  [[ -z $LOCAL_WORK ]] || rm -rf -- "$LOCAL_WORK"
}
trap cleanup EXIT

say "connecting to $TARGET"
remote_uid="$(ssh "${SSH_OPTIONS[@]}" "$TARGET" id -u)"
if [[ $remote_uid == 0 ]]; then
  REMOTE_SUDO=""
else
  REMOTE_SUDO=sudo
  ssh "${SSH_OPTIONS[@]}" -t "$TARGET" sudo -v
fi

remote_arch="$(ssh "${SSH_OPTIONS[@]}" "$TARGET" uname -m)"
case "$remote_arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "server architecture must be amd64 or arm64 (found $remote_arch)" ;;
esac

detected_collector="$(ssh "${SSH_OPTIONS[@]}" "$TARGET" "getent passwd | awk -F: '\$3>=1000 && \$3<65534 && \$7 !~ /(nologin|false)\$/ {print \$1; exit}'")"
collector_prompt="Collector user"
[[ -n $detected_collector ]] && collector_prompt+=" [$detected_collector]"
read -r -p "$collector_prompt: " collector_user </dev/tty
collector_user="${collector_user:-$detected_collector}"
[[ $collector_user =~ ^[A-Za-z_][A-Za-z0-9._-]*$ ]] || die "collector user contains unsupported characters"
ssh "${SSH_OPTIONS[@]}" "$TARGET" "id -u '$collector_user'" >/dev/null || die "collector user does not exist on the server"

version="local-$(date -u +%Y%m%d%H%M%S)"
name="usagewidget-${version}-linux-${arch}"
say "building linux/$arch release $version"
USAGEWIDGET_DIST_DIR="$LOCAL_WORK/dist" "$ROOT/scripts/package-release.sh" "$version" "$arch" >/dev/null

REMOTE_WORK="$(ssh "${SSH_OPTIONS[@]}" "$TARGET" mktemp -d /tmp/usagewidget-setup.XXXXXX)"
[[ $REMOTE_WORK =~ ^/tmp/usagewidget-setup\.[A-Za-z0-9]+$ ]] || die "server returned an unsafe temporary path"
say "transferring verified release"
scp "${SSH_OPTIONS[@]}" \
  "$LOCAL_WORK/dist/$name.tar.gz" \
  "$LOCAL_WORK/dist/$name.tar.gz.sha256" \
  "$TARGET:$REMOTE_WORK/"

remote_command="set -euo pipefail
cleanup_remote() { ${REMOTE_SUDO:+$REMOTE_SUDO }rm -rf -- '$REMOTE_WORK'; }
trap cleanup_remote EXIT
${REMOTE_SUDO:+$REMOTE_SUDO }apt-get update
${REMOTE_SUDO:+$REMOTE_SUDO }apt-get install -y --no-install-recommends ca-certificates curl iproute2 jq openssl qrencode sqlite3 tar util-linux
cd '$REMOTE_WORK'
sha256sum -c '$name.tar.gz.sha256'
tar -xzf '$name.tar.gz'
${REMOTE_SUDO:+$REMOTE_SUDO }'./$name/server-install.sh' install --collector-user '$collector_user'"

say "installing on $TARGET as collector user $collector_user"
ssh "${SSH_OPTIONS[@]}" -t "$TARGET" "$remote_command"
REMOTE_WORK=""
say "complete; verify later with: ssh $TARGET sudo usagewidget-admin doctor --json"
