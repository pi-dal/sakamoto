# macOS installation and migration

## Fresh install

1. Install sing-box via Homebrew: `brew install sing-box`.
2. Build sakamoto: `go build -o sakamoto ./cmd/sakamoto`.
3. Create `~/.sakamoto/sakamoto.yaml` and `~/.sakamoto/nodes.txt` from the examples in the repository. Replace example `conf:`/`nodes_file:` with **absolute paths on your Mac**.
4. In the TUI's Config tab, import your current Shadowrocket `.conf` URL/file and its relative `include` rule file. The import fails if required include/rule-set data cannot load or if `sing-box check` fails; last working config is restored.
5. Verify `sing-box check -c ~/.sakamoto/config.json`.
6. Run `bash scripts/install-macos.sh` interactively to install the rendered macOS LaunchDaemon and LaunchAgent. It does not connect automatically.
7. Disconnect Shadowrocket VPN (Tailscale may remain connected). Run `sakamoto`, click **连接**, and wait for *已验证可用*. Check the browser, DNS, SOCKS exit, and Tailscale before retiring Shadowrocket.

The LaunchDaemon controls TUN as root. A user-owned socket (`0600`) allows the TUI to request connect/disconnect without repeated sudo. The watcher handles group fallback and optional macOS HTTP/HTTPS proxy restore.

## Existing installation at `~/.config/sakamoto`

**Never replace the root plist while sakamoto is connected.** Its process may keep TUN routes active; never remove a live socket underneath it. New binaries detect the old live socket and stay on the legacy directory until migration is deliberate.

1. Back up the old state and verify backup permissions: `cp -Rp ~/.config/sakamoto ~/.config/sakamoto.backup` (contains secrets).
2. Use the TUI to disconnect sakamoto. Ensure the watcher has restored Wi-Fi HTTP/HTTPS proxy settings (`networksetup -getwebproxy Wi-Fi` and `-getsecurewebproxy Wi-Fi`). You may reconnect Shadowrocket temporarily to keep networking while migrating.
3. Copy **source files**, not active sockets or logs, to `~/.sakamoto`: `sakamoto.yaml`, `nodes.txt`, your `macOS.conf` and its included files. Point `nodes_file:` and `conf:` at the new absolute locations. Re-import or generate under the new directory to build a new `config.json` and `rules/`. Check with `sing-box check`.
4. Stop the old services *while disconnected*: `launchctl bootout gui/$(id -u)/dev.pi-dal.sakamoto-watch` and `sudo launchctl bootout system/dev.pi-dal.sing-box`. Remove only the old, now-stale socket. The legacy LaunchAgent/LaunchDaemon plist files may then be removed from their launchd directories after backing them up.
5. `bash scripts/install-macos.sh` installs the new templates. Start `sakamoto` and confirm it reads `~/.sakamoto`, then connect after disconnecting Shadowrocket.

If anything fails, stop the new service *while disconnected*, restore the old plists and backup, or use Shadowrocket. The installer refuses to run when it detects a legacy socket; it will not perform a potentially disruptive migration on your behalf.

## Network troubleshooting

- **TUN started but the browser fails:** verify the macOS HTTP/HTTPS proxy points to the listening mixed inbound, and watch restored it on disconnect. A `curl --noproxy '*'` check does not prove the browser path works.
- **Nodes test but TUN does not carry TCP:** try `tun_stack: gvisor`; on one Mac, `system` started without carrying TCP while `gvisor` worked.
- **Two VPNs active:** disconnect Shadowrocket before sakamoto. Tailscale routes (`100.64.0.0/10`, ULA) should stay excluded from sakamoto TUN.
- **DNS fails:** inspect `scutil --dns`, `scutil --proxy`, and `sakamoto → Data` logs. The exporter cannot recover source rules from Shadowrocket's compiled `.db.rule` alone.
