# Configuration

`sakamoto` keeps runtime state in `~/.sakamoto`:

```text
~/.sakamoto/
├── sakamoto.yaml       # user settings and source declarations
├── nodes.txt           # manual proxy share links, one per line
├── config.json         # generated sing-box configuration
├── auto-proxy.json     # local-only learned domains (experimental auto mode)
├── rules/              # generated binary rule sets
├── imports/            # cached remote Shadowrocket conf files
└── logs/
```

## Minimal settings

```yaml
api:
  url: http://127.0.0.1:9090
  secret: REPLACE_WITH_RANDOM_SECRET

fallback_enabled: true
chain_enabled: true
block_stun: true
block_quic: true
experiment:
  mode: off          # off | on | auto
  threshold: 3      # 1–20 spaced direct failures
tun_stack: gvisor
system_proxy:
  enabled: true
  service: Wi-Fi

fallbacks:
  MainProxy: [RealityAuto, OthersAuto]
```

`system_proxy.enabled` makes the launchd watcher set macOS HTTP and HTTPS proxy to the local mixed inbound (`127.0.0.1:2334`) while sakamoto is connected. The previous settings are restored on disconnect. This is useful for browsers because macOS DNS can resolve some proxy domains to unusable addresses before TUN routing sees the original hostname.

## Security

- Never commit `sakamoto.yaml`, `nodes.txt`, `config.json`, `proxy-restore.json`, or generated rule sets.
- Subscription URLs, UUIDs, passwords, Reality public keys, SOCKS credentials, and server addresses are secrets or operational metadata.
- The public repository contains parsers and examples only. Use `nodes.example.txt` and `sakamoto.example.yaml` as templates.
- The native API listens only on `127.0.0.1`, uses a private credential, and has its web dashboard disabled. Template or short API secrets are rejected when generating. **`sakamoto rotate-api` now only stages** a 32-byte random credential in private `api-rotation.pending.json` (mode `0600`); it validates the candidate but does **not** touch the live YAML/JSON, API or TUN. The next **normal TUI connect after an explicit disconnect** applies it before starting sing-box and verifies the new API; no additional disconnect is introduced. `sakamoto rotate-api --status` checks for a pending key without displaying it, `--cancel` removes it, and **`--apply-now` explicitly accepts a brief immediate reconnect**. A failed activation restores the previous configuration only if its credential was safe. Do not paste the YAML, JSON or pending file into issues. An automatic crash restart or direct daemon-socket/script `connect` bypasses TUI activation (on both legacy and migrated daemons) and continues with the old key; the pending key waits for the next TUI connect. A known-weak old key remains vulnerable until activation: use `--apply-now` if security is more urgent than avoiding a brief interruption.

## Rule, Global and Direct routing modes

Requires **sing-box 1.14.2 or newer**; 1.14.0 may lack native API mode management even if it accepts mode rules. Run `sing-box version` and `brew update && brew upgrade sing-box` if needed.

The native API mode switch is a **rule condition**, not an automatic route rewrite. Generated configs now include `clash_mode` route and DNS rules so the core exposes and actually applies all three modes. Use the Home **Mode** button or `m` to cycle them.

- **Rule:** imported split-routing rules plus the experimental unmatched policy below.
- **Global:** ordinary public traffic uses the selected proxy/chain exit before explicit DIRECT/GeoIP split rules; ordinary DNS uses a remote resolver detouring through the same exit. `experiment.mode` no longer decides the fallback while Global is selected.
- **Direct:** ordinary public traffic goes directly before proxy/learned split rules; ordinary DNS uses the local resolver without the proxy chain.

All modes keep hosts/MagicDNS, private-address and TUN route exclusions, reject/ad rules and enabled STUN/QUIC blocking. Thus Global is not a claim that every packet is proxied, and Direct is not a bypass of safety/reject rules. Proxy/DoH hostname bootstrap still uses the local resolver; system DNS outside the TUN and browser-owned DoH are not controlled by this switch. Switching clears core DNS caches but affects new connections only; it does not close existing streams. Without configured mode persistence, the core starts in Rule after a restart.

An older generated config with no `clash_mode` rules exposes only Rule. After upgrading the CLI, regenerate and validate the config, then perform **one planned TUN reconnect** to load the rules. Thereafter switching modes through the API does not require another TUN restart. A CLI update alone cannot alter the routing of a core already running an old config.

## Experimental unmatched-domain policy

