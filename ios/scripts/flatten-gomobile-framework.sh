#!/usr/bin/env bash
#
# Flatten one gomobile framework slice from the macOS-style deep (versioned)
# layout to the shallow layout Xcode requires for iOS embedding:
#
#   Xxx.framework/Versions/A/{Xxx,Headers,Modules,Resources}   (deep)
#   -> Xxx.framework/{Xxx,Headers,Modules,Resources}            (shallow)
#
# Also rewrites the owning xcframework's Info.plist BinaryPath entries
# (Versions/A/Xxx -> Xxx) when one is passed via -p.
#
# Usage: flatten-gomobile-framework.sh [-p xcframework/Info.plist] path/to/Xxx.framework

set -euo pipefail

PLIST=""
while getopts "p:" opt; do
    case "$opt" in
        p) PLIST="$OPTARG" ;;
    esac
done
shift $((OPTIND - 1))

FRAMEWORK="${1:?usage: flatten-gomobile-framework.sh [-p Info.plist] path/to/Xxx.framework}"
NAME="$(basename "${FRAMEWORK}" .framework)"

if [ ! -d "${FRAMEWORK}/Versions" ]; then
    echo "flatten: ${FRAMEWORK} is already shallow"
    exit 0
fi

# Versions/Current is a symlink; copy the real version directory so the
# result is a plain shallow bundle, never a symlink chain.
REAL_VERSION="$(basename "$(readlink "${FRAMEWORK}/Versions/Current")")"
STAGE="$(mktemp -d)/${NAME}.framework"
cp -R "${FRAMEWORK}/Versions/${REAL_VERSION}" "${STAGE}"
rm -rf "${FRAMEWORK}"
mv "${STAGE}" "${FRAMEWORK}"

# gomobile parks the bundle Info.plist inside Resources/; iOS shallow
# frameworks must carry it at the bundle root or Xcode rejects the embed.
if [ -f "${FRAMEWORK}/Resources/Info.plist" ] && [ ! -f "${FRAMEWORK}/Info.plist" ]; then
    mv "${FRAMEWORK}/Resources/Info.plist" "${FRAMEWORK}/Info.plist"
fi

# gomobile emits an EMPTY Info.plist dict; Xcode 26 requires the standard
# keys. Fill in whatever is missing (name-derived, stable).
PB=/usr/libexec/PlistBuddy
INFO_PLIST="${FRAMEWORK}/Info.plist"
[ -f "${INFO_PLIST}" ] || echo '{}' > "${INFO_PLIST}"
plist_set() {
    "${PB}" -c "Add :$1 $2 $3" "${INFO_PLIST}" 2>/dev/null || \
        "${PB}" -c "Set :$1 $3" "${INFO_PLIST}"
}
plist_set CFBundleIdentifier string "io.sagernet.gomobile.${NAME}"
plist_set CFBundleExecutable string "${NAME}"
plist_set CFBundleName string "${NAME}"
plist_set CFBundlePackageType string "FMWK"
plist_set CFBundleShortVersion string "1.0"
plist_set CFBundleVersion string "1"
plist_set MinimumOSVersion string "15.0"

if [ -n "${PLIST}" ] && [ -f "${PLIST}" ]; then
    /usr/libexec/PlistBuddy -c "Set :AvailableLibraries:0:BinaryPath ${NAME}.framework/${NAME}" "${PLIST}" 2>/dev/null || true
    /usr/libexec/PlistBuddy -c "Set :AvailableLibraries:1:BinaryPath ${NAME}.framework/${NAME}" "${PLIST}" 2>/dev/null || true
    # Cover >2 slices explicitly (Libbox ships exactly two).
    INDEX=0
    while /usr/libexec/PlistBuddy -c "Print :AvailableLibraries:${INDEX}:BinaryPath" "${PLIST}" >/dev/null 2>&1; do
        /usr/libexec/PlistBuddy -c "Set :AvailableLibraries:${INDEX}:BinaryPath ${NAME}.framework/${NAME}" "${PLIST}"
        INDEX=$((INDEX + 1))
    done
fi

echo "flatten: ${FRAMEWORK} -> shallow layout"
