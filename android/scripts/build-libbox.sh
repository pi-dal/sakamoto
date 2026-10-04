#!/usr/bin/env bash
#
# Build libbox.aar (+ libbox-legacy.aar) for the sakamoto Android client.
#
# Delegates to sing-box's own Android builder (cmd/internal/build_libbox
# -target android) so the tag set, API levels and output names stay
# identical to upstream v1.14.2 — this script adds no build flags of its
# own. with_tailscale is part of the builder's sharedTags in v1.14.2
# (see experimental/libbox + cmd/internal/build_libbox/main.go), so the
# Tailscale endpoint is built in exactly like the iOS Libbox.xcframework.
#
# Upstream builder behavior (v1.14.2):
#   - libbox.aar       : gomobile bind -androidapi 24, full tag set
#   - libbox-legacy.aar: gomobile bind -androidapi 21, without with_naive_outbound
#   - requires openjdk 17 exactly (upstream checkJavaVersion), the Android
#     SDK (upstream build_shared.FindSDK via ANDROID_HOME) and gomobile/gobind
#     in $(go env GOPATH)/bin.
#
# Usage:
#   android/scripts/build-libbox.sh
#   SING_BOX_SOURCE=/path/to/sing-box android/scripts/build-libbox.sh
#
# SING_BOX_SOURCE defaults to the go module cache entry for sing-box v1.14.2.
#
# Policy (mirrors ios/scripts/build-libbox.sh): this script NEVER installs
# anything (no JDK, no SDK, no gomobile bootstrap). If a tool is missing it
# prints the exact command for the owner to run manually and exits non-zero.

set -euo pipefail

REQUIRED_SING_BOX_VERSION="v1.14.2"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUTPUT_DIR="${REPO_ROOT}/android/app/libs"

fail() {
    echo "build-libbox[android]: ERROR: $*" >&2
    exit 1
}

command -v go >/dev/null 2>&1 || fail "go not found; install go via mise (mise use -g go) and retry"

# --- resolve sing-box source ---------------------------------------------------

if [ -z "${SING_BOX_SOURCE:-}" ]; then
    GOMODCACHE="$(go env GOMODCACHE)"
    SING_BOX_SOURCE="${GOMODCACHE}/github.com/sagernet/sing-box@${REQUIRED_SING_BOX_VERSION}"
fi

if [ ! -d "${SING_BOX_SOURCE}" ]; then
    cat >&2 <<EOF
build-libbox[android]: sing-box source not found at ${SING_BOX_SOURCE}

Provide a source tree explicitly, e.g.:

  git clone --branch ${REQUIRED_SING_BOX_VERSION} --depth 1 https://github.com/SagerNet/sing-box /tmp/sing-box
  SING_BOX_SOURCE=/tmp/sing-box android/scripts/build-libbox.sh
EOF
    exit 1
fi

# --- verify version ------------------------------------------------------------

if [ -d "${SING_BOX_SOURCE}/.git" ]; then
    DETECTED_VERSION="$(git -C "${SING_BOX_SOURCE}" describe --tags --abbrev=0 2>/dev/null || echo unknown)"
else
    case "$(basename "${SING_BOX_SOURCE}")" in
        *"@${REQUIRED_SING_BOX_VERSION}"*) DETECTED_VERSION="${REQUIRED_SING_BOX_VERSION}" ;;
        *) DETECTED_VERSION="unknown" ;;
    esac
fi

if [ "${DETECTED_VERSION}" != "${REQUIRED_SING_BOX_VERSION}" ] && [ "${LIBBOX_ALLOW_OTHER_VERSION:-0}" != "1" ]; then
    cat >&2 <<EOF
build-libbox[android]: expected sing-box ${REQUIRED_SING_BOX_VERSION}, found '${DETECTED_VERSION}' at
  ${SING_BOX_SOURCE}

To proceed with a different tree anyway:

  LIBBOX_ALLOW_OTHER_VERSION=1 SING_BOX_SOURCE=${SING_BOX_SOURCE} android/scripts/build-libbox.sh
EOF
    exit 1
fi

# --- preflight the Android toolchain (fail loudly, never install) --------------

if [ -z "${ANDROID_HOME:-}" ] && [ -z "${ANDROID_SDK_ROOT:-}" ]; then
    cat >&2 <<EOF
build-libbox[android]: ANDROID_HOME/ANDROID_SDK_ROOT is not set and the upstream
builder's build_shared.FindSDK cannot run without an SDK.

Restore the toolchain first (exact commands for this machine, macOS + mise):

  brew install --cask android-commandlinetools          # SDK manager, no IDE
  export ANDROID_HOME="\$HOME/Library/Android/sdk"       # put in your shell rc
  sdkmanager "platforms;android-35" "build-tools;35.0.0" "ndk;28.2.13676358"

  mise use -g java temurin-17                            # upstream requires openjdk 17
  java --version                                          # must print openjdk 17

See android/README.md for the full list.
EOF
    exit 1
fi

if ! command -v sdkmanager >/dev/null 2>&1 && [ ! -d "${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}/cmdline-tools" ]; then
    echo "build-libbox[android]: WARNING: sdkmanager not on PATH; continuing (the builder checks the SDK itself)." >&2
fi

# Upstream checkJavaVersion demands openjdk 17 exactly.
JAVA_BIN="${JAVA_HOME:+${JAVA_HOME}/bin/}java"
if command -v "${JAVA_BIN}" >/dev/null 2>&1; then
    JAVA_VERSION="$("${JAVA_BIN}" --version 2>&1 | head -1 || true)"
    case "${JAVA_VERSION}" in
        *openjdk\ 17*) : ;;
        *)
            cat >&2 <<EOF
