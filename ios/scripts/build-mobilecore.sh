#!/usr/bin/env bash
#
# Build Mobilecore.xcframework (gomobile bind of pkg/mobilecore) so the iOS
# app folds phases/latency/config-state through the Go bridge instead of
# re-deriving rules in Swift.
#
# Requires the same toolchain as build-libbox.sh: gomobile + gobind in
# $(go env GOPATH)/bin, and github.com/sagernet/gomobile resolvable by this
# module (pinned for `go mod tidy` by tools/tools.go with the `tools` tag).
#
# Output: ios/Frameworks/Mobilecore.xcframework (git-ignored).

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUTPUT_DIR="${REPO_ROOT}/ios/Frameworks"

fail() {
    echo "build-mobilecore: ERROR: $*" >&2
    exit 1
}

command -v go >/dev/null 2>&1 || fail "go not found; install go via mise (mise use -g go) and retry"

GOMOBILE_BIN="$(go env GOPATH)/bin/gomobile"
GOBIND_BIN="$(go env GOPATH)/bin/gobind"
if [ ! -x "${GOMOBILE_BIN}" ] || [ ! -x "${GOBIND_BIN}" ]; then
    cat >&2 <<EOF
build-mobilecore: gomobile/gobind not found in \$(go env GOPATH)/bin.

Install manually (this script never installs anything):

  GOBIN="\$(go env GOPATH)/bin" go install github.com/sagernet/gomobile/cmd/gomobile@latest
  GOBIN="\$(go env GOPATH)/bin" go install github.com/sagernet/gomobile/cmd/gobind@latest

(The explicit GOBIN matters when mise sets GOBIN — see build-libbox.sh.)
EOF
    exit 1
fi

# gomobile bind executes gobind in this module's context; the generated
# bindings import gomobile/bind. tools/tools.go keeps the require honest.
if ! go list -m github.com/sagernet/gomobile >/dev/null 2>&1; then
    fail "github.com/sagernet/gomobile is not in go.mod (expected via tools/tools.go + go get github.com/sagernet/gomobile@v0.1.12)"
fi

mkdir -p "${OUTPUT_DIR}"

echo "build-mobilecore: binding ./pkg/mobilecore -> ${OUTPUT_DIR}/Mobilecore.xcframework"
cd "${REPO_ROOT}"
"${GOMOBILE_BIN}" bind \
    -v \
    -target=ios,iossimulator \
    -iosversion=15.0 \
    -o "${OUTPUT_DIR}/Mobilecore.xcframework" \
    -libname=mobilecore \
    ./pkg/mobilecore

# gomobile writes macOS-style deep (versioned) bundles; Xcode requires
# shallow bundles for iOS embedding. Flatten every iOS slice in place.
FLATTEN="${REPO_ROOT}/ios/scripts/flatten-gomobile-framework.sh"
for slice in "${OUTPUT_DIR}"/Mobilecore.xcframework/ios-*; do
    for fw in "${slice}"/*.framework; do
        [ -d "${fw}" ] && "${FLATTEN}" -p "${OUTPUT_DIR}/Mobilecore.xcframework/Info.plist" "${fw}"
    done
done

echo "build-mobilecore: done."
