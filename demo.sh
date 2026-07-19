#!/usr/bin/env bash
# Build a real release and install it inside an isolated Linux systemd container.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONTAINER="${USAGEWIDGET_DEMO_CONTAINER:-usagewidget-installer-demo}"
ACTION="${1:-up}"
DISTRO="${2:-ubuntu24}"
VERSION="demo-local"

say() { printf 'demo: %s\n' "$*"; }
die() { printf 'demo: %s\n' "$*" >&2; exit 1; }
need_docker() { command -v docker >/dev/null 2>&1 || die "Docker is required"; docker info >/dev/null 2>&1 || die "Docker daemon is not running"; }

image_for() {
  case "$1" in
    ubuntu22) BASE_IMAGE=ubuntu:22.04 ;;
    ubuntu24) BASE_IMAGE=ubuntu:24.04 ;;
    debian12) BASE_IMAGE=debian:12 ;;
    *) die "distro must be ubuntu22, ubuntu24, or debian12" ;;
  esac
  IMAGE="usagewidget-demo-${1}:local"
}

docker_arch() {
  case "$(docker info --format '{{.Architecture}}')" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) die "Docker reports an unsupported architecture" ;;
  esac
}

wait_for_systemd() {
  for _ in $(seq 1 30); do
    if docker exec "$CONTAINER" systemctl is-system-running --wait >/dev/null 2>&1; then return 0; fi
    state="$(docker exec "$CONTAINER" systemctl is-system-running 2>/dev/null || true)"
    [[ $state == degraded ]] && return 0
    sleep 1
  done
  docker logs "$CONTAINER" >&2 || true
  die "systemd did not become ready"
}

container_token() {
  docker exec "$CONTAINER" sed -n 's/^USAGEWIDGET_TOKEN=//p' /etc/usagewidget/env
}

verify_install() {
  local token code
  token="$(container_token)"; [[ ${#token} -ge 32 ]] || die "installer did not create a bearer token"
  code="$(docker exec "$CONTAINER" curl -sS -o /tmp/demo-poll.json -w '%{http_code}' -X POST -H "Authorization: Bearer $token" http://127.0.0.1:8377/v1/poll)"
  [[ $code == 200 ]] || { docker exec "$CONTAINER" cat /tmp/demo-poll.json >&2; die "poll returned HTTP $code"; }
  docker exec "$CONTAINER" jq -e '.success == true' /tmp/demo-poll.json >/dev/null
  docker exec "$CONTAINER" usagewidget-admin doctor --json | jq -e '.service == true and .database == true and .collector == "ok"' >/dev/null
  docker exec "$CONTAINER" systemctl is-active --quiet usagewidget usagewidget-collector
  docker exec "$CONTAINER" grep -q -- '--set-path=/usagewidget' /tmp/usagewidget-demo-tailscale-serve.log
  say "verified health, collector poll, schema, services, and Tailscale Serve route"
}

up() {
  need_docker; image_for "$DISTRO"
  local arch work bundle
  arch="$(docker_arch)"; work="$(mktemp -d)"
  DEMO_WORK="$work"
  trap 'rm -rf -- "${DEMO_WORK:-/tmp/usagewidget-demo-noop}"' EXIT
  say "building $BASE_IMAGE systemd image"
  docker build --build-arg "BASE_IMAGE=$BASE_IMAGE" -f "$ROOT/demo/Dockerfile.systemd" -t "$IMAGE" "$ROOT/demo"
  if docker container inspect "$CONTAINER" >/dev/null 2>&1; then
    say "replacing existing container $CONTAINER"
    docker container rm --force "$CONTAINER" >/dev/null
  fi
  docker run --detach --name "$CONTAINER" --hostname usagewidget-demo \
    --privileged --cgroupns=host --tmpfs /run --tmpfs /run/lock \
    -v /sys/fs/cgroup:/sys/fs/cgroup:rw "$IMAGE" >/dev/null
  wait_for_systemd

  say "building the real linux/$arch release bundle"
  USAGEWIDGET_DIST_DIR="$work/dist" "$ROOT/scripts/package-release.sh" "$VERSION" "$arch" >/dev/null
  bundle="$work/dist/usagewidget-${VERSION}-linux-${arch}"
  docker cp "$bundle" "$CONTAINER:/tmp/usagewidget-release"
  rm -rf -- "$work"; DEMO_WORK=""
  say "running server-install.sh inside $DISTRO"
  docker exec "$CONTAINER" /tmp/usagewidget-release/server-install.sh install --collector-user demo >/tmp/usagewidget-demo-install.log
  verify_install
  say "ready. Commands: ./demo.sh doctor | qr | shell | down"
}

doctor() { need_docker; docker exec "$CONTAINER" usagewidget-admin doctor --json; }
qr() { need_docker; docker exec -it "$CONTAINER" usagewidget-admin qr; }
shell_into() { need_docker; docker exec -it "$CONTAINER" bash; }
down() {
  need_docker
  if docker container inspect "$CONTAINER" >/dev/null 2>&1; then docker container rm --force "$CONTAINER" >/dev/null; fi
  say "removed $CONTAINER"
}

matrix() {
  for candidate in ubuntu22 ubuntu24 debian12; do
    DISTRO=$candidate; up; down
  done
  say "all supported distro demos passed"
}

case "$ACTION" in
  up) up ;;
  doctor) doctor ;;
  qr) qr ;;
  shell) shell_into ;;
  down) down ;;
  matrix) need_docker; matrix ;;
  *) die "usage: ./demo.sh [up [ubuntu22|ubuntu24|debian12]|doctor|qr|shell|down|matrix]" ;;
esac
