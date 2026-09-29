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

## Experimental unmatched-domain policy

In Settings choose **未命中策略** and **直连失败阈值**, or set `experiment.mode`/`experiment.threshold` in YAML. `off` (the default for new setups) **always** sends unmatched traffic direct. Existing sidecars without this setting whose already-generated `route.final` was a proxy are migrated to `on` when loaded, to avoid silently widening direct traffic. `on` forces unmatched traffic through the generated proxy exit; explicit source `REJECT`, `DIRECT`, local-network, and Tailscale bypasses retain priority. `auto` starts with unmatched traffic direct. When three **separate** TCP direct dial timeouts to the same observed hostname occur at least 10 seconds apart within 30 minutes, and the chosen proxy has a fresh successful URL test, it saves the hostname in private `auto-proxy.json`, checks the generated config, and reconnects to apply it. A successful direct response resets its counter. The threshold is configurable from 1 to 20. Learned rules go after explicit DIRECT and before explicit PROXY; they survive regeneration, and are ignored while mode is off/on. Removing a name from `auto-proxy.json` followed by generation/reconnection reverses it.

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
