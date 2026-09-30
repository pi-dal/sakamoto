# Native protected system DNS (macOS)

This opt-in feature uses **one sing-box process**, not dnsmasq/Unbound or a separate DNS daemon. A native `direct` inbound listens on loopback TCP/UDP 53; the first route rule hijacks that inbound into sing-box's DNS engine.

```yaml
dns_guard:
  enabled: false                 # opt-in; do not enable before upgrading the root supervisor
  service: Wi-Fi
  local_domains: [local, lan]     # only explicit private namespaces use LAN DNS
  bootstrap_ip: 1.12.12.12
  bootstrap_server_name: doh.pub  # normal TLS validation; never set insecure
```

## Data-plane guarantees and limits

- Ordinary public queries use the primary encrypted remote resolver through the selected proxy/chain exit. The route's ordinary destination resolver also uses it; imported DIRECT-domain DNS rules no longer send public queries to plaintext local DNS.
- Explicit local suffixes, source hosts, and Tailscale `*.ts.net` retain their private resolvers. Add a campus/internal suffix deliberately if public DNS cannot resolve it; do not use broad public TLD exceptions.
- Proxy/DoH server bootstrap uses a **separate direct, IP-pinned HTTPS resolver**, avoiding the proxy-needs-DNS-needs-proxy loop. Its TLS name is verified. Private proxy server names use the corresponding LAN/MagicDNS resolver.
- Protected DNS takes precedence over traffic mode: even when traffic mode is Direct, ordinary DNS remains encrypted/proxied while protection is enabled. Direct therefore does not mean "bypass DNS protection". Turn protection off with a reviewed regeneration/reconnect if you intentionally need entirely direct DNS.
- No claim of zero leakage: apps using their own DoH/DoT, hardcoded private DNS, a bound physical interface, other active VPNs, or IPv6 paths need independent checks. Port-53 hijacking cannot inspect HTTPS DoH.

`dns_mode: hijack` alone is not sufficient for the raw macOS CLI. Native Apple Network Extension clients manage interface DNS; the bare Darwin TUN path mainly manages routes. Our current private exclusions can even route a derived TUN DNS peer outside TUN. Loopback DNS avoids that dependency, and sakamoto performs the small system-setting transaction.

## Root-supervisor lifecycle

The root daemon owns DNS changes, not the user watcher:

1. Validate the generated native DNS listener/proxy resolver and preflight port 53 conflicts before starting.
2. Start sing-box and require successful DNS answers over **both UDP and TCP** through its encrypted resolver before touching system DNS.
3. Save the previous network-service DNS (including the distinction between static IPs and DHCP/empty) in local-only private `dns-restore.json`, set `127.0.0.1`, then verify read-back.
4. On normal disconnect, restore the saved DNS **before** terminating the loopback listener. A failed restoration retains the listener/core and snapshot rather than leaving a dead system resolver.
5. Unexpected core restarts keep the protected snapshot/loopback DNS instead of silently falling back to campus/ISP DNS. Startup may be reported as degraded until the proxy recovers. After an orphaned supervisor restart, restoration is attempted only when port 53 is unowned; manual `sakamoto dns-restore` is available while the core is stopped.

Old root daemons cannot perform this lifecycle. New clients check `native-dns-v1` capability before protected-DNS connections; upgrading only the CLI does **not** upgrade the running root process. Known DNS state must never enter iCloud or git.

## Safe activation

First update the installed CLI and finish any active Pi work. Use the explicit interactive helper:

```bash
bash "$(brew --prefix)/share/sakamoto/scripts/enable-dns-guard.sh"
```

For a source build, set `SAKAMOTO_BIN` to its absolute signed path. The helper obtains `sudo -v` **before** any network change, privately prepares/checks a candidate and captures selector defaults, backs up active files, stops the TUN once, refreshes the existing root plist (not a directory migration), verifies capability, applies the candidate and reconnects. Candidates contain machine credentials; retain their private folder because generated rule paths reference it. Do not copy the candidate or restore state to another Mac.

If health/read-back fails, activation restores previous files and reconnects the prior configuration. If system-DNS restoration itself fails, the helper refuses to destroy the still-running resolver; restore the network service administratively before stopping it. Use `ssh -t` for remote interactive authorization, never transmit or save a password in a command. A missing administrator credential is a deployment blocker, not a reason to skip safety checks.

## Verify

- `networksetup -getdnsservers Wi-Fi` should show only `127.0.0.1` while protected.
- `scutil --dns` should show the protected resolver while preserving scoped mDNS/MagicDNS behavior.
- Query both UDP and TCP locally, check ordinary proxy domains and private/Tailscale names, and inspect a DNS-leak test using known test domains. Registry geography alone is not proof of a query path.
- Test normal disconnect: original DHCP/static settings must reappear before the listener exits. Then reconnect and verify protection again.

Changing a YAML toggle alone never rewrites the running core. Generate/check and perform a planned reconnect after enabling/disabling protection; do not edit a live root plist without disconnecting first.
