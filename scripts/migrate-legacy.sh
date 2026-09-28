#!/bin/bash
# One-time migration from the original dev.pi-dal launchd services.
# Must be run interactively; obtains sudo authorization BEFORE disconnecting the VPN.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
old="$HOME/.config/sakamoto"
new="$HOME/.sakamoto"
old_plist="/Library/LaunchDaemons/dev.pi-dal.sing-box.plist"
old_agent="$HOME/Library/LaunchAgents/dev.pi-dal.sakamoto-watch.plist"
uid="$(id -u)"
connected=false
watch_stopped=false
root_stopped=false
complete=false

sock_cmd() {
  python3 - "$1" "$2" <<'PY'
import socket,sys
s=socket.socket(socket.AF_UNIX);s.settimeout(5);s.connect(sys.argv[1]);s.sendall((sys.argv[2]+'\n').encode());print(s.recv(1024).decode().strip());s.close()
PY
}
check() {
  [[ -S "$old/svc.sock" ]] || { echo "No legacy daemon socket: $old/svc.sock" >&2; return 1; }
  [[ -f "$old_plist" && -f "$old_agent" ]] || { echo 'Legacy launchd files missing; migrate manually using docs/migration.md.' >&2; return 1; }
  [[ -f "$new/config.json" && -f "$new/sakamoto.yaml" ]] || { echo "Prepare $new config.json and sakamoto.yaml first." >&2; return 1; }
  command -v sing-box >/dev/null || { echo 'sing-box not installed' >&2; return 1; }
  sing-box check -c "$new/config.json"
  [[ ! -S "$new/svc.sock" ]] || { echo 'New daemon is already active.' >&2; return 1; }
  echo "Legacy daemon: $(sock_cmd "$old/svc.sock" status)"
  echo "New config: $new/config.json (validated)"
}
if [[ "${1:-}" == '--check' ]]; then check; exit; fi
[[ -t 0 ]] || { echo 'Run this in your own terminal to approve sudo and confirm the VPN switch.' >&2; exit 1; }
check
read -r -p 'Disconnect the old sakamoto VPN, move launchd services, reconnect new TUN, and rollback on failure? [y/N] ' reply
[[ "$reply" == y || "$reply" == Y ]] || exit 0
sudo -v # fail before touching connectivity if authorization is not available
backup="$old/backup-$(date +%Y%m%d-%H%M%S)"
mkdir -m 700 -p "$backup"
cp -p "$old_plist" "$old_agent" "$backup/"

rollback() {
  if [[ "$complete" == true ]]; then return; fi
  echo 'Migration did not complete; restoring legacy services...' >&2
  launchctl bootout "gui/$uid/dev.sakamoto.watch" >/dev/null 2>&1 || true
  sudo -n launchctl bootout system/dev.sakamoto.daemon >/dev/null 2>&1 || true
  rm -f "$new/svc.sock"
  if [[ "$root_stopped" == true ]]; then sudo -n launchctl bootstrap system "$old_plist" >/dev/null 2>&1 || true; fi
  if [[ "$watch_stopped" == true ]]; then launchctl bootstrap "gui/$uid" "$old_agent" >/dev/null 2>&1 || true; fi
  for _ in {1..10}; do [[ -S "$old/svc.sock" ]] && break; sleep 1; done
  if [[ "$connected" == true && -S "$old/svc.sock" ]]; then sock_cmd "$old/svc.sock" connect || true; fi
  echo 'Check VPN state and Wi-Fi proxy before continuing.' >&2
}
trap rollback EXIT
if [[ "$(sock_cmd "$old/svc.sock" status)" == connected* ]]; then connected=true; sock_cmd "$old/svc.sock" disconnect; fi
# Give the old watcher time to restore its saved Wi-Fi HTTP/HTTPS settings.
for _ in {1..10}; do [[ ! -f "$old/proxy-restore.json" ]] && break; sleep 1; done
[[ ! -f "$old/proxy-restore.json" ]] || { echo 'Old system proxy was not restored; aborting.' >&2; exit 1; }
launchctl bootout "gui/$uid/dev.pi-dal.sakamoto-watch"
watch_stopped=true
sudo -n launchctl bootout system/dev.pi-dal.sing-box
root_stopped=true
rm -f "$old/svc.sock"
bash "$root/scripts/install-macos.sh"
for _ in {1..12}; do [[ -S "$new/svc.sock" ]] && break; sleep 1; done
[[ -S "$new/svc.sock" ]] || { echo 'New daemon did not create its socket.' >&2; exit 1; }
if [[ "$connected" == true ]]; then
  [[ "$(sock_cmd "$new/svc.sock" connect)" == connected* ]] || { echo 'New TUN failed to start.' >&2; exit 1; }
  sleep 4
  ip="$(curl --noproxy '*' --fail --silent --show-error --max-time 14 https://api.ipify.org)"
  expected="$(python3 - "$new/config.json" <<'PY'
import ipaddress,json,sys
c=json.load(open(sys.argv[1]));tag=c.get('route',{}).get('final','')
for o in c.get('outbounds',[]):
 if o.get('tag')==tag:
  try:print(ipaddress.ip_address(o.get('server','')))
  except ValueError:pass
  break
PY
)"
  if [[ -n "$expected" && "$ip" != "$expected" ]]; then echo "Wrong exit $ip (expected $expected)" >&2; exit 1; fi
  curl --noproxy '*' --fail --silent --show-error --max-time 12 https://www.gstatic.com/generate_204 >/dev/null
  if scutil --nc list | grep -qiE '^\* \(Connected\).*com\.liguangming\.Shadowrocket'; then echo 'Shadowrocket VPN reconnected during migration' >&2; exit 1; fi
fi
complete=true
trap - EXIT
printf 'Migrated: %s; new daemon ready, old plist backups: %s\n' "$new" "$backup"
