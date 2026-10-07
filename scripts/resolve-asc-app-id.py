#!/usr/bin/env python3
"""Pick a unique App Store Connect app id from `asc apps list` JSON on stdin."""

from __future__ import annotations

import json
import sys


def rows(payload: object) -> list[object]:
    if isinstance(payload, list):
        return payload
    if isinstance(payload, dict):
        for key in ("data", "apps", "items"):
            value = payload.get(key)
            if isinstance(value, list):
                return value
    return []


def bundle_id_of(row: object) -> str | None:
    if not isinstance(row, dict):
        return None
    attrs = row.get("attributes")
    if isinstance(attrs, dict) and isinstance(attrs.get("bundleId"), str):
        return attrs["bundleId"]
    if isinstance(row.get("bundleId"), str):
        return row["bundleId"]
    if isinstance(row.get("bundle_id"), str):
        return row["bundle_id"]
    return None


def app_id_of(row: object) -> str | None:
    if not isinstance(row, dict):
        return None
    for key in ("id", "appId", "app_id"):
        value = row.get(key)
        if isinstance(value, str) and value:
            return value
        if isinstance(value, int):
            return str(value)
    return None


def main() -> int:
    if len(sys.argv) != 2:
        sys.stderr.write("usage: resolve-asc-app-id.py <bundle-id>\n")
        return 2
    wanted = sys.argv[1]
    payload = json.load(sys.stdin)
    matches: list[str] = []
    collected = rows(payload)
    for row in collected:
        bundle = bundle_id_of(row)
        app_id = app_id_of(row)
        if not app_id:
            continue
        if bundle == wanted or (bundle is None and len(collected) == 1):
            matches.append(app_id)
    unique = list(dict.fromkeys(matches))
    if len(unique) != 1:
        sys.stderr.write(
            f"Could not resolve a unique App Store Connect app id for {wanted}: "
            f"{json.dumps(payload)[:2000]}\n"
        )
        return 1
    sys.stdout.write(unique[0] + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
