#!/usr/bin/env bash
# Compatibility entry point. Never create a second gomobile Go runtime.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec "${SCRIPT_DIR}/build-libbox.sh" "$@"
