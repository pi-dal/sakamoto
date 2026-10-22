# Configuration

`sakamoto` keeps runtime state in `~/.sakamoto`:

```text
~/.sakamoto/
├── sakamoto.yaml       # user settings and source declarations
├── nodes.txt           # manual proxy share links, one per line
├── config.json         # generated sing-box configuration
├── auto-proxy.json     # local-only learned domains (experimental auto mode)
├── sources/            # local conf/include sources; optional source-only iCloud sync
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
  cf_region_block: false  # learn explicit Cloudflare region blocks
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
- **Direct:** ordinary public traffic goes directly before proxy/learned split rules; ordinary DNS uses the local resolver without the proxy chain **unless protected system DNS is explicitly enabled**, which keeps public DNS encrypted/proxied independently of traffic mode.

All modes keep hosts/MagicDNS, private-address and TUN route exclusions, reject/ad rules and enabled STUN/QUIC blocking. Thus Global is not a claim that every packet is proxied, and Direct is not a bypass of safety/reject rules. Proxy/DoH hostname bootstrap still uses the local resolver; system DNS outside the TUN and browser-owned DoH are not controlled by this switch. Switching clears core DNS caches but affects new connections only; it does not close existing streams. Without configured mode persistence, the core starts in Rule after a restart.

An older generated config with no `clash_mode` rules exposes only Rule. After upgrading the CLI, regenerate and validate the config, then perform **one planned TUN reconnect** to load the rules. Thereafter switching modes through the API does not require another TUN restart. A CLI update alone cannot alter the routing of a core already running an old config.

## User policy overrides

Open **Config → Policy** to add rules without hand-editing the Shadowrocket source. Each rule has a match and an action:

```yaml
policy:
  - match: "example.com"
    action: proxy
  - match: "*.corp.example"
    action: direct
  - match: "keyword:tracker"
    action: reject
  - match: "cidr:192.0.2.0/24"
    action: direct