In Settings, click the **Unmatched policy** three-state switch (or press Enter) to cycle **off → on → auto → off**; its highlighted value is the current selection, not a text editor. Edit **Direct failure threshold** separately, or set `experiment.mode`/`experiment.threshold` in YAML. `off` (the default for new setups) **always** sends unmatched traffic direct. Existing sidecars without this setting whose already-generated `route.final` was a proxy are migrated to `on` when loaded, to avoid silently widening direct traffic. `on` forces unmatched traffic through the generated proxy exit; explicit source `REJECT`, `DIRECT`, local-network, and Tailscale bypasses retain priority. `auto` starts with unmatched traffic direct. When three **separate** TCP direct dial timeouts to the same observed hostname occur at least 10 seconds apart within 30 minutes, and the chosen proxy has a fresh successful URL test, it saves the hostname in private `auto-proxy.json`, checks the generated config, and reconnects to apply it. A successful direct response resets its counter. The threshold is configurable from 1 to 20. Learned rules go after explicit DIRECT and before explicit PROXY; they survive regeneration, and are ignored while mode is off/on. Removing a name from `auto-proxy.json` followed by generation/reconnection reverses it.

**Scope:** auto is not a general "this website failed, change the node" feature. It learns only a hostname whose connection reached the unmatched **direct fallback**, not an explicit DIRECT/PROXY/REJECT rule. Existing proxy rules already route via the proxy, so HTTP 403, browser challenges, rate limits or server errors do not become learned routes. A fixed chained SOCKS exit may present the same public IP despite switching entry nodes. Use the watcher recovery interface for confirmed node failures; never treat an HTTP challenge alone as proof a node is dead.

**Activation:** saving a mode changes the YAML immediately; watcher monitoring reloads on its next interval, but existing generated routes are not rewritten by that save. Regenerate and reconnect once to apply a changed fallback/learned-rule configuration. In Global or Direct routing mode, mode rules take precedence over the unmatched policy; auto is useful for ordinary Rule-mode fallback.

**Limits:** sing-box 1.14 does not expose a connection failure reason or hot route update in its API. The experiment correlates timeout error logs with rule-free (`FINAL`) direct connection events; it does not learn IP-only/TUN connections whose hostname is unknown, connection refusals, DNS failures, or sites merely returning an error page. A successful *proxy health test* does not prove that a particular destination works through the proxy. Auto-learning briefly drops active connections when it safely restarts the TUN; failure validation restores previous files and tries to reconnect. It does not fix macOS system-DNS leaks, IPv6 bypasses or WebRTC/TURN beyond the separate STUN rule. `on` is the stronger default-route privacy choice, but still leaves explicitly direct/excluded traffic direct.

### External ruleset reference

[`senshinya/singbox_ruleset`](https://github.com/senshinya/singbox_ruleset) is a GPL-3.0 collection of `.srs` files built daily from `blackmatrix7/ios_rule_script`. It is not silently imported: its workflow currently compiles with sing-box `1.10.0-beta.5` and force-pushes `main` every day. Treat it as an optional source for **individual** service lists, review their semantics/overlap with the imported Shadowrocket rules, pin or verify downloaded bytes, and run `sing-box check` before activation. Do not swap an entire service collection in for `FINAL` or assume that a daily build guarantees privacy.

## Fallback policy

The fallback watcher interprets each list as an ordered priority chain. `fallback_enabled: false` disables automation without deleting the list. Both the toggle and the ordered list are editable in Settings; enter the list as `RealityAuto,OthersAuto`:

```yaml
fallbacks:
  MainProxy: [RealityAuto, OthersAuto]
```

It triggers a fresh URL test, waits for the result stream, selects the first freshly healthy group, immediately falls back when the current group is dead, and requires `recover_after` consecutive healthy checks before switching back to Reality.

Clicking a node in `ManualPick` is treated as an explicit manual override; the watcher does not override a selector choice outside the fallback chain.

### On-demand recovery trigger

`sakamoto recover` asks the **user-level watcher** for fresh `MainProxy` URL tests now, without waiting for the normal health interval. It does not select an outbound itself, access the root control socket, read the native API secret into a caller, or restart the TUN. The watcher applies its existing ordered fallback policy after results settle (at least 10 seconds).

The command prints exactly one status: `queued` (new tests scheduled), `busy` (a test is already settling), `cooldown` (a request was accepted within 90 seconds), `manual` (`ManualPick` or another explicit choice), `disabled` (automatic fallback is off or has no alternate), or `unavailable` (watcher cannot provide a valid group snapshot). `busy` and `cooldown` are not failures: the caller may wait and verify the real application path; `manual`, `disabled` and `unavailable` must **not** trigger a forced switch. A missing watcher socket exits with an error instead of trying another backend.

The interface is a small Unix socket, `watch.sock`, under the private runtime directory (mode `0600`). `watch.lock` (also `0600`) ensures there is only one watcher managing selectors. Neither file is synced to iCloud or tracked in git. `pi-proxy-guard` invokes this CLI in optional recover mode, then deep-tests the Pi provider path twice before continuing. `RealityAuto`/`OthersAuto` URL tests check entry nodes, not necessarily the chained SOCKS exit: a broken exit cannot be fixed by switching entries, so the guard remains paused and reports it.
