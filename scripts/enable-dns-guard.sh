#!/bin/bash
# Explicit, interactive activation. No password is sent over SSH or saved.
set -euo pipefail
[[ "$(uname -s)" == Darwin ]] || { echo 'macOS only' >&2; exit 1; }
[[ -t 0 ]] || { echo 'Run interactively (ssh -t if remote) to authorize a planned reconnect.' >&2; exit 1; }
bin="${SAKAMOTO_BIN:-$(command -v sakamoto || true)}"
[[ -x "$bin" && "$bin" = /* ]] || { echo 'Install the current sakamoto CLI first.' >&2; exit 1; }
dir="${SAKAMOTO_DIR:-}"
if [[ -z "$dir" ]]; then
  if [[ -S "$HOME/.config/sakamoto/svc.sock" ]]; then dir="$HOME/.config/sakamoto"; else dir="$HOME/.sakamoto"; fi
fi
[[ -f "$dir/config.json" && -f "$dir/sakamoto.yaml" && -S "$dir/svc.sock" ]] || { echo 'Existing configured supervisor required.' >&2; exit 1; }
if [[ "$dir" == "$HOME/.config/sakamoto" ]]; then label=dev.pi-dal.sing-box; else label=dev.sakamoto.daemon; fi
plist="/Library/LaunchDaemons/$label.plist"
[[ -f "$plist" ]] || { echo 'Unknown root service layout; perform a reviewed manual upgrade instead.' >&2; exit 1; }
python3 - "$plist" "$bin" "$dir" "$HOME" <<'PY'
import os,plistlib,sys
p=plistlib.load(open(sys.argv[1],'rb'));args=p.get('ProgramArguments',[]);env=p.get('EnvironmentVariables',{})
if len(args)<2 or args[1]!='daemon' or os.path.realpath(args[0])!=os.path.realpath(sys.argv[2]):
 raise SystemExit('Root plist does not use this installed CLI; stop before network changes')
if env.get('SAKAMOTO_DIR') and env['SAKAMOTO_DIR']!=sys.argv[3]:raise SystemExit('Root service directory differs from candidate directory')
if not env.get('SAKAMOTO_DIR') and sys.argv[3] not in (sys.argv[4]+'/.sakamoto',sys.argv[4]+'/.config/sakamoto'):
 raise SystemExit('Custom runtime requires explicit root SAKAMOTO_DIR')
PY
sock_cmd() {
  python3 - "$dir/svc.sock" "$1" <<'PY'
import socket,sys
s=socket.socket(socket.AF_UNIX);s.settimeout(70);s.connect(sys.argv[1]);s.sendall((sys.argv[2]+'\n').encode());print(s.recv(65536).decode().strip());s.close()
PY
}
wait_child() {
  python3 - "$1" <<'PY'
import re,subprocess,sys,time
m=re.search(r'pid=(\d+)',sys.argv[1])
if not m:
 if sys.argv[1].startswith('connected'):raise SystemExit('Cannot identify old core PID')
else:
 for _ in range(120):
  if subprocess.run(['/bin/ps','-p',m.group(1),'-o','pid='],stdout=subprocess.DEVNULL).returncode:break
  time.sleep(.1)
 else:raise SystemExit('Old TUN still running; refusing another')
PY
}
echo 'This will prepare/validate native DNS, stop the TUN once, refresh ONLY its root supervisor, and reconnect.'
echo 'Original system DNS will be saved. No extra DNS daemon is installed.'
read -r -p 'Proceed with this planned reconnect and allow administrative changes? [y/N] ' answer
[[ "$answer" == y || "$answer" == Y ]] || exit 0
sudo -v # Authorization BEFORE any active files or tunnel state change.
umask 077
backup="$(mktemp -d "$dir/.dns-upgrade-backup.XXXXXX")"
cp "$dir/config.json" "$backup/config.json"
cp "$dir/sakamoto.yaml" "$backup/sakamoto.yaml"
cp "$plist" "$backup/daemon.plist"
candidate="$("$bin" --config "$dir/sakamoto.yaml" dns-prepare)"
[[ "$candidate" == "$dir"/.dns-candidate-* && -f "$candidate/config.json" && -f "$candidate/sakamoto.yaml" ]] || { echo 'DNS candidate was not prepared safely.' >&2; exit 1; }
was_connected=false
[[ "$(sock_cmd status)" == connected* ]] && was_connected=true
rollback() {
  echo 'Activation failed; restoring the previous configuration.' >&2
  # A failing DNS restoration must NOT destroy its still-running listener.
  old="$(sock_cmd status 2>/dev/null || true)"
  reply="$(sock_cmd disconnect 2>/dev/null || true)"
  if [[ "$reply" == 'disconnect failed:'* ]]; then
    echo 'DNS restoration failed: core/snapshot retained. Do not stop the listener; use manual administrative recovery.' >&2
    return 1
  fi
  wait_child "$old" || return 1
  cp "$backup/config.json" "$dir/config.json"
  cp "$backup/sakamoto.yaml" "$dir/sakamoto.yaml"
  if ! sock_cmd status >/dev/null 2>&1; then
    sudo -n launchctl bootstrap system "$plist" || true
    for _ in {1..30}; do sock_cmd status >/dev/null 2>&1 && break; sleep 1; done
  fi
  if [[ "$was_connected" == true ]]; then
    restored="$(sock_cmd connect)" || return 1
    [[ "$restored" == connected* ]] || { echo 'Old configuration restored but reconnect failed.' >&2; return 1; }
  fi
}
committed=false
trap '[[ "$committed" == true ]] || rollback' EXIT
if [[ "$was_connected" == true ]]; then
  old="$(sock_cmd status)"
  reply="$(sock_cmd disconnect)"
  [[ "$reply" == disconnected* ]] || { echo 'Disconnect refused; active network retained.' >&2; exit 1; }
  wait_child "$old"
fi
sudo -n launchctl bootout "system/$label"
sudo -n launchctl bootstrap system "$plist"
for _ in {1..30}; do
  [[ "$(sock_cmd capabilities 2>/dev/null || true)" == native-dns-v1 ]] && break
  sleep 1
done
[[ "$(sock_cmd capabilities)" == native-dns-v1 ]] || { echo 'Root daemon was not upgraded; no DNS override performed.' >&2; exit 1; }
# The existing plist must point to the installed binary. It is not replaced or
# migrated here. Candidates preserve API credentials and selector defaults.
cp "$candidate/config.json" "$dir/config.json.next"
cp "$candidate/sakamoto.yaml" "$dir/sakamoto.yaml.next"
mv -f "$dir/config.json.next" "$dir/config.json"
mv -f "$dir/sakamoto.yaml.next" "$dir/sakamoto.yaml"
reply="$(sock_cmd connect)"
[[ "$reply" == connected* && "$reply" == *dns=protected* ]] || { echo 'DNS did not activate as protected.' >&2; exit 1; }
committed=true
printf 'Protected native DNS is active. Backup: %s\n' "$backup"
printf 'Keep the private candidate directory: generated rule paths reference %s\n' "$candidate"
