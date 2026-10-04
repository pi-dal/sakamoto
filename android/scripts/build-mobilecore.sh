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
  sdkmanager "platforms;android-35" "build-tools;35.0.0" "ndk;28.2.13676358"
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

# libbox.aar and mobilecore.aar are both gomobile bindings, so each embeds
# the same go.Seq/go.Universe Java runtime classes. Keep that runtime in the
# primary libbox AAR only; Gradle rejects duplicate classes when both AARs are
# linked into one application. The mobilecore Java bridge still uses the
# identical runtime supplied by libbox.
python3 - "${OUTPUT_DIR}/mobilecore.aar" <<'PY'
import io
import os
import sys
import tempfile
import zipfile

aar_path = sys.argv[1]
with zipfile.ZipFile(aar_path, "r") as aar:
    entries = {info.filename: aar.read(info) for info in aar.infolist()}

classes = entries.get("classes.jar")
if classes is None:
    raise SystemExit("mobilecore.aar has no classes.jar")

with zipfile.ZipFile(io.BytesIO(classes), "r") as jar:
    kept = {
        info.filename: jar.read(info)
        for info in jar.infolist()
        if not info.filename.startswith("go/")
    }

classes_out = io.BytesIO()
with zipfile.ZipFile(classes_out, "w", zipfile.ZIP_DEFLATED) as jar:
    for name, data in kept.items():
        jar.writestr(name, data)
entries["classes.jar"] = classes_out.getvalue()

fd, tmp_path = tempfile.mkstemp(suffix=".aar", dir=os.path.dirname(aar_path))
os.close(fd)
try:
    with zipfile.ZipFile(tmp_path, "w", zipfile.ZIP_DEFLATED) as aar:
        for name, data in entries.items():
            aar.writestr(name, data)
    os.replace(tmp_path, aar_path)
finally:
    if os.path.exists(tmp_path):
        os.unlink(tmp_path)
PY

cat <<EOF

build-mobilecore[android]: done.
  ${OUTPUT_DIR}/mobilecore.aar

The AAR is git-ignored (android/app/libs/) — never commit build output.
Kotlin side: import com.pidal.sakamoto.mobilecore.Mobilecore
EOF
