#!/usr/bin/env bash
#
# Bind pkg/mobilecore into app/libs/mobilecore.aar for the sakamoto Android
# client (the gomobile bridge the Home/Config pages call for ALL state
# semantics: SessionPhase / NextRoutingMode / ClassifyProbe flattenings /
# NodeStatus / ConfigStateTransition / the importer API).
#
# Binding convention (mirrors the upstream libbox AAR: -javapkg + -libname):
#   gomobile bind -target android -androidapi 24 \
#     -javapkg=com.pidal.sakamoto -libname=mobilecore ./pkg/mobilecore
# → Kotlin import com.pidal.sakamoto.mobilecore.Mobilecore
#
# Policy: NEVER installs anything; prints the exact missing command and fails.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUTPUT_DIR="${REPO_ROOT}/android/app/libs"
ANDROID_API="${MOBILECORE_ANDROID_API:-24}"

fail() {
    echo "build-mobilecore[android]: ERROR: $*" >&2
    exit 1
}

command -v go >/dev/null 2>&1 || fail "go not found; install go via mise (mise use -g go) and retry"

if [ -z "${ANDROID_HOME:-}" ] && [ -z "${ANDROID_SDK_ROOT:-}" ]; then
    cat >&2 <<EOF
build-mobilecore[android]: ANDROID_HOME/ANDROID_SDK_ROOT is not set.

  brew install --cask android-commandlinetools
  export ANDROID_HOME="\$HOME/Library/Android/sdk"
  sdkmanager "platforms;android-35" "build-tools;35.0.0" "ndk;27.2.12479018"
EOF
    exit 1
fi

GOMOBILE_BIN="$(go env GOPATH)/bin/gomobile"
GOBIND_BIN="$(go env GOPATH)/bin/gobind"
if ! command -v gomobile >/dev/null 2>&1 && [ ! -x "${GOMOBILE_BIN}" ]; then
    cat >&2 <<EOF
build-mobilecore[android]: gomobile not found.

  GOBIN="\$(go env GOPATH)/bin" go install github.com/sagernet/gomobile/cmd/gomobile@latest
  GOBIN="\$(go env GOPATH)/bin" go install github.com/sagernet/gomobile/cmd/gobind@latest
EOF
    exit 1
fi
if [ ! -x "${GOBIND_BIN}" ] && ! command -v gobind >/dev/null 2>&1; then
    cat >&2 <<EOF
build-mobilecore[android]: gobind not found.

  GOBIN="\$(go env GOPATH)/bin" go install github.com/sagernet/gomobile/cmd/gobind@latest
EOF
    exit 1
fi

echo "build-mobilecore[android]: binding ./pkg/mobilecore (api ${ANDROID_API}) → ${OUTPUT_DIR}/mobilecore.aar"
cd "${REPO_ROOT}"
mkdir -p "${OUTPUT_DIR}"
rm -f "${OUTPUT_DIR}/mobilecore.aar"

gomobile bind \
    -v \
    -o "${OUTPUT_DIR}/mobilecore.aar" \
    -target android \
    -androidapi "${ANDROID_API}" \
    -javapkg=com.pidal.sakamoto \
    -libname=mobilecore \
    ./pkg/mobilecore

cat <<EOF

build-mobilecore[android]: done.
  ${OUTPUT_DIR}/mobilecore.aar

The AAR is git-ignored (android/app/libs/) — never commit build output.
Kotlin side: import com.pidal.sakamoto.mobilecore.Mobilecore
EOF