```

A plain hostname is an exact domain match. `*.example.com` matches the suffix; `keyword:` matches a domain keyword; `cidr:` matches an IP range. An `http(s)` URL is accepted for convenience, but only its hostname is routable by the TUN—URL paths such as `/login` are not distinguished. In the TUI, `proxy`, `direct`, and `reject` are selectable options; the match is an editable field.

User rules are merged before generation and survive refreshing or replacing the imported `.conf`. `reject` has priority over routing, and `direct` has priority over `proxy` when the same match appears in both. Explicit private/Tailscale exclusions remain direct. Global and Direct modes still take precedence over ordinary public split routing, so these rules are primarily for Rule mode.

## Native protected system DNS

Optional `dns_guard.enabled` uses a sing-box loopback DNS inbound and a root-supervisor transaction to save/restore macOS DNS. It is off by default, requires updating the root daemon while disconnected, and cannot be activated by upgrading only the CLI. **This setting persists the configuration, not a boot-time connection:** `sakamoto setup` installs/checks the daemon/watch jobs, but after a reboot the TUN and protected DNS require an explicit Connect. Read [activation, rollback, startup behavior and scope limits](dns-protection.md) before enabling it. `sakamoto dns-prepare` makes a private candidate without changing live files; `sakamoto dns-restore` recovers an orphaned snapshot only while the core is stopped. `dns-restore.json` stays local and is forbidden in iCloud source sync.

## Experimental unmatched-domain policy

In Settings, click the **Unmatched policy** switch (or press Enter) to cycle **off → on → auto → off**. `off` sends unmatched traffic direct; `on` sends it through the configured proxy exit. Explicit DIRECT/PROXY/REJECT, private-network and Tailscale rules retain their normal priority. `auto` starts with unmatched traffic direct.

On desktop, an observed unmatched direct TCP dial timeout, refusal, reset or unreachable-network error starts a bounded comparison for HTTP(S) origins on ports 80/443. The watcher checks the same origin through the actual direct and chained proxy exits. If direct recovers or the proxy does not return an acceptable response, it saves nothing. The **Direct failure threshold** (1–20, default 3) counts the original failure plus active direct confirmations, so users do not have to manually retry a broken website three times. Independent passive failures still require at least 10 seconds between counts within 30 minutes; a successful direct response resets their count. A fresh successful proxy health test remains required.

Only a confirmed improvement becomes a candidate in the private `auto-proxy-rules.json` local rule set. The core hot-loads this dedicated file for new connections; the watcher then checks the target through ordinary routing and commits `auto-proxy.json` only on success. Failed verification restores the previous live file. **Automatic learning never reconnects the TUN or restarts the core**, including setup and rollback. Learned rules go after explicit DIRECT and before explicit PROXY, survive regeneration, and are ignored in off/on unless the separate Cloudflare experiment is enabled. Existing learned entries are retained; new verification does not retroactively certify them.

Set `experiment.cf_region_block: true` for the separate Cloudflare experiment. It requires an explicit regional restriction signal correlated with unmatched direct traffic and a healthy proxy, then uses the same desktop verification before saving a route. Generic 403s and challenges are not learning triggers. Ordinary HTTPS response bodies are encrypted and generally absent from core logs, so this option cannot detect every Cloudflare regional error shown in a browser.

**Scope:** these experiments are not a general "this website failed, change the node" feature. They learn only a hostname whose connection reached the unmatched **direct fallback**, not an explicit DIRECT/PROXY/REJECT rule. Existing proxy rules already route via the proxy, so HTTP 403, browser challenges, rate limits or server errors do not become learned routes. A fixed chained SOCKS exit may present the same public IP despite switching entry nodes. Use the watcher recovery interface for confirmed node failures; never treat an HTTP challenge alone as proof a node is dead.

**Activation:** saving a mode changes the YAML immediately; watcher monitoring reloads on its next interval, but existing generated routes are not rewritten by that save. Regenerate and reconnect once to apply a changed fallback/learned-rule configuration. In Global or Direct routing mode, mode rules take precedence over the unmatched policy; auto is useful for ordinary Rule-mode fallback.

**Limits and observation:** Learning correlates current connection events and error logs, tolerates their arrival in either order, and ignores historical replay. Unknown hostnames, DNS-only failures, non-HTTP(S) ports and error pages alone do not become learned rules. Origin checks use bounded HEAD requests to `/`, verify TLS certificates, send no application credentials and do not follow redirects. HTTP 403, 429 and server errors do not count as recovery; 401, 404 and 405 establish origin reachability but do not prove an authenticated application works. There is a five-minute per-domain verification cooldown and a 30-second global start limit. One background worker performs origin checks while the watcher continues consuming groups and answering recovery; recovery reports `busy` during learning.

Run `sakamoto learning` to inspect private `learning-status.json` explanations (`verified`, `not_improved`, `direct_recovered`, `proxy_unhealthy`, `deferred`, `rolled_back`, `skipped` or `failed`) alongside the saved domains. At most 100 results are kept locally and excluded from source sync. New generated configs include verification listeners and the dedicated local file when learning is enabled. Older configurations remain `deferred` until you explicitly regenerate and apply them; starting or upgrading a watcher cannot interrupt a connected session. A changed configuration must belong to the current core before learning is allowed. Traffic deltas update every five seconds while lifecycle events arrive immediately; disabled learning has no connection/log subscription. Native subscription APIs use `time.Duration` with a one-second floor. See [failure coverage and repeatable verification](desktop-learning.md).

### Mobile learning

iOS and Android use the same candidate tracking, origin comparison, threshold confirmation and HTTP acceptance policy. Their native bridge probes the running core's configured exits directly, using its DNS, trust pool and protected sockets. No extra listeners or subscriptions are created for verification. A candidate changes only the dedicated inline automatic rule set, then checks the target through the ordinary router before saving it; failed verification restores the previous live set without restarting the core, TUN or Tailscale. Network, selector, routing-mode changes and profile edits prevent a stale result from being committed.

Mobile verification runs only while learning is enabled and a correlated failure arrives. It processes one candidate at a time, starts at most once per 30 seconds and checks each domain at most once per five minutes. Each comparison has a 25-second budget and the installed-route check has an eight-second deadline. **Recent checks** in Experiment shows the ten latest explanations; at most 50 are stored locally. Existing learned rules remain; only newly verified routes receive these records. See [mobile failure coverage and native verification](mobile-learning.md).

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
