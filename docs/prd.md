# Sakamoto product requirements (PRD)

Version: 1.1 · Updated: 2026-10-22 · Status: implementation baseline

Related documents: [README](../README.md), [TUI](tui.md), [configuration](configuration.md), [DNS protection](dns-protection.md), [import](import.md), [iCloud](icloud.md), [S3 sync](s3-sync.md), [migration](migration.md), [virtual location feasibility](virtual-location-feasibility.md), [virtual location setup](virtual-location-setup.md), [release checklist](release-checklist.md), and [skills](../skills.md).

This document describes implemented behavior and its verification boundaries. Limitations are part of the product contract; the project does not claim feature parity with another product.

## 1. Product overview

Sakamoto is a cross-platform proxy controller built around SagerNet sing-box v1.14.2+ through its native API and libbox. Its Home, Config, Data and Settings workflow takes inspiration from Shadowrocket. The project is not affiliated with or endorsed by Shadowrocket or SagerNet.

| Platform | Application | Distribution |
|---|---|---|
| macOS | Mouse-enabled terminal TUI and launchd services | Homebrew tap and source builds |
| iOS | Native SwiftUI app and PacketTunnel extension | TestFlight and signed distribution builds |
| Android | Native Material 3 Kotlin app | Signed APKs; Android 7.0+ |

Shared Go packages provide consistent configuration semantics across platforms. Shadowrocket `.conf` import is supported. Protected DNS, fallback monitoring and virtual location require explicit opt-in.

## 2. Problems addressed

1. macOS users need a scriptable, auditable controller for a core that does not provide its own desktop GUI.
2. Existing Shadowrocket subscriptions, rules and includes need a faithful import path that preserves the previous working configuration on failure.
3. Proxy routing, DNS, system proxy settings and fallback involve several components. Their state and recovery behavior must be visible.
4. Virtual location needs an honest account of platform feasibility and limits, with implementation guided by that account.

## 3. Intended users

- Primarily, advanced individual users who already have Shadowrocket or sing-box configurations and want a terminal workflow on macOS.
- Also, users who want the same node and rule semantics on iOS and Android.
- Out of scope: subscription marketplaces, shared multi-user distribution, and Shadowrocket HTTP rewrite, MITM or JavaScript response scripting parity.

## 4. Product invariants

1. Never commit private runtime files: API secrets, `nodes.txt`, subscription tokens, `config.json`, compiled `.srs` files, logs, restore snapshots, learned domains or pending keys.
2. SOCKS exits use `MainProxy` and `detour` in the defined chain direction. Shadowrocket `FINAL,DIRECT` maps to `route.final=direct`.
3. Ordered fallback is `RealityAuto` to `OthersAuto`. Switches require fresh URL-test results, and manual selections are preserved.
4. Shadowrocket and sing-box must not own active TUN/VPN sessions simultaneously. The desktop supervisor stops sing-box when it detects the competing VPN.
5. Import fails closed. Missing includes or remote rule sets must not replace the previous usable configuration.
6. Status labels distinguish `Untested`, `Testing`, `Reachable Nms` and `Failed/timed out`. Selection alone does not prove reachability.
7. iCloud and S3 sync source files only, including supported relative include graphs. Generated state, logs, sockets and credentials remain local. Concurrent edits are not overwritten.
8. A successful probe proves only its tested path. Protected DNS is not a zero-leak guarantee. Virtual location is not a system-wide GPS override.

## 5. Functional requirements and implementation

### 5.1 macOS TUI

| Feature | Implemented behavior |
|---|---|
| Home | Connect/disconnect; separate core-start and network-probe states; chain and SOCKS exit summary; group latency; mouse selection, testing and details |
| Traffic mode | Cycle Rule, Global and Direct through the native API and verify the result; report when an old configuration needs regeneration; retain private/Tailscale exclusions, ad rejection and configured STUN/QUIC blocking |
| Config | Import local or remote Shadowrocket `.conf` with relative includes; inspect General, rules and DNS; manage sources, node links and proxy/direct/reject policies |
| Policy forms | Exact names, `*.` suffixes, `keyword:`, `cidr:` and supported remote rule URLs |
| Settings forms | Edit `sakamoto.yaml` in overlays; cycle choices or focus custom values; save with `Ctrl+S`, discard with `Esc` |
| Regenerate | Rebuild `config.json` and rule sets and refresh remote sources; explicit application/reconnection activates a newly generated configuration |
| Data | Traffic, active connections with routing details and close actions, and logs including server-address warnings |
| Settings | Protected DNS, fallback, system proxy, S3, iCloud and experiments |
| About | Half-block portrait with truecolor, ANSI-256 and ASCII fallbacks; attribution and non-affiliation notices; `SAKAMOTO_ASCII_ART=1` |
| Keyboard | Tab/1-5, j/k, Enter, Esc, c, u, a, n, e, g, m and q; SGR mouse input |
| Speed test | Implemented in `internal/tui/speedtest.go` |
| Masked editing | Hide node links and subscription URLs by default; `Ctrl+R` reveals and `Ctrl+U` clears |

