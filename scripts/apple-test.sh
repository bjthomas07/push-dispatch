#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ -z "${IOS_DESTINATION:-}" ]]; then
  push_simulator_id=$(xcrun simctl list devices available --json | python3 -c 'import json,sys; d=json.load(sys.stdin); print(next(x["udid"] for a in d["devices"].values() for x in a if x["name"].startswith("iPhone")))')
  IOS_DESTINATION="platform=iOS Simulator,id=$push_simulator_id"
fi
xcodebuild test -scheme PushDispatch-Package -destination "$IOS_DESTINATION" \
  -derivedDataPath apple/.build/xcode -disableAutomaticPackageResolution -quiet
