#!/usr/bin/env python3
"""Print an xcodebuild -destination for the newest available iPhone simulator.

UsageWidget's deployment target is iOS 26, so older runtimes are skipped.
"""

from __future__ import annotations

import json
import re
import subprocess
import sys


def main() -> int:
    raw = subprocess.check_output(
        ["xcrun", "simctl", "list", "devices", "available", "-j"]
    )
    data = json.loads(raw)
    candidates: list[tuple[int, int, int, str, str]] = []
    for runtime, devices in data.get("devices", {}).items():
        match = re.search(r"iOS-(\d+)-(\d+)", runtime)
        if not match:
            continue
        major, minor = int(match.group(1)), int(match.group(2))
        if major < 26:
            continue
        for device in devices:
            name = device.get("name", "")
            if "iPhone" not in name:
                continue
            if device.get("isAvailable") is False:
                continue
            number = re.search(r"iPhone (\d+)", name)
            rank = int(number.group(1)) if number else 0
            candidates.append((major, minor, rank, name, device["udid"]))
    if not candidates:
        sys.stderr.write("No available iPhone simulator with iOS 26+.\n")
        return 1
    candidates.sort()
    major, minor, _rank, name, udid = candidates[-1]
    sys.stderr.write(f"Selected {name} (iOS {major}.{minor}) {udid}\n")
    sys.stdout.write(f"platform=iOS Simulator,id={udid}\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
