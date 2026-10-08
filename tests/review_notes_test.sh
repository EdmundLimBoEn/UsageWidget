#!/usr/bin/env bash
# Keeps metadata/review/notes.txt (App Store Connect "Notes" for App Review)
# honest: the review host and token are read from review-worker/src/token.ts,
# never from a hardcoded copy, and every quoted UI label must exist in the app.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"

python3 - "$root" <<'PY'
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
errors = []

def ts_const(source: str, name: str) -> str:
    match = re.search(rf'export\s+const\s+{name}\s*=\s*"([^"]+)"\s*;', source)
    if not match:
        sys.exit(f"FAIL: could not find `export const {name} = \"...\"` in review-worker/src/token.ts")
    return match.group(1)

token_ts = (root / "review-worker/src/token.ts").read_text()
token = ts_const(token_ts, "REVIEW_TOKEN")
host = ts_const(token_ts, "REVIEW_HOST")
if len(token) < 32:
    errors.append(f"REVIEW_TOKEN must be >= 32 chars for the iOS parser, got {len(token)}")

notes_path = root / "metadata/review/notes.txt"
notes = notes_path.read_text()

url_line = re.search(r"^Server URL:\s*(\S+)\s*$", notes, re.M)
token_line = re.search(r"^Bearer token:\s*(\S+)\s*$", notes, re.M)
if not url_line:
    errors.append("notes need a 'Server URL: https://...' line")
elif url_line.group(1) != f"https://{host}":
    errors.append(f"notes Server URL {url_line.group(1)!r} != https://{host} from token.ts")
if not token_line:
    errors.append("notes need a 'Bearer token: ...' line")
elif token_line.group(1) != token:
    errors.append(f"notes Bearer token {token_line.group(1)!r} != REVIEW_TOKEN from token.ts")

# No stale second host/token anywhere else in the notes.
for other in set(re.findall(r"https://([A-Za-z0-9.-]+)", notes)) - {host, "github.com"}:
    errors.append(f"unexpected host in notes: {other}")

# App Store Connect caps the review Notes field at 4000 characters.
if len(notes) > 4000:
    errors.append(f"notes are {len(notes)} chars; App Store Connect allows 4000")

lower = notes.lower()
for phrase in ["self-hosted", "no account", "no in-app purchase", "sample data"]:
    if phrase not in lower:
        errors.append(f"notes must mention {phrase!r}")

# Every "quoted" UI label the reviewer is told to look for must be a Swift string literal.
swift = "\n".join(p.read_text() for p in (root / "ios/Sources").rglob("*.swift"))
for label in re.findall(r'"([^"\n]+)"', notes):
    if f'"{label}"' not in swift:
        errors.append(f'notes reference UI label "{label}" but no Swift source contains it')

# Human-facing READMEs must point reviewers at the same host and token.
for readme in ["README.md", "review-worker/README.md"]:
    text = (root / readme).read_text()
    if f"https://{host}" not in text:
        errors.append(f"{readme} does not mention https://{host}")
    if token not in text:
        errors.append(f"{readme} does not mention REVIEW_TOKEN")

# The review-worker tests must target the same host the notes advertise.
api_test = (root / "review-worker/test/api.test.ts").read_text()
if f"https://{host}" not in api_test:
    errors.append("review-worker/test/api.test.ts does not exercise REVIEW_HOST")

if errors:
    for e in errors:
        print(f"FAIL: {e}", file=sys.stderr)
    sys.exit(1)
print(f"ok: review notes match token.ts (host {host}) and app labels")
PY
