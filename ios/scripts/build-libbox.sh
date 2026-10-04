#!/usr/bin/env bash
#
# Build Libbox.xcframework for the sakamoto iOS integration.
#
# Delegates to sing-box's own Apple builder (cmd/internal/build_libbox) so
# the tag set, linker flags and minimum OS versions stay identical to
# upstream v1.14.2 — this script adds no build flags of its own.
#
# Usage:
#   ios/scripts/build-libbox.sh
#   SING_BOX_SOURCE=/path/to/sing-box ios/scripts/build-libbox.sh   # git checkout
#   LIBBOX_TARGETS=ios ios/scripts/build-libbox.sh                  # default: ios,iossimulator
#
# SING_BOX_SOURCE defaults to the go module cache entry for sing-box v1.14.2
# (read-only is fine: the builder writes only to temp/output dirs).
#
# Policy: this script NEVER installs anything (no go install, no brew, no
# gomobile init bootstrap). If a tool is missing it prints the exact command
# for the owner to run manually and exits non-zero.

set -euo pipefail

REQUIRED_SING_BOX_VERSION="v1.14.2"
LIBBOX_TARGETS="${LIBBOX_TARGETS:-ios,iossimulator}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUTPUT_DIR="${REPO_ROOT}/ios/Frameworks"

fail() {
    echo "build-libbox: ERROR: $*" >&2
    exit 1
}

command -v go >/dev/null 2>&1 || fail "go not found; install go via mise (mise use -g go) and retry"
command -v xcrun >/dev/null 2>&1 || fail "xcrun not found; install Xcode and run 'xcodebuild -runFirstLaunch'"

# --- resolve sing-box source -------------------------------------------------

if [ -z "${SING_BOX_SOURCE:-}" ]; then
    GOMODCACHE="$(go env GOMODCACHE)"
    SING_BOX_SOURCE="${GOMODCACHE}/github.com/sagernet/sing-box@${REQUIRED_SING_BOX_VERSION}"
fi

if [ ! -d "${SING_BOX_SOURCE}" ]; then
    cat >&2 <<EOF
build-libbox: sing-box source not found at ${SING_BOX_SOURCE}

Provide a source tree explicitly, e.g.:

  git clone --branch ${REQUIRED_SING_BOX_VERSION} --depth 1 https://github.com/SagerNet/sing-box /tmp/sing-box
  SING_BOX_SOURCE=/tmp/sing-box ios/scripts/build-libbox.sh
EOF
    exit 1
fi

# --- ensure a writable source tree --------------------------------------------
#
# Real failure found on this machine: gomobile bind writes a build/ directory
# (and its generated bindings) NEXT TO the libbox package, i.e. inside the
# source tree. A go module-cache entry (${GOMODCACHE}/...) is read-only, so
# the build dies with "mkdir ...: permission denied". When the resolved source
# is not writable, copy it once to a writable location and use that copy
# (bit-identical source, no network access needed).

if [ ! -w "${SING_BOX_SOURCE}" ]; then
    WRITABLE_ROOT="${LIBBOX_WORK_DIR:-${TMPDIR:-/tmp}/sakamoto-libbox}"
    WRITABLE_SOURCE="${WRITABLE_ROOT}/sing-box@${REQUIRED_SING_BOX_VERSION}"
    if [ ! -d "${WRITABLE_SOURCE}" ]; then
        echo "build-libbox: source is read-only (module cache); copying to ${WRITABLE_SOURCE} ..."
        mkdir -p "${WRITABLE_ROOT}"
        rm -rf "${WRITABLE_SOURCE}.tmp" "${WRITABLE_SOURCE}"
        cp -R "${SING_BOX_SOURCE}" "${WRITABLE_SOURCE}.tmp"
        # cp -R preserves the module cache's read-only permission bits
        # (r-xr-xr-x); gomobile must write build artifacts inside the tree.
        chmod -R u+w "${WRITABLE_SOURCE}.tmp"
        mv "${WRITABLE_SOURCE}.tmp" "${WRITABLE_SOURCE}"
    fi
    SING_BOX_SOURCE="${WRITABLE_SOURCE}"
fi

# --- verify version ----------------------------------------------------------

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
build-libbox: expected sing-box ${REQUIRED_SING_BOX_VERSION}, found '${DETECTED_VERSION}' at
  ${SING_BOX_SOURCE}

The libbox API surface this integration targets is v1.14.2 (gRPC-based
CommandServer/CommandClient). To proceed with a different tree anyway:

  LIBBOX_ALLOW_OTHER_VERSION=1 SING_BOX_SOURCE=${SING_BOX_SOURCE} ios/scripts/build-libbox.sh
