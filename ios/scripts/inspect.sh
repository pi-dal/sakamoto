#!/usr/bin/env bash
# Build/run the isolated Debug-only SimAgentationPlus app in a simulator.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEVICE="${IOS_SIMULATOR_UDID:-booted}"
DERIVED="${ROOT}/ios/build/inspector"
cd "${ROOT}/ios"
xcodegen generate --spec project.sim-agentation.yml
xcodebuild -project SakamotoInspector.xcodeproj -scheme Sakamoto \
    -destination 'generic/platform=iOS Simulator' -configuration Debug \
    -derivedDataPath "${DERIVED}" CODE_SIGN_IDENTITY=- CODE_SIGNING_ALLOWED=YES build
xcrun simctl install "${DEVICE}" "${DERIVED}/Build/Products/Debug-iphonesimulator/Sakamoto.app"
if xcrun simctl spawn "${DEVICE}" launchctl list | grep -q 'com.pidal.sakamoto'; then
    xcrun simctl terminate "${DEVICE}" com.pidal.sakamoto
fi
xcrun simctl launch "${DEVICE}" com.pidal.sakamoto
printf '\nInspector: http://127.0.0.1:38471/snapshot\nSimAgentation: http://127.0.0.1:38470\n'
