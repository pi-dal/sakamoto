#!/usr/bin/env bash
# Bind libbox and mobilecore once: one Go runtime per app/extension process.
# Preserve the pinned upstream Apple tags, platform flags and linker options.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION=v1.14.2
SOURCE="${SING_BOX_SOURCE:-$(go env GOMODCACHE)/github.com/sagernet/sing-box@${VERSION}}"
TARGETS="${LIBBOX_TARGETS:-ios,iossimulator}"
OUTPUT_DIR="${IOS_FRAMEWORK_OUTPUT_DIR:-${REPO_ROOT}/ios/Frameworks}"
[ -d "${SOURCE}" ] || { echo "Missing sing-box ${VERSION}: run mise run mobile:tools" >&2; exit 1; }
if [ -d "${SOURCE}/.git" ]; then
    DETECTED_VERSION="$(git -C "${SOURCE}" describe --tags --abbrev=0 2>/dev/null || true)"
else
    case "$(basename "${SOURCE}")" in
        *"@${VERSION}"*) DETECTED_VERSION="${VERSION}" ;;
        *) DETECTED_VERSION=unknown ;;
    esac
fi
[ "${DETECTED_VERSION}" = "${VERSION}" ] || [ "${LIBBOX_ALLOW_OTHER_VERSION:-0}" = 1 ] || {
    echo "Expected sing-box ${VERSION}, found ${DETECTED_VERSION}" >&2; exit 1;
}
[ -x "$(go env GOPATH)/bin/gomobile" ] && [ -x "$(go env GOPATH)/bin/gobind" ] || {
    echo "Missing mise-managed gomobile/gobind: run mise run mobile:tools" >&2; exit 1;
}
WORK="$(mktemp -d "${TMPDIR:-/tmp}/sakamoto-ios-bind.XXXXXX")"
trap 'rm -rf "${WORK}"' EXIT
cp -R "${SOURCE}" "${WORK}/sing-box"
chmod -R u+w "${WORK}/sing-box"

python3 - "${WORK}/sing-box/cmd/internal/build_libbox/main.go" <<'PY'
from pathlib import Path
import sys
p = Path(sys.argv[1])
s = p.read_text()
anchor = '_ "github.com/sagernet/gomobile"'
assert s.count(anchor) == 1, "Upstream builder import drift"
s = s.replace(anchor, anchor + '\n\t_ "github.com/pi-dal/sakamoto/pkg/mobilecore"\n\t_ "github.com/pi-dal/sakamoto/pkg/mobileexperiment"\n\t_ "github.com/pi-dal/sakamoto/pkg/mobilegen"')
anchor = 'args = append(args, "./experimental/libbox")'
assert s.count(anchor) == 2, "Upstream bind target drift"
# Only change the Apple occurrence, never the Android builder.
pos = s.index('func buildApple()')
s = s[:pos] + s[pos:].replace(anchor, 'args = append(args, "./experimental/libbox", "github.com/pi-dal/sakamoto/pkg/mobilecore", "github.com/pi-dal/sakamoto/pkg/mobileexperiment", "github.com/pi-dal/sakamoto/pkg/mobilegen")', 1)
p.write_text(s)
PY
cd "${WORK}/sing-box"
go mod edit -require=github.com/pi-dal/sakamoto@v0.0.0 "-replace=github.com/pi-dal/sakamoto=${REPO_ROOT}"
GOFLAGS=-mod=mod go run ./cmd/internal/build_libbox -target apple -platform "${TARGETS}"
[ -d Libbox.xcframework ] || { echo "Combined Libbox.xcframework not produced" >&2; exit 1; }
for slice in Libbox.xcframework/ios-*; do
    for fw in "${slice}"/*.framework; do
        [ ! -d "${fw}" ] || "${REPO_ROOT}/ios/scripts/flatten-gomobile-framework.sh" -p Libbox.xcframework/Info.plist "${fw}"
    done
done
mkdir -p "${OUTPUT_DIR}"
rm -rf "${OUTPUT_DIR}/Libbox.xcframework"
mv Libbox.xcframework "${OUTPUT_DIR}/Libbox.xcframework"
echo "Built combined libbox + mobilecore + mobileexperiment: ${OUTPUT_DIR}/Libbox.xcframework"
