#!/usr/bin/env bash
# Package simulator output, or archive/export a signed device build.
# Frameworks must be built first. Automatic provisioning is opt-in.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUTPUT_DIR="${IOS_OUTPUT_DIR:-${REPO_ROOT}/ios/build}"
MODE="${1:-simulator}"
mkdir -p "${OUTPUT_DIR}"
OUTPUT_DIR="$(cd "${OUTPUT_DIR}" && pwd)"

case "${MODE}" in
    simulator|archive|store-archive|export) ;;
    *) echo "usage: package.sh simulator|archive|store-archive|export" >&2; exit 2 ;;
esac

PROVISIONING_ARGS=()
if [ "${IOS_ALLOW_PROVISIONING_UPDATES:-0}" = 1 ]; then
    PROVISIONING_ARGS+=(-allowProvisioningUpdates)
fi

# Fail before an expensive build when device-signing input is absent.
if [ "${MODE}" = archive ]; then
    : "${IOS_DEVELOPMENT_TEAM:?Set IOS_DEVELOPMENT_TEAM to a provisioned Apple Team ID}"
fi
if [ "${MODE}" = export ]; then
    : "${IOS_EXPORT_OPTIONS_PLIST:?Set IOS_EXPORT_OPTIONS_PLIST to your local export options plist}"
    [ -f "${IOS_EXPORT_OPTIONS_PLIST}" ] || { echo "Export options plist not found" >&2; exit 1; }
    [ -d "${OUTPUT_DIR}/Sakamoto.xcarchive" ] || { echo "Run mise run ios:archive first" >&2; exit 1; }
    xcodebuild -exportArchive \
        -archivePath "${OUTPUT_DIR}/Sakamoto.xcarchive" \
        -exportPath "${OUTPUT_DIR}/ipa" \
        -exportOptionsPlist "${IOS_EXPORT_OPTIONS_PLIST}" "${PROVISIONING_ARGS[@]}"
    exit 0
fi

[ -d "${REPO_ROOT}/ios/Frameworks/Libbox.xcframework" ] || {
    echo "Missing combined Libbox.xcframework: run mise run ios:build first" >&2
    exit 1
}
cd "${REPO_ROOT}/ios"
xcodegen generate

if [ "${MODE}" = simulator ]; then
    xcodebuild -project Sakamoto.xcodeproj -scheme Sakamoto \
        -destination 'generic/platform=iOS Simulator' \
        -configuration Debug -derivedDataPath "${OUTPUT_DIR}/DerivedData" \
        CODE_SIGNING_ALLOWED=NO build
    APP="${OUTPUT_DIR}/DerivedData/Build/Products/Debug-iphonesimulator/Sakamoto.app"
    [ -d "${APP}" ] || { echo "Built simulator app not found" >&2; exit 1; }
    # ditto preserves bundle metadata and symlinks in the distributable zip.
    ditto -c -k --sequesterRsrc --keepParent "${APP}" "${OUTPUT_DIR}/Sakamoto-simulator.zip"
    python3 - "${OUTPUT_DIR}/Sakamoto-simulator.zip" <<'PY'
import hashlib
import pathlib
import sys
p = pathlib.Path(sys.argv[1])
with p.open("rb") as f:
    digest = hashlib.file_digest(f, "sha256").hexdigest()
p.with_suffix(".zip.sha256").write_text(digest + "  " + p.name + "\n")
PY
    echo "Simulator app: ${OUTPUT_DIR}/Sakamoto-simulator.zip (not installable on iPhone)"
elif [ "${MODE}" = store-archive ]; then
    # Teams without registered devices cannot create development profiles.
    # Preserve the required entitlements with an ad-hoc archive signature so
    # App Store Connect export requests full distribution profiles, rather
    # than silently exporting a bundle without VPN/App Group/iCloud access.
    xcodebuild -project Sakamoto.xcodeproj -scheme Sakamoto \
        -destination 'generic/platform=iOS' -configuration Release \
        -archivePath "${OUTPUT_DIR}/Sakamoto.xcarchive" \
        CODE_SIGNING_ALLOWED=NO archive
    APP="${OUTPUT_DIR}/Sakamoto.xcarchive/Products/Applications/Sakamoto.app"
    codesign --force --sign - --entitlements Extension/SakamotoPacketTunnel.entitlements \
        "${APP}/PlugIns/SakamotoPacketTunnel.appex"
    codesign --force --sign - --entitlements Widgets/SakamotoWidgets.entitlements \
        "${APP}/PlugIns/SakamotoWidgets.appex"
    codesign --force --sign - --entitlements App/Sakamoto.entitlements "${APP}"
    echo "Archive prepared for distribution export: ${OUTPUT_DIR}/Sakamoto.xcarchive"
    echo "Ad-hoc archive signature is not a distribution signature; run ios:ipa."
else
    xcodebuild -project Sakamoto.xcodeproj -scheme Sakamoto \
        -destination 'generic/platform=iOS' -configuration Release \
        -archivePath "${OUTPUT_DIR}/Sakamoto.xcarchive" \
        DEVELOPMENT_TEAM="${IOS_DEVELOPMENT_TEAM}" "${PROVISIONING_ARGS[@]}" archive
    echo "Signed archive: ${OUTPUT_DIR}/Sakamoto.xcarchive"
fi