### 5.2 macOS services

| Feature | Implemented behavior |
|---|---|
| `sakamoto setup` | Interactive LaunchDaemon/LaunchAgent installation; read-only `--check`; reject existing or incomplete installations; do not start the VPN |
| Root supervisor | Manage the sing-box TUN and accept user connect/disconnect requests through a mode-0600 socket |
| Watcher | Run ordered fallback without a TUI; require configured healthy rounds before switching back; respect manual selection |
| `sakamoto recover` | Request fresh MainProxy URL tests without restarting the TUN or forcing a node; single watcher, owner-only socket and rate limits |
| Restart boundary | `RunAtLoad` and `KeepAlive` start services, not the TUN; DNS snapshot recovery and port ownership remain guarded |
| API rotation | Stage a private key for the next normal connection; `--apply-now` explicitly permits a reconnect; never print keys |
| System proxy | Opt-in HTTP/HTTPS proxy while connected when the mixed inbound is enabled; restore saved state on disconnect |
| Protected DNS | Opt-in TCP/UDP loopback port 53 in the same core; verify both transports before takeover; retain static/DHCP snapshots and guarded restoration; remain protected in Direct mode |
| DNS commands | `dns-prepare` and `dns-restore` expose preparation and restoration boundaries |
| VPN exclusion | Stop sing-box when Shadowrocket reconnects |
| Legacy migration | Back up and migrate `~/.config/sakamoto` to `~/.sakamoto`, restore system proxy state and roll back failures; do not replace service definitions over active sockets |

### 5.3 Learning and observation

- Unmatched routing supports `off` (direct), `on` (proxy) and `auto` (verified learning).
- Auto learning correlates current unmatched direct failures, confirms the tested origin through direct and proxy exits, and verifies ordinary routing after applying a candidate. It does not retry authenticated application flows or treat HTTP challenges as recovery.
- The direct-failure threshold defaults to 3 and is configurable from 1 to 20.
- Cloudflare region learning is independently enabled and requires a region-specific signal, such as error 1009. Ordinary 403 responses, challenges, DNS failures and already-proxied routes are insufficient.
- Desktop generation prepares a dedicated native local rule set. Learning and rollback hot-update that file without restarting the core or TUN. Older or unapplied configurations defer until explicit application.
- Verification runs in a background worker so groups and recovery requests remain responsive. Recovery reports `busy` during learning.
- Mobile learning updates dedicated native domain matching, verifies the installed route, and restores prior rules on failure while retaining the same core.
- Learned state and bounded result explanations remain private. Experiment pages expose those explanations.
- Detailed mobile subscriptions follow page visibility and learning state; disabled learning does not retain its connection/log subscription. See [mobile observation](mobile-observation.md), [desktop learning](desktop-learning.md) and [mobile learning](mobile-learning.md).

### 5.4 iOS

| Feature | Implementation and boundary |
|---|---|
| Native core | Combined libbox/mobilecore framework for devices and simulators, with Tailscale and gVisor; one Go runtime per process |
| App and VPN | XcodeGen-managed SwiftUI app, PacketTunnel extension and shared packages; iOS 16 deployment target |
| Tailscale | Status stream, exit node, logout and ping; Keychain auth storage; configuration injection at startup |
| Configuration | Import, policy, nodes/sources and Generate/Apply backed by shared Go parsing and generation |
| Navigation | Home, Config, Data and Settings; chain and rule inspection; batch node tests |
| iCloud | Actor-based source sync, conflict-safe baselines, file coordination and security-scoped source-folder selection; production export entitlements verified |
| System surfaces | Small/medium/large widgets, interactive widgets on iOS 17+, Shortcuts/Siri, and Control Center on iOS 18+ using saved profiles |
| Virtual location | Trusted device CA and Apple WLOC response rewriting inside the tunnel; MapKit selection/search; disabled on iOS 27+ under the documented compatibility policy |
| Network recovery | Path callbacks refresh interface prefixes, flags and MTU |
| Simulator inspection | Separate Debug simulator project using SimAgentationPlus; excluded from distribution |
| Device verification | Simulator and native integration checks do not substitute for physical-device VPN and battery verification |

