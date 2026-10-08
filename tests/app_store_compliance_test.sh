#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok: $*"; }

python3 - "$root" <<'PY'
import json
import pathlib
import plistlib
import re
import sys

root = pathlib.Path(sys.argv[1])
errors = []

def fail(msg):
    errors.append(msg)

privacy_path = root / "ios/Resources/PrivacyInfo.xcprivacy"
if not privacy_path.is_file():
    fail(f"missing {privacy_path}")
else:
    with privacy_path.open("rb") as fh:
        plist = plistlib.load(fh)
    if plist.get("NSPrivacyTracking") is not False:
        fail(f"NSPrivacyTracking must be false, got {plist.get('NSPrivacyTracking')!r}")
    collected = plist.get("NSPrivacyCollectedDataTypes")
    if collected != []:
        fail(f"NSPrivacyCollectedDataTypes must be [], got {collected!r}")
    api_types = plist.get("NSPrivacyAccessedAPITypes") or []
    user_defaults = next(
        (
            item
            for item in api_types
            if item.get("NSPrivacyAccessedAPIType") == "NSPrivacyAccessedAPICategoryUserDefaults"
        ),
        None,
    )
    if user_defaults is None:
        fail("privacy manifest does not declare NSPrivacyAccessedAPICategoryUserDefaults")
    else:
        reasons = user_defaults.get("NSPrivacyAccessedAPITypeReasons") or []
        if "1C8F.1" not in reasons:
            fail(f"UserDefaults reasons must include 1C8F.1 for the App Group, got {reasons!r}")
        if "CA92.1" not in reasons:
            fail(f"UserDefaults reasons must include CA92.1 for app-only defaults, got {reasons!r}")

sample = (root / "ios/Sources/Core/SampleCapacity.swift").read_text()
provider_ids = re.findall(r'Provider\(id: "([^"]+)"', sample)
if len(provider_ids) < 2:
    fail(f"SampleCapacity must declare multiple providers, found {provider_ids}")

yml = (root / "ios/project.yml").read_text()
privacy_hits = len(re.findall(r"Resources/PrivacyInfo\.xcprivacy", yml))
if privacy_hits < 2:
    fail(f"project.yml must wire PrivacyInfo.xcprivacy into app and widget, found {privacy_hits}")
if 'MARKETING_VERSION: "1.0"' not in yml:
    fail("project.yml MARKETING_VERSION must be 1.0")

pbx = (root / "ios/UsageWidget.xcodeproj/project.pbxproj").read_text()
if pbx.count("MARKETING_VERSION = 1.0;") < 2:
    fail("project.pbxproj MARKETING_VERSION must be 1.0")
if "PrivacyInfo.xcprivacy in Resources" not in pbx:
    fail("project.pbxproj must copy PrivacyInfo.xcprivacy into target resources")

assets = root / "ios/Resources/Assets.xcassets"
brand_names = ("codex", "claude", "grok", "cursor", "openai", "anthropic", "chatgpt", "copilot", "gemini", "xai")
image_ext = {".svg", ".png", ".pdf", ".jpg", ".jpeg", ".webp"}
for imageset in assets.glob("Provider*.imageset"):
    for child in imageset.iterdir():
        if child.suffix.lower() in image_ext:
            fail(f"brand-capable image remains in {child.relative_to(root)}")
        lower = child.name.lower()
        if any(name in lower for name in brand_names):
            fail(f"brand filename remains in {child.relative_to(root)}")

for path in assets.rglob("*"):
    if not path.is_file():
        continue
    lower = path.name.lower()
    if path.suffix.lower() in image_ext and any(name in lower for name in brand_names):
        if "appicon" in str(path).lower():
            continue
        fail(f"brand image file remains at {path.relative_to(root)}")

version_dir = root / "metadata/version/1.0"
if not version_dir.is_dir():
    fail("metadata/version/1.0 is missing")
legacy = root / "metadata/version/0.1"
if legacy.exists():
    fail("metadata/version/0.1 must be replaced by 1.0")

meta_path = version_dir / "en-US.json"
meta = json.loads(meta_path.read_text())
keywords = meta.get("keywords") or ""
if len(keywords) > 100:
    fail(f"App Store keywords exceed 100 characters ({len(keywords)})")
deny = {
    "codex",
    "claude",
    "grok",
    "openai",
    "anthropic",
    "chatgpt",
    "gpt",
    "cursor",
    "copilot",
    "gemini",
    "xai",
    "devin",
    "claude code",
}
tokens = [token.strip().lower() for token in keywords.split(",") if token.strip()]
for token in tokens:
    for mark in deny:
        if mark == token or mark in token.split() or mark in re.split(r"[\s/_-]+", token):
            fail(f"keywords contain trademark {mark!r} in token {token!r}")

description = (meta.get("description") or "").lower()
if "not affiliated" not in description:
    fail("version description must include a one-line unaffiliated disclaimer")
if "self-hosted" not in description and "companion server" not in description:
    fail("version description must describe the self-hosted companion server")

# App Store users get dashboard-only delivery unless they run their own server
# with APNs credentials. Metadata must not promise alerts unconditionally.
push_words = re.compile(r"\b(alert|alerts|notification|notifications|push|notify)\b", re.I)
raw_description = meta.get("description") or ""
for sentence in re.split(r"(?<=[.!?])\s+|\n+", raw_description):
    if push_words.search(sentence) and not ("apns" in sentence.lower() and "self-hosted" in sentence.lower()):
        fail(f"description promises alerts without the self-hosted APNs condition: {sentence.strip()!r}")
if "dashboard-only" not in description:
    fail("description must say UsageWidget is dashboard-only without a self-hosted APNs setup")
for field in ("promotionalText", "keywords"):
    if push_words.search(meta.get(field) or ""):
        fail(f"{field} must not advertise alerts/notifications: {meta.get(field)!r}")
app_info = json.loads((root / "metadata/app-info/en-US.json").read_text())
for field in ("name", "subtitle"):
    if push_words.search(app_info.get(field) or ""):
        fail(f"app-info {field} must not advertise alerts/notifications: {app_info.get(field)!r}")

# The in-app dashboard-only state must exist and must gate the permission prompt.
models = (root / "ios/Sources/Core/Models.swift").read_text()
if 'case .dashboardOnly: "Dashboard-only"' not in models:
    fail("Models.swift must define the Dashboard-only delivery title")
if "health?.apns != false" not in models:
    fail("offersNotificationPermission must be driven by Health.apns")
for view in ("ios/Sources/App/ReadinessView.swift", "ios/Sources/App/SettingsView.swift"):
    text = (root / view).read_text()
    for call in re.finditer(r"requestAuthorization\(", text):
        # The enclosing function must bail out when the server can't push.
        func_start = text.rfind("func ", 0, call.start())
        if "guard model.offersNotificationPermission" not in text[func_start:call.start()]:
            fail(f"{view}: requestAuthorization is not guarded by model.offersNotificationPermission")
    if "Request notification permission" in text and "if model.offersNotificationPermission" not in text:
        fail(f"{view}: permission button is shown without checking offersNotificationPermission")
for path in (root / "ios/Sources").rglob("*.swift"):
    if path.name in ("ReadinessView.swift", "SettingsView.swift"):
        continue
    if "requestAuthorization(" in path.read_text():
        fail(f"{path.relative_to(root)} requests notification permission outside the gated views")

if errors:
    print("\n".join(f"FAIL: {item}" for item in errors), file=sys.stderr)
    sys.exit(1)
print("ok: privacy manifest, project wiring, trademarks, metadata 1.0, dashboard-only delivery")
PY
