#!/usr/bin/env bash
# Run UsageWidgetCoreTests on the newest available iOS 26+ iPhone simulator.
# Used by ios-ci.yml (every PR) and by ios-testflight.yml before archiving, so
# the TestFlight gate runs exactly what PR CI already proved green.
# Needs no signing secrets (CODE_SIGNING_ALLOWED=NO). Expects
# ios/UsageWidget.xcodeproj to exist (run `xcodegen generate` first).
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "${root}/ios"

if [ ! -d UsageWidget.xcodeproj ]; then
  echo "::error::ios/UsageWidget.xcodeproj is missing. Run xcodegen generate first."
  exit 1
fi

destination="$(python3 "${root}/scripts/pick-ios-simulator.py")"
result_bundle="${RESULT_BUNDLE_PATH:-${RUNNER_TEMP:-/tmp}/ios-tests.xcresult}"
derived_data="${DERIVED_DATA_PATH:-${RUNNER_TEMP:-/tmp}/DerivedData}"
rm -rf "${result_bundle}"

xcodebuild test \
  -project UsageWidget.xcodeproj \
  -scheme UsageWidget \
  -configuration Debug \
  -destination "${destination}" \
  -derivedDataPath "${derived_data}" \
  -resultBundlePath "${result_bundle}" \
  -only-testing:UsageWidgetCoreTests \
  CODE_SIGNING_ALLOWED=NO
