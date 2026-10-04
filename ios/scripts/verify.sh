#!/usr/bin/env bash
#
# Verification entry point for the sakamoto iOS integration.
#
#   1/4  Go:    gofmt clean + go test ./...        (state vocabulary, contract golden)
#   2/4  iOS:   swift build && swift test          (contract models, IPC codec, NE transport, Tailscale vocabulary)
#   3/4  Frameworks: Libbox.xcframework + Mobilecore.xcframework present and stamped
#   4/4  Xcode: xcodegen generate + unsigned simulator build of app + extension
#
# Exit codes: 0 = everything verified; 1 = at least one check failed.
#
# Device/signed builds are NOT attempted here and are NOT reported as a pass:
# they require a real signing Team with the NetworkExtension capability
# (see ios/README.md "Signing").

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
FAILED=0

section() { echo; echo "=== $* ==="; }

# --- 1/4 Go -------------------------------------------------------------------

section "1/4 Go: gofmt + go test ./..."

UNFORMATTED="$(cd "${REPO_ROOT}" && gofmt -l ./cmd ./internal ./pkg ./tools)"
if [ -n "${UNFORMATTED}" ]; then
    echo "gofmt needed on:"
    echo "${UNFORMATTED}"
    FAILED=1
else
    echo "gofmt: clean"
fi

if (cd "${REPO_ROOT}" && go test ./...); then
    echo "go test ./...: PASS"
else
    echo "go test ./...: FAIL"
    FAILED=1
fi

# --- 2/4 iOS SwiftPM ----------------------------------------------------------

section "2/4 iOS package: swift build && swift test (ios/)"

if (cd "${REPO_ROOT}/ios" && swift build && swift test); then
    echo "swift build/test: PASS"
else
    echo "swift build/test: FAIL"
    FAILED=1
fi

# --- 3/4 Frameworks -----------------------------------------------------------

section "3/4 Frameworks (built artifacts, git-ignored)"

for NAME in Libbox Mobilecore; do
    FRAMEWORK="${REPO_ROOT}/ios/Frameworks/${NAME}.xcframework"
    if [ -d "${FRAMEWORK}" ]; then
        PLIST_OK=1
        for fw in "${FRAMEWORK}"/ios-*/*.framework; do
            [ -f "${fw}/Info.plist" ] || PLIST_OK=0
        done
        if [ "${PLIST_OK}" -eq 1 ]; then
            echo "${NAME}.xcframework: PRESENT (shallow bundles stamped)"
        else
            echo "${NAME}.xcframework: PRESENT but bundles need flattening — re-run scripts/build-${NAME#,}.sh or flatten-gomobile-framework.sh"
            FAILED=1
        fi
    else
        echo "${NAME}.xcframework: MISSING"
        cat >&2 <<EOF

NOT VERIFIED (explicit boundary, not a silent pass):
  ios/Frameworks/${NAME}.xcframework is missing.
  Build it: ios/scripts/build-$(echo "${NAME}" | tr '[:upper:]' '[:lower:]').sh
  (requires gomobile + gobind in \$(go env GOPATH)/bin — see ios/README.md)
EOF
        FAILED=1
    fi
done

# --- 4/4 Xcode ----------------------------------------------------------------

section "4/4 Xcode: xcodegen + unsigned simulator build"

if command -v xcodegen >/dev/null 2>&1; then
    (cd "${REPO_ROOT}/ios" && xcodegen generate) || FAILED=1
else
    echo "xcodegen not found (brew install xcodegen); skipping project generation"
    FAILED=1
fi

if command -v xcodebuild >/dev/null 2>&1; then
    if (cd "${REPO_ROOT}/ios" && xcodebuild -project Sakamoto.xcodeproj \
        -scheme Sakamoto -destination 'generic/platform=iOS Simulator' \
        -configuration Debug CODE_SIGNING_ALLOWED=NO build >/dev/null); then
        echo "xcodebuild (unsigned simulator): PASS"
        echo "NOTE: a signed device build additionally needs DEVELOPMENT_TEAM and"
        echo "provisioned NetworkExtension capabilities — intentionally not attempted here."
    else
        echo "xcodebuild (unsigned simulator): FAIL"
        FAILED=1
    fi
else
    echo "xcodebuild not found; install Xcode"
    FAILED=1
fi

section "result"

if [ "${FAILED}" -ne 0 ]; then
    echo "VERIFY: FAIL (see sections above)"
    exit 1
fi
echo "VERIFY: PASS"
