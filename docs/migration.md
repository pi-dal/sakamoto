# macOS installation and migration

## Fresh install

1. Install from the [tap](https://github.com/pi-dal/homebrew-tap): `brew install pi-dal/tap/sakamoto` (includes the sing-box dependency). This does not start a VPN or install privileged launchd jobs.
2. Copy `sakamoto.example.yaml` and `nodes.example.txt` from `"$(brew --prefix)/share/sakamoto/"` to a private `~/.sakamoto/` directory. Replace the example API secret with a random value (`openssl rand -hex 32`), and update `conf:`/`nodes_file:` to **absolute paths on your Mac**.
3. In the TUI's Config tab, import your current Shadowrocket `.conf` URL/file and its relative `include` rule file. The import fails if required include/rule-set data cannot load or if `sing-box check` fails; last working config is restored.
4. Verify `sing-box check -c ~/.sakamoto/config.json`.
5. Run `sakamoto setup --check` for a read-only preflight, then `sakamoto setup` interactively to install the macOS LaunchDaemon and LaunchAgent. Source builds can run `./sakamoto setup` from the repository root. The command refuses to replace existing/partial services and does not connect automatically.
6. Disconnect Shadowrocket VPN (Tailscale may remain connected). Run `sakamoto`, click **Connect**, and wait for *Network reachable*. Check the browser, DNS, SOCKS exit, and Tailscale before retiring Shadowrocket.

The LaunchDaemon controls TUN as root. A user-owned socket (`0600`) allows the TUI to request connect/disconnect without repeated sudo. The watcher handles group fallback and optional macOS HTTP/HTTPS proxy restore. Both launchd jobs start automatically (`RunAtLoad`/`KeepAlive`), **but they do not automatically connect sing-box**: after every reboot, log in and click **Connect**. Protected DNS also needs this explicit reconnect; its old DNS snapshot is restored on supervisor startup only when restoration succeeds and port 53 is free. See [startup and reboot behavior](dns-protection.md#startup-and-reboot-behavior).

## Existing installation at `~/.config/sakamoto`

**Never replace the root plist while sakamoto is connected.** Its process may keep TUN routes active; never remove a live socket underneath it. New binaries detect the old live socket and stay on the legacy directory until migration is deliberate.

For the original `dev.pi-dal` launchd labels, the one-command path is:

```bash
bash "$(brew --prefix)/share/sakamoto/scripts/migrate-legacy.sh" --check   # read-only preflight
bash "$(brew --prefix)/share/sakamoto/scripts/migrate-legacy.sh"           # prompts for sudo before disconnecting
```

This interactive script backs up the old plists, disconnects the old VPN, waits for the system HTTP/HTTPS proxy to restore, replaces launchd services, starts the new VPN, tests the exit IP and a 204 URL, then rolls back to the old service if any step fails. It does **not** change Shadowrocket or Tailscale. If you have different old launchd labels, use the manual sequence below.

1. Back up the old state and verify backup permissions: `cp -Rp ~/.config/sakamoto ~/.config/sakamoto.backup` (contains secrets).
2. Use the TUI to disconnect sakamoto. Ensure the watcher has restored Wi-Fi HTTP/HTTPS proxy settings (`networksetup -getwebproxy Wi-Fi` and `-getsecurewebproxy Wi-Fi`). You may reconnect Shadowrocket temporarily to keep networking while migrating.
3. Copy **source files**, not active sockets or logs, to `~/.sakamoto`: `sakamoto.yaml`, `nodes.txt`, your `macOS.conf` and its included files. Point `nodes_file:` and `conf:` at the new absolute locations. Re-import or generate under the new directory to build a new `config.json` and `rules/`. Check with `sing-box check`.
4. Stop the old services *while disconnected*: `launchctl bootout gui/$(id -u)/dev.pi-dal.sakamoto-watch` and `sudo launchctl bootout system/dev.pi-dal.sing-box`. Remove only the old, now-stale socket. The legacy LaunchAgent/LaunchDaemon plist files may then be removed from their launchd directories after backing them up.
5. After the old services and plists are removed, `sakamoto setup --check` and `sakamoto setup` install the new templates. Start `sakamoto` and confirm it reads `~/.sakamoto`, then connect after disconnecting Shadowrocket. `setup` will not migrate or overwrite a remaining legacy service for you.

If anything fails, stop the new service *while disconnected*, restore the old plists and backup, or use Shadowrocket. The installer refuses to run when it detects a legacy socket; it will not perform a potentially disruptive migration on your behalf.

## Network troubleshooting

- **TUN started but the browser fails:** verify the macOS HTTP/HTTPS proxy points to the listening mixed inbound, and watch restored it on disconnect. A `curl --noproxy '*'` check does not prove the browser path works.
- **Nodes test but TUN does not carry TCP:** try `tun_stack: gvisor`; on one Mac, `system` started without carrying TCP while `gvisor` worked.
- **Two VPNs active:** disconnect Shadowrocket before sakamoto. Tailscale routes (`100.64.0.0/10`, ULA) should stay excluded from sakamoto TUN.
- **DNS fails:** inspect `scutil --dns`, `scutil --proxy`, and `sakamoto → Data` logs. If Wi-Fi DNS is `127.0.0.1` after a reboot but no loopback listener responds, preserve `dns-restore.json` and diagnose restoration; do not simply delete the snapshot. The exporter cannot recover source rules from Shadowrocket's compiled `.db.rule` alone.
