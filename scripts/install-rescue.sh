#!/bin/bash
# Install an independently runnable offline rescue copy. Never reload a service,
# connect/disconnect a VPN, or change DNS/proxy settings.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
dir="${SAKAMOTO_DIR:-$HOME/.sakamoto}"
source_bin="${SAKAMOTO_RESCUE_BIN:-$root/scripts/sakamoto-rescue}"
base='/Library/Application Support/Sakamoto/rescue'
replace=false
check=false
for argument in "$@"; do
  case "$argument" in
    --replace) replace=true ;;
    --check) check=true ;;
    *) echo 'Supported options: --check, --replace' >&2; exit 2 ;;
  esac
done
[[ "$(uname -s)" == Darwin ]] || { echo 'macOS only' >&2; exit 1; }
[[ "$dir" = /* && -d "$dir" ]] || { echo 'SAKAMOTO_DIR must name an existing absolute runtime directory.' >&2; exit 1; }
verify_protected() {
  local path="$1" owner mode
  [[ ! -L "$path" ]] || { echo "Refusing symbolic link: $path" >&2; exit 1; }
  owner="$(/usr/bin/stat -f '%u' "$path")"
  mode="$(/usr/bin/stat -f '%Lp' "$path")"
  if [[ "$owner" != 0 ]] || (( (8#$mode & 022) != 0 )); then
    echo "Recovery path is not protected: $path" >&2
    exit 1
  fi
}
for protected in '/Library' '/Library/Application Support' '/Library/Application Support/Sakamoto' "$base"; do
  if [[ -e "$protected" || -L "$protected" ]]; then verify_protected "$protected"; fi
done
if [[ -x "$base/sakamoto-rescue" && "$replace" == false ]]; then
  verify_protected "$base/sakamoto-rescue"
  /usr/bin/codesign --verify --strict "$base/sakamoto-rescue"
  "$base/sakamoto-rescue" version
  echo 'Existing independent rescue retained. Use --replace only to deliberately replace it; a previous copy will be kept.'
  exit 0
fi
[[ -x "$source_bin" && "$source_bin" = /* ]] || { echo 'Packaged rescue binary missing. Set SAKAMOTO_RESCUE_BIN to a locally built, signed cmd/sakamoto-rescue executable; no download is attempted.' >&2; exit 1; }
/usr/bin/codesign --verify --strict "$source_bin"
"$source_bin" version
if [[ "$check" == true ]]; then
  echo 'Rescue installation preflight passed. No changes made.'
  exit 0
fi
sudo /usr/bin/install -d -o root -g wheel -m 755 "$base"
verify_protected "$base"
for previous in "$base/sakamoto-rescue" "$base/sakamoto-rescue.previous"; do
  if [[ -e "$previous" || -L "$previous" ]]; then verify_protected "$previous"; fi
done
candidate="$base/.candidate-$$"
trap 'sudo -n /bin/rm -f "$candidate" 2>/dev/null || true' EXIT
sudo /usr/bin/install -o root -g wheel -m 755 "$source_bin" "$candidate"
sudo /usr/bin/codesign --verify --strict "$candidate"
sudo "$candidate" diagnose --dir "$dir"
if [[ -e "$base/sakamoto-rescue" ]]; then
  sudo /bin/cp -p "$base/sakamoto-rescue" "$base/sakamoto-rescue.previous"
fi
sudo /bin/mv -f "$candidate" "$base/sakamoto-rescue"
printf 'Installed independent offline rescue at %s/sakamoto-rescue\n' "$base"
printf 'Read-only: %q diagnose --dir %q\n' "$base/sakamoto-rescue" "$dir"
printf 'Explicit exit: sudo %q exit --dir %q --restore-network\n' "$base/sakamoto-rescue" "$dir"
printf 'Clean user shell: %q shell\n' "$base/sakamoto-rescue"
