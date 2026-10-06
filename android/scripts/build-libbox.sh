#!/usr/bin/env bash
# Build one Android AAR containing both libbox and our mobilecore bridge.
# Both packages must share gomobile's JNI runtime: independently generated
# AARs each provide go.Seq but can initialize only their own native library.
# Use the pinned upstream builder unchanged except for adding our bind target.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION=v1.14.2
SOURCE="${SING_BOX_SOURCE:-$(go env GOMODCACHE)/github.com/sagernet/sing-box@${VERSION}}"
: "${ANDROID_HOME:?Run mise -E android run android:sdk first}"
: "${ANDROID_NDK_HOME:?Set ANDROID_NDK_HOME to NDK 28.2.13676358}"
[ -d "${SOURCE}" ] || { echo "Missing pinned sing-box source: run mise run mobile:tools" >&2; exit 1; }
WORK="$(mktemp -d "${TMPDIR:-/tmp}/sakamoto-android-bind.XXXXXX")"
trap 'rm -rf "${WORK}"' EXIT
cp -R "${SOURCE}" "${WORK}/sing-box"
chmod -R u+w "${WORK}/sing-box"

# The Android bind keeps the official tags, linker flags, API levels and
# io.nekohasekai package prefix. Its additional package is local project code.
python3 - "${WORK}/sing-box/cmd/internal/build_libbox/main.go" <<'PY'
from pathlib import Path
import sys
p = Path(sys.argv[1])
s = p.read_text()
anchor = '_ "github.com/sagernet/gomobile"'
assert s.count(anchor) == 1, "Upstream builder import drift"
s = s.replace(anchor, anchor + '\n\t_ "github.com/pi-dal/sakamoto/pkg/mobilecore"\n\t_ "github.com/pi-dal/sakamoto/pkg/mobileexperiment"')
anchor = 'args = append(args, "./experimental/libbox")'
assert s.count(anchor) == 2, "Upstream builder target drift"
s = s.replace(anchor, 'args = append(args, "./experimental/libbox", "github.com/pi-dal/sakamoto/pkg/mobilecore", "github.com/pi-dal/sakamoto/pkg/mobileexperiment")', 1)
p.write_text(s)
PY
cd "${WORK}/sing-box"
go mod edit -require=github.com/pi-dal/sakamoto@v0.0.0 "-replace=github.com/pi-dal/sakamoto=${REPO_ROOT}"
GOFLAGS=-mod=mod go run ./cmd/internal/build_libbox -target android
mkdir -p "${REPO_ROOT}/android/app/libs"
cp libbox.aar "${REPO_ROOT}/android/app/libs/libbox.aar"
# Remove obsolete independently-bound artifacts; Gradle links only libbox.aar.
rm -f "${REPO_ROOT}/android/app/libs/mobilecore.aar"
echo "Built combined libbox + mobilecore: android/app/libs/libbox.aar"
