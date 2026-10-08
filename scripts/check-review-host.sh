#!/usr/bin/env bash
# Manual check that the App Review backend is live. NOT part of required CI:
# it depends on the Worker being deployed and on Cloudflare DNS.
#
#   scripts/check-review-host.sh                   # https://<REVIEW_HOST> from review-worker/src/token.ts
#   scripts/check-review-host.sh http://127.0.0.1:8787   # e.g. against `wrangler dev`
#
# Exit 0 only when GET /v1/health with the review token returns 200 and
# "service":"ok". Prints what went wrong otherwise.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
token_ts="${root}/review-worker/src/token.ts"

ts_const() {
  sed -n "s/^export const $1 = \"\\(.*\\)\";\$/\\1/p" "${token_ts}"
}

token="$(ts_const REVIEW_TOKEN)"
host="$(ts_const REVIEW_HOST)"
if [ -z "${token}" ] || [ -z "${host}" ]; then
  echo "FAIL: could not read REVIEW_TOKEN/REVIEW_HOST from ${token_ts}" >&2
  exit 2
fi

base="${1:-https://${host}}"
base="${base%/}"
url="${base}/v1/health"

body_file="$(mktemp)"
trap 'rm -f -- "${body_file}"' EXIT

status="$(curl -sS -o "${body_file}" -w '%{http_code}' --max-time 15 \
  -H "Authorization: Bearer ${token}" -H 'Accept: application/json' "${url}" 2>"${body_file}.err")" || {
  echo "FAIL: ${url} unreachable: $(cat "${body_file}.err")" >&2
  rm -f -- "${body_file}.err"
  exit 1
}
rm -f -- "${body_file}.err"

case "${status}" in
  200)
    if grep -q '"service":"ok"' "${body_file}"; then
      echo "ok: ${url} -> 200, service ok"
      exit 0
    fi
    echo "FAIL: ${url} -> 200 but body is not a UsageWidget health response:" >&2
    head -c 300 "${body_file}" >&2; echo >&2
    exit 1
    ;;
  401)
    echo "FAIL: ${url} -> 401: the deployed Worker does not accept REVIEW_TOKEN from token.ts (redeploy review-worker)." >&2
    ;;
  530|52[0-9])
    echo "FAIL: ${url} -> ${status}: Cloudflare cannot reach an origin for ${host}." >&2
    echo "      The review Worker is most likely not deployed or its custom domain is not attached." >&2
    echo "      Deploy bot: cd review-worker && npx wrangler deploy" >&2
    ;;
  *)
    echo "FAIL: ${url} -> HTTP ${status}" >&2
    head -c 300 "${body_file}" >&2; echo >&2
    ;;
esac
exit 1
