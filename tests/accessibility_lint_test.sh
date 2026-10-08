#!/usr/bin/env bash
# Fail if app/widget text uses a hard-coded point size that ignores Dynamic Type.
# Use a text style (.font(.title3)), or @ScaledMetric for a custom size
# (see CapacityPercentText in ios/Sources/App/DashboardView.swift).
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"

python3 - "$root" <<'PY'
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
patterns = [
    # .system(size: 30 ...) / Font.system(size: 12) with a numeric literal
    (re.compile(r"\.system\(\s*size:\s*[0-9]"), "fixed .system(size:) literal"),
    # .custom("Name", size: 14) without relativeTo:/fixedSize opt-out
    (re.compile(r"\.custom\([^)]*size:\s*[0-9][^)]*\)"), "fixed .custom(size:) literal"),
    (re.compile(r"UIFont\.systemFont\(ofSize:\s*[0-9]"), "fixed UIFont size"),
]
errors = []
for path in sorted((root / "ios/Sources").rglob("*.swift")):
    for lineno, line in enumerate(path.read_text().splitlines(), 1):
        if line.strip().startswith("//"):
            continue
        for pattern, what in patterns:
            if pattern.search(line) and "relativeTo:" not in line:
                errors.append(f"{path.relative_to(root)}:{lineno}: {what}: {line.strip()}")
if errors:
    print("\n".join(f"FAIL: {e}" for e in errors), file=sys.stderr)
    sys.exit(1)
print("ok: no fixed-size fonts in ios/Sources")
PY
