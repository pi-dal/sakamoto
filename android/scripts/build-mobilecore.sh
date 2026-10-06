#!/usr/bin/env bash
# Compatibility entry: Android now binds libbox + mobilecore in one AAR.
# Standalone mobilecore AARs cannot safely share gomobile's JNI runtime.
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
exec "${REPO_ROOT}/android/scripts/build-libbox.sh"
