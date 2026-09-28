#!/bin/bash
# Fresh-install helper. Never migrates or stops an existing VPN session.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
user="$(id -un)"
home="$HOME"
dir="${SAKAMOTO_DIR:-$home/.sakamoto}"
bin="${SAKAMOTO_BIN:-/opt/homebrew/bin/sakamoto}"

if [[ "$(uname -s)" != Darwin ]]; then echo 'macOS only' >&2; exit 1; fi
if [[ ! -f "$dir/sakamoto.yaml" ]]; then
  echo "Create $dir/sakamoto.yaml from sakamoto.example.yaml and edit your sources first." >&2; exit 1
fi
if [[ ! -f "$dir/config.json" ]]; then
  echo "Generate $dir/config.json via sakamoto → Config → Import before installing the daemon." >&2; exit 1
fi
if grep -Eq 'secret:[[:space:]]*(REPLACE_WITH_RANDOM_SECRET|change-me)' "$dir/sakamoto.yaml"; then
  echo 'Replace the example API secret with a unique value before installing.' >&2; exit 1
fi
if [[ -S "$home/.config/sakamoto/svc.sock" ]]; then
  echo 'Legacy sakamoto daemon detected. Stop it in the TUI before migrating; see docs/migration.md.' >&2; exit 1
fi
command -v sing-box >/dev/null || { echo 'Install sing-box via Homebrew first: brew install sing-box' >&2; exit 1; }
sing-box check -c "$dir/config.json"
if [[ ! -t 0 ]]; then echo 'Run interactively to approve installing a root LaunchDaemon.' >&2; exit 1; fi
read -r -p "Install sakamoto daemon (root) + watch for $user in $dir? [y/N] " answer
[[ "$answer" == y || "$answer" == Y ]] || exit 0
mkdir -p "$dir/logs" "$home/Library/LaunchAgents"
umask 077
tmp_plist="$(mktemp /tmp/sakamoto-daemon.XXXXXX)"
trap 'rm -f "$tmp_plist"' EXIT
build_out="$(mktemp /tmp/sakamoto-build.XXXXXX)"
trap 'rm -f "$tmp_plist" "$build_out"' EXIT
go build -C "$root" -o "$build_out" ./cmd/sakamoto
cp "$build_out" "$bin.next"
codesign -s - -f "$bin.next" >/dev/null
mv -f "$bin.next" "$bin"
python3 - "$root" "$home" "$user" "$dir" "$bin" "$tmp_plist" <<'PY'
import html,pathlib,sys
root,home,user,directory,binary,tmp=map(pathlib.Path,sys.argv[1:])
values={'@HOME@':str(home),'@USER@':str(user),'@DIR@':str(directory),'@BIN@':str(binary)}
for source,target in [
 ('dev.sakamoto.daemon.plist.in',tmp),
 ('dev.sakamoto.watch.plist.in',home/'Library/LaunchAgents/dev.sakamoto.watch.plist'),
]:
 text=(root/'launchd'/source).read_text()
 for key,value in values.items():text=text.replace(key,html.escape(value,quote=True))
 target.write_text(text)
PY
plutil -lint "$tmp_plist" "$home/Library/LaunchAgents/dev.sakamoto.watch.plist"
sudo cp "$tmp_plist" /Library/LaunchDaemons/dev.sakamoto.daemon.plist
sudo chown root:wheel /Library/LaunchDaemons/dev.sakamoto.daemon.plist
sudo chmod 644 /Library/LaunchDaemons/dev.sakamoto.daemon.plist
sudo launchctl bootstrap system /Library/LaunchDaemons/dev.sakamoto.daemon.plist
launchctl bootstrap "gui/$(id -u)" "$home/Library/LaunchAgents/dev.sakamoto.watch.plist"
printf 'Installed. Open sakamoto and click Connect after disconnecting other VPNs.\n'