### 5.5 Android

| Feature | Implementation and boundary |
|---|---|
| Native client | Material 3 Kotlin VPN application with combined libbox, mobilecore/mobileexperiment bindings and JNI |
| Control | VPN diagnostics, node/group editing, widgets, notification controls and optional experiments |
| Virtual location | AOSP test-provider path and GMS fused-location adapter; MapLibre/OpenFreeMap tiles and Photon search; route stops; user must select the mock-location app |
| Mock disclosure | `isMock` is not hidden; receiving applications may reject simulated locations |
| Appearance | Route overlay remains readable in dark mode |
| Tailscale | Transport-session preservation |
| Signing | Release and debug certificates differ; changing signing identities requires explicit data preservation and migration |

### 5.6 Shared Go packages

`pkg/` contains `mobileconf` (parsing, policy and share links), `mobilecore` (core control), `mobilegen` (generation, Tailscale, relay and network routing), `mobilelocation`, `mobileexperiment`, `mobilerelay` and `sourcesync`.

Source sync uses the same `PREFIX/sources-v1.json` wire format on all three platforms, with ETag and conditional writes.

`internal/` contains configuration, core session/mode/probe state, generation, native clients, supervision, DNS/system-proxy lifecycle, watching/recovery, experiments, sync, TUI and security.

### 5.7 Optional source sync

- iCloud Drive requires opt-in. `include_conf` includes local configuration and recursive relative includes; `additional_paths` lists other source files. Watcher source reloads do not reconnect the TUN. Conflicts preserve both edits.
- S3-compatible storage uses HTTPS, SigV4, path-style requests and no redirect following. Credentials need only supported GetObject/PutObject access and remain private. The desktop watcher checks periodically and stops on conflicts.

## 6. Nonfunctional requirements

| Area | Contract |
|---|---|
| Privacy | Credentials stay local; generated state, keys and logs are excluded from sync; the location CA key uses device-only Keychain storage |
| Security | Owner-only sockets, single-watcher locking and rate limits; no key printing; no TLS-verification bypass |
| Licensing | GPL-3.0-or-later code; separately attributed CC BY 2.0 portrait |
| Quality | Shared Go tests, race checks, vet and pinned golangci-lint; native integration and E2E artifacts; terminal layout coverage at documented sizes |
| Builds | mise toolchains; arm64 and Intel CLI packaging; signature verification; trimpath/compiler size policy; macOS CGO preserved |
| Claims | Document probe, DNS, location and boot-time connection limits alongside the feature |

## 7. Acceptance scenarios

1. Fresh Homebrew installation, template copy, configuration import with relative includes, native validation, setup and explicit Connect reach the documented readiness states.
2. Starting or restarting services does not silently connect a TUN.
3. Failed import retains the previous usable configuration.
4. Shadowrocket reconnection does not leave two active VPN owners.
5. Concurrent iCloud/S3 source edits do not overwrite each other.
6. iOS location setup exposes certificate trust, selected location, opt-in activation and rewrite counters; iOS 27+ rejects unsupported activation.
7. Android explains missing mock-location selection and handles permission revocation.
8. Learning success and rollback preserve the core and existing connections; slow verification leaves recovery responsive.
9. Publication satisfies the secret audit and functional checks in [release checklist](release-checklist.md).

## 8. Known limitations

- No Shadowrocket URL/header/body rewrite, MITM or JavaScript response scripting parity.
- Compiled rule databases are not equivalent to original configuration source; imported rules require validation against the current export.
- Latency and reachability probes do not prove every application works. Protected DNS does not control application-owned DoH or hardcoded resolvers.
- Reboot does not automatically connect the TUN or activate protected DNS.
- iOS location rewriting is not GPS injection. Android simulated location remains marked as mock.
- Measured CPU samples and simulator tests do not establish physical-phone battery savings.

## 9. Open work

- Continue physical-device VPN and battery regression checks.
- Keep production iCloud identifiers and exported entitlements consistent.
- Continue fused-location adapter evaluation on GMS-certified devices.
- Preserve unrelated workspace work when preparing releases.
- Treat unattended auto-connect as a separate opt-in feature with its own lifecycle and failure testing.
