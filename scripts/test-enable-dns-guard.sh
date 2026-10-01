#!/bin/bash
# shellcheck disable=SC2034,SC2329
# eval below invokes these mock functions and variables indirectly.
# Exercise the exact activation helper without booting out a real launchd job.
set -euo pipefail
script="$(dirname "$0")/enable-dns-guard.sh"
function_body="$(awk '/^bootstrap_root\(\) \{/ { capture=1 } /^rollback\(\) \{/ { capture=0 } capture' "$script")"
[[ "$function_body" == bootstrap_root* ]] || exit 1
(
  eval "$function_body"
  backup="$(mktemp -d)"
  trap 'rm -rf "$backup"' EXIT
  plist=/nonexistent/test.plist
  label=test.bootstrap.race
  attempts=0
  sudo() {
    attempts=$((attempts + 1))
    if (( attempts < 3 )); then
      echo 'Bootstrap failed: 5: Input/output error' >&2
      return 5
    fi
    return 0
  }
  launchctl() { return 1; }
  sock_cmd() { return 1; }
  sleep() { :; }
  bootstrap_root
  [[ $attempts -eq 3 ]] || { echo "Expected bootstrap retry, got $attempts attempts" >&2; exit 1; }
  echo 'launchd transient-bootstrap retry passed'
)
(
  eval "$function_body"
  backup="$(mktemp -d)"
  trap 'rm -rf "$backup"' EXIT
  plist=/nonexistent/test.plist
  label=test.bootstrap.failure
  attempts=0
  sudo() { attempts=$((attempts + 1)); echo 'permanent failure' >&2; return 5; }
  launchctl() { return 1; }
  sock_cmd() { return 1; }
  sleep() { :; }
  if bootstrap_root >/dev/null 2>"$backup/result"; then
    echo 'Permanent failure was silently accepted' >&2
    exit 1
  fi
  [[ $attempts -eq 15 ]] || { echo "Expected bounded retries, got $attempts" >&2; exit 1; }
  grep -q 'Root service did not restart after retries' "$backup/result"
  echo 'launchd permanent-bootstrap failure reported'
)
rollback_body="$(awk '/^rollback\(\) \{/ { capture=1 } /^committed=false$/ { capture=0 } capture' "$script")"
[[ "$rollback_body" == rollback* ]] || exit 1
(
  eval "$rollback_body"
  dir="$(mktemp -d)"
  backup="$(mktemp -d)"
  trap 'rm -rf "$dir" "$backup"' EXIT
  printf 'previous config' > "$backup/config.json"
  printf 'previous yaml' > "$backup/sakamoto.yaml"
  printf 'candidate config' > "$dir/config.json"
  printf 'candidate yaml' > "$dir/sakamoto.yaml"
  was_connected=false
  command_log="$backup/commands"
  sock_cmd() {
    echo "$1" >> "$command_log"
    case "$1" in
      status) echo disconnected ;;
      disconnect) echo 'not running' ;;
      connect) echo 'connected (pid 123) dns=off' ;;
    esac
  }
  wait_child() { :; }
  shadowrocket_connected() { return 1; }
  bootstrap_root() { echo 'Unexpected bootstrap' >&2; return 1; }
  rollback
  [[ "$(<"$dir/config.json")" == 'previous config' ]]
  [[ "$(<"$dir/sakamoto.yaml")" == 'previous yaml' ]]
  grep -qx connect <(tail -n 1 "$command_log")
  echo 'failed protected DNS reconnects the previous config from standby'
)
