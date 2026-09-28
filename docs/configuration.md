# Configuration

`sakamoto` keeps runtime state in `~/.sakamoto`:

```text
~/.sakamoto/
├── sakamoto.yaml       # user settings and source declarations
├── nodes.txt           # manual proxy share links, one per line
├── config.json         # generated sing-box configuration
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

## Fallback policy

The fallback watcher interprets each list as an ordered priority chain. `fallback_enabled: false` disables automation without deleting the list. Both the toggle and the ordered list are editable in Settings; enter the list as `RealityAuto,OthersAuto`:

```yaml
fallbacks:
  MainProxy: [RealityAuto, OthersAuto]
```

It triggers a fresh URL test, waits for the result stream, selects the first freshly healthy group, immediately falls back when the current group is dead, and requires `recover_after` consecutive healthy checks before switching back to Reality.

Clicking a node in `ManualPick` is treated as an explicit manual override; the watcher does not override a selector choice outside the fallback chain.