build-libbox[android]: upstream checkJavaVersion requires 'openjdk 17', found:
  ${JAVA_VERSION}

Fix with: mise use -g java temurin-17
EOF
            exit 1
            ;;
    esac
else
    cat >&2 <<EOF
build-libbox[android]: no usable java runtime.

/usr/bin/java on this machine is the macOS stub. Restore with:

  mise use -g java temurin-17
EOF
    exit 1
fi

GOMOBILE_BIN="$(go env GOPATH)/bin/gomobile"
GOBIND_BIN="$(go env GOPATH)/bin/gobind"
if ! command -v gomobile >/dev/null 2>&1 && [ ! -x "${GOMOBILE_BIN}" ]; then
    cat >&2 <<EOF
build-libbox[android]: gomobile not found.

The upstream builder shells out to gomobile. Install it manually (this script
will not install anything), then retry:

  GOBIN="\$(go env GOPATH)/bin" go install github.com/sagernet/gomobile/cmd/gomobile@latest
  GOBIN="\$(go env GOPATH)/bin" go install github.com/sagernet/gomobile/cmd/gobind@latest
  # (keep GOPATH/bin on PATH, or export PATH="\$PATH:\$(go env GOPATH)/bin")
EOF
    exit 1
fi
if [ ! -x "${GOBIND_BIN}" ] && ! command -v gobind >/dev/null 2>&1; then
    cat >&2 <<EOF
build-libbox[android]: gobind not found in \$(go env GOPATH)/bin.

  GOBIN="\$(go env GOPATH)/bin" go install github.com/sagernet/gomobile/cmd/gobind@latest
EOF
    exit 1
fi

# --- ensure a writable source tree ----------------------------------------------
#
# Same pitfall as the iOS build: gomobile writes build/ artifacts INSIDE the
# source tree; the go module cache is read-only. Copy once when needed.

if [ ! -w "${SING_BOX_SOURCE}" ]; then
    WRITABLE_ROOT="${LIBBOX_WORK_DIR:-${TMPDIR:-/tmp}/sakamoto-libbox-android}"
    WRITABLE_SOURCE="${WRITABLE_ROOT}/sing-box@${REQUIRED_SING_BOX_VERSION}"
    if [ ! -d "${WRITABLE_SOURCE}" ]; then
        echo "build-libbox[android]: source is read-only (module cache); copying to ${WRITABLE_SOURCE} ..."
        mkdir -p "${WRITABLE_ROOT}"
        rm -rf "${WRITABLE_SOURCE}.tmp" "${WRITABLE_SOURCE}"
        cp -R "${SING_BOX_SOURCE}" "${WRITABLE_SOURCE}.tmp"
        chmod -R u+w "${WRITABLE_SOURCE}.tmp"
        mv "${WRITABLE_SOURCE}.tmp" "${WRITABLE_SOURCE}"
    fi
    SING_BOX_SOURCE="${WRITABLE_SOURCE}"
fi

echo "build-libbox[android]: sing-box source : ${SING_BOX_SOURCE} (${DETECTED_VERSION})"
echo "build-libbox[android]: building via upstream cmd/internal/build_libbox -target android"
echo "build-libbox[android]: (main libbox.aar = API 24; legacy libbox-legacy.aar = API 21,"
echo "                       sharedTags include with_tailscale in v1.14.2)"

cd "${SING_BOX_SOURCE}"

if ! go run ./cmd/internal/build_libbox -target android; then
    cat >&2 <<EOF

build-libbox[android]: upstream builder failed. Common causes:
  - fresh checkout needs:  cd ${SING_BOX_SOURCE} && go mod download
  - NDK missing:           sdkmanager "ndk;28.2.13676358"
  - wrong java:            mise use -g java temurin-17   (must be openjdk 17)
EOF
    exit 1
fi

# --- relocate the AARs -----------------------------------------------------------

mkdir -p "${OUTPUT_DIR}"
BUILT_MAIN=""
for candidate in "${SING_BOX_SOURCE}/libbox.aar" \
                 "${SING_BOX_SOURCE}/../sing-box-for-android/app/libs/libbox.aar"; do
    if [ -f "${candidate}" ]; then BUILT_MAIN="${candidate}"; break; fi
done
[ -n "${BUILT_MAIN}" ] || fail "builder finished but libbox.aar was not found"

rm -f "${OUTPUT_DIR}/libbox.aar" "${OUTPUT_DIR}/libbox-legacy.aar"
cp "${BUILT_MAIN}" "${OUTPUT_DIR}/libbox.aar"
LEGACY=""
for candidate in "${SING_BOX_SOURCE}/libbox-legacy.aar" \
                 "${SING_BOX_SOURCE}/../sing-box-for-android/app/libs/libbox-legacy.aar"; do
    if [ -f "${candidate}" ]; then LEGACY="${candidate}"; break; fi
done
if [ -n "${LEGACY}" ]; then
    cp "${LEGACY}" "${OUTPUT_DIR}/libbox-legacy.aar"
fi

cat <<EOF

build-libbox[android]: done.
  ${OUTPUT_DIR}/libbox.aar
$( [ -n "${LEGACY}" ] && echo "  ${OUTPUT_DIR}/libbox-legacy.aar" || true )

AARs are git-ignored (android/app/libs/) — never commit build output.
Next: android/scripts/build-mobilecore.sh, then ./gradlew :app:assembleDebug.
EOF