EOF
    exit 1
fi

# --- require gomobile + gobind (never install them here) ----------------------
#
# Upstream cmd/internal/build_shared.FindMobile requires BOTH binaries in
# $(go env GOPATH)/bin. Pitfall discovered on this machine: mise sets GOBIN,
# and `go install` honors GOBIN over GOPATH/bin, so a plain
# `go install .../gomobile@latest` lands in the mise dir and FindMobile still
# fails. Install with an explicit GOBIN (see ios/README.md).

GOMOBILE_BIN="$(go env GOPATH)/bin/gomobile"
GOBIND_BIN="$(go env GOPATH)/bin/gobind"
if ! command -v gomobile >/dev/null 2>&1 && [ ! -x "${GOMOBILE_BIN}" ]; then
    cat >&2 <<EOF
build-libbox: gomobile not found.

The upstream builder shells out to gomobile. Install it manually (this
script will not install anything), then retry:

  GOBIN="\$(go env GOPATH)/bin" go install github.com/sagernet/gomobile/cmd/gomobile@latest
  GOBIN="\$(go env GOPATH)/bin" go install github.com/sagernet/gomobile/cmd/gobind@latest
  # (keep GOPATH/bin on PATH, or export PATH="\$PATH:\$(go env GOPATH)/bin")
  # Note: if GOBIN is set (e.g. by mise), a plain go install goes there and
  # the upstream FindMobile check (which looks in GOPATH/bin) still fails.
EOF
    exit 1
fi
if [ ! -x "${GOBIND_BIN}" ]; then
    cat >&2 <<EOF
build-libbox: gobind not found in \$(go env GOPATH)/bin.

The upstream FindMobile check requires BOTH gomobile and gobind there
(even when a gomobile is already on PATH). Install it manually:

  GOBIN="\$(go env GOPATH)/bin" go install github.com/sagernet/gomobile/cmd/gobind@latest
EOF
    exit 1
fi

echo "build-libbox: sing-box source : ${SING_BOX_SOURCE} (${DETECTED_VERSION})"
echo "build-libbox: targets         : ${LIBBOX_TARGETS}"
echo "build-libbox: building via upstream cmd/internal/build_libbox ..."

cd "${SING_BOX_SOURCE}"

# The upstream builder may need module downloads on a fresh checkout; if this
# fails, it tells you exactly what to run by hand (no implicit installs here).
if ! go run ./cmd/internal/build_libbox -target apple -platform "${LIBBOX_TARGETS}"; then
    cat >&2 <<EOF

build-libbox: upstream builder failed. On a fresh sing-box checkout the usual
cause is missing module downloads. Run manually, then retry:

  cd ${SING_BOX_SOURCE} && go mod download
  ios/scripts/build-libbox.sh
EOF
    exit 1
fi

# --- relocate the framework ---------------------------------------------------

BUILT=""
for candidate in "${SING_BOX_SOURCE}/Libbox.xcframework" \
                 "${SING_BOX_SOURCE}/../sing-box-for-apple/Libbox.xcframework"; do
    if [ -d "${candidate}" ]; then BUILT="${candidate}"; break; fi
done
[ -n "${BUILT}" ] || fail "builder finished but Libbox.xcframework was not found next to the source"

mkdir -p "${OUTPUT_DIR}"
rm -rf "${OUTPUT_DIR}/Libbox.xcframework"
mv "${BUILT}" "${OUTPUT_DIR}/Libbox.xcframework"

# gomobile writes macOS-style deep (versioned) bundles; Xcode requires
# shallow bundles for iOS embedding. Flatten every iOS slice in place.
FLATTEN="${REPO_ROOT}/ios/scripts/flatten-gomobile-framework.sh"
for slice in "${OUTPUT_DIR}"/Libbox.xcframework/ios-*; do
    for fw in "${slice}"/*.framework; do
        [ -d "${fw}" ] && "${FLATTEN}" -p "${OUTPUT_DIR}/Libbox.xcframework/Info.plist" "${fw}"
    done
done

cat <<EOF

build-libbox: done.
  ${OUTPUT_DIR}/Libbox.xcframework

Next steps (see ios/README.md):
  - link the framework into the future Xcode app + extension targets
    ("Embed & Sign", and "Do Not Embed" is wrong for static libbox slices)
  - ios/scripts/verify.sh will then build the Libbox-dependent integration
    surface instead of reporting it as an explicit boundary
EOF
