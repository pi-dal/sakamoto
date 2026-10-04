# sakamoto iOS integration

Real integration between an iOS front end and the sakamoto core, on
sing-box **v1.14.2** `experimental/libbox` — with **built-in Tailscale**
(the sing-box Tailscale endpoint embeds the Tailscale client inside the
tunnel process; this is not a CLI probe and not an external-daemon count).

## What exists now

| Piece | Status |
|---|---|
| `Libbox.xcframework` (ios + simulator, `with_tailscale` + `with_gvisor`) | builds via `scripts/build-libbox.sh` (verified) |
| `Mobilecore.xcframework` (phase/latency/config-state folding) | builds via `scripts/build-mobilecore.sh` (verified) |
| Xcode app + PacketTunnel extension targets | `project.yml` → `xcodegen generate` (verified) |
| `ExtensionPlatformInterface` (iOS subset, upstream port) | `Extension/ExtensionPlatformInterface.swift` (verified by unsigned build) |
| Provider compiling against real Libbox API | `Extension/SakamotoPacketTunnelProvider.swift` (verified by unsigned build) |
| App-side `CoreCommanding` (mode/selection/URL test/groups) | `App/Sources/LibboxCoreCommanding.swift` |
| Built-in Tailscale adapter (status stream, exit node, logout, ping) | `App/Sources/TailscaleController.swift` + `SakamotoKit/TailscaleVocabulary.swift` |
| Auth-key storage | Keychain (`App/Sources/TailscaleKeychainStore.swift`), injected into the endpoint config at start (`TailscaleConfigInjection`) |
| Unsigned simulator build | `xcodebuild … CODE_SIGNING_ALLOWED=NO build` (verified) |
| Device build / VPN entitlement grant | **blocked on a real signing Team** (see below) — not faked |

## Architecture

```
┌─ iOS App (com.pidal.sakamoto) ─────────────────────────────────────────┐
│ SwiftUI (Home / Config / Data / Settings / About; Tailscale under      │
│   Settings)                                                            │
│   phase = MobilecoreSessionPhase(service, probe, conflict)  (Go bridge)│
│   ├─ NETunnelController (SakamotoNE)                                   │
│   │    NEVPNManager Connect/Disconnect, provider IPC (ping/reload)     │
│   ├─ LibboxCoreCommanding (CoreCommanding)                             │
│   │    CommandClient → command.sock; lifecycle follows the tunnel      │
│   │    (start on Running, stop otherwise, retry until the server       │
│   │    answers); serves mode/groups/URL-test + Data-page streams       │
│   │    (status/connections/logs)                                       │
│   └─ TailscaleController (built-in Tailscale, shares the channel)      │
│        subscribeTailscaleStatus / setTailscaleExitNode /               │
│        tailscaleLogout / startTailscalePing                            │
└──────────────┬─────────────────────────────────────────────────────────┘
               │ app-group container: command.sock (gRPC), start options
┌─ PacketTunnel (com.pidal.sakamoto.PacketTunnel) ───────────────────────┐
│ SakamotoPacketTunnelProvider (upstream lifecycle port)                 │
│   LibboxSetup → LibboxNewCommandServer(platformInterface, …)           │
│   ExtensionPlatformInterface: OpenTun over packetFlow, NWPathMonitor,  │
│   WIFI state, notifications, TailscaleHostname (device identifier)     │
│   sing-box instance: endpooints[type=tailscale] = embedded tsnet       │
└────────────────────────────────────────────────────────────────────────┘
```

## Built-in Tailscale: real capabilities

sing-box v1.14.2's Apple builder compiles `protocol/tailscale` (embedded
Tailscale client, gVisor userspace networking) by default — build tags
`with_tailscale with_gvisor ts_omit_logtail ts_omit_ssh ts_omit_taildrop …`.
The endpoint is configured as `{"type": "tailscale", …}` in `endpoints[]`.

**Supported and wired** (each maps to a Libbox v1.14.2 API, verified in the
generated bindings):

| Capability | API |
|---|---|
| Live tailnet status (state, tailnet name, self, peers, exit node) | `CommandClient.SubscribeTailscaleStatus` |
| Pick / clear exit node | `CommandClient.SetTailscaleExitNode` |
| Log out of the tailnet | `CommandClient.TailscaleLogout` |
| Per-peer latency probe (direct vs DERP relay) | `CommandClient.StartTailscalePing` |
| Login | status-driven auth-URL flow (`BackendState == NeedsLogin` + `authURL` → open in browser); optional auth key via Keychain → `TailscaleConfigInjection` at start |
| As routing source | the tailscale endpoint is an outbound/endpoint in the running core; its reachability shows in the peers' online/active facts |

**Explicitly unsupported on iOS** (rendered verbatim in the Tailscale tab;
pinned by `TailscaleVocabularyTests`):

- Taildrop (`ts_omit_taildrop` in Apple builds)
- Tailscale SSH (`ts_omit_ssh`)
- serve/funnel (no libbox API)
- login-by-auth-key as a client RPC (it is config input, not an action)
- any external-CLI/system-daemon probing (no such thing on iOS; faking it
  would be fake support)

`TailscaleBackendState` maps the wire strings `Stopped / Starting /
NeedsLogin / NeedsMachineAuth / Running` and carries unknown values verbatim
(`unrecognized`) instead of guessing.

## State vocabulary: one source of truth

The iOS side consumes the exact words of the macOS TUI
(`Disconnected / Starting / TUNRunning / Reachable / Unverified / Conflict /
Unavailable / Stopping`, probe states, node statuses, `Rule → Global →
Direct`, config states, notice kinds):

```
internal/core (Go constants)
  → pkg/mobilecore (gomobile bind → Mobilecore.xcframework)
  → ios/contract/vocabulary.json (golden, pinned by go test + swift test)
  → SakamotoKit enums (pinned by ContractAlignmentTests)
```

State *logic* (phase folding, mode cycling, probe classification, config
state machine) stays in Go (`internal/core`), is exported through
`pkg/mobilecore` (struct returns flattened into bindable functions:
`MobilecoreProbeStateOf/PathOf/ErrorOf` — gomobile skips struct-returning
funcs), and Swift renders values only. Home honors: TUN running ≠ network
reachable; one failed probe stays Unverified; selected ≠ reachable; mode
changes are the Rule → Global → Direct cycle; unavailable modes surface
"regenerate the config and reconnect" instead of silently no-oping.

## Directory map

| Path | What |
|---|---|
| `project.yml` | XcodeGen source of truth (targets, entitlements wiring, frameworks). The `.xcodeproj` is generated, git-ignored. |
| `App/Sources/` | App-target sources (compiled by Xcode only): entry, Home/Config/Data/Settings/About, Tailscale tool page, `ConfigStore` (saved config + state machine), LibboxCoreCommanding, TailscaleController, Keychain store, app-process `LibboxSetup` |
| `Extension/` | Tunnel-target sources: `SakamotoPacketTunnelProvider`, `ExtensionPlatformInterface`, `ExtensionSupport` (all carry upstream GPLv3 headers) |
| `Sources/SakamotoKit/` | SwiftPM vocabulary (`Vocabulary`, `TailscaleVocabulary`, `DataVocabulary`, `SettingsSemantics`, `TailscaleEndpointProvisioning`), IPC codec, `TunnelStartOptions`, `CoreCommanding` protocol |
| `Sources/SakamotoNE/` | `NETunnelController` (NEVPNManager / provider messages) |
| `Tests/SakamotoKitTests/` | contract alignment + IPC codec + Data folding + Settings merges + Tailscale vocabulary/injection/provisioning tests |
| `contract/vocabulary.json` | golden generated from Go |
| `scripts/build-libbox.sh` | reproducible Libbox.xcframework build (see notes below) |
| `scripts/build-mobilecore.sh` | reproducible Mobilecore.xcframework bind |
| `scripts/flatten-gomobile-framework.sh` | gomobile deep→shallow bundle fix (required for Xcode 26 embedding) |
| `Frameworks/` | built xcframeworks — **git-ignored, never committed** |

## Build from clean (verified sequence)

```sh
# 1. Toolchain (once; GOPATH/bin, not Homebrew, not global site-packages)
GOBIN="$(go env GOPATH)/bin" go install github.com/sagernet/gomobile/cmd/gomobile@latest
GOBIN="$(go env GOPATH)/bin" go install github.com/sagernet/gomobile/cmd/gobind@latest
export PATH="$PATH:$(go env GOPATH)/bin"
# (mise users: the explicit GOBIN matters — `go install` follows GOBIN when
# mise sets it, and the upstream FindMobile check looks in GOPATH/bin.)

# 2. gomobile's bind runtime must resolve from this module (pinned via tools tag)
go get github.com/sagernet/gomobile@v0.1.12   # recorded in go.mod via tools/tools.go

# 3. Frameworks (libbox ~10 min; tailscale+gvisor dominate)
ios/scripts/build-libbox.sh       # reads sing-box v1.14.2 from the go module cache,
                                  # copies it to a writable dir (gomobile must write
                                  # build/ inside the tree), builds ios+simulator,
                                  # flattens deep bundles, moves to ios/Frameworks/
ios/scripts/build-mobilecore.sh   # binds ./pkg/mobilecore the same way

# 4. Xcode project + verification
cd ios
xcodegen generate
swift test
xcodebuild -project Sakamoto.xcodeproj -scheme Sakamoto \
  -destination 'generic/platform=iOS Simulator' \
  -configuration Debug CODE_SIGNING_ALLOWED=NO build
```

## Signing: what is placeholder, what is real

- Bundle IDs `com.pidal.sakamoto` / `com.pidal.sakamoto.PacketTunnel` and app
  group `group.com.pidal.sakamoto` are **development placeholders** (set in
  `project.yml`, the entitlements files, and mirrored into each target's
  Info.plist via `SakamotoAppGroupIdentifier` / `SakamotoProviderBundleIdentifier`).
  Replace all four places together.
- `DEVELOPMENT_TEAM` is empty in `project.yml`; set it (or an xcconfig) for
  device builds.
- The entitlements are **real declarations**: `packet-tunnel-provider` under
  `com.apple.developer.networking.networkextension`, plus `application-groups`.
  There is no stub path: Apple grants the NetworkExtension entitlement only
  through a provisioned profile for a real Team. Until then:
  - unsigned simulator build: works (`CODE_SIGNING_ALLOWED=NO`), the VPN
    UI/NE APIs are compile-time real but cannot start a tunnel;
  - device build: blocked on Team + capabilities — a documented blocker, not
    a pass.

## Next minimal manual steps

1. Set `DEVELOPMENT_TEAM` in `ios/project.yml`; replace the placeholder
   bundle IDs / app group if desired; `xcodegen generate`.
2. Register an App ID with the NetworkExtension capability for both bundle
   IDs, create the app group, and let Xcode refresh provisioning.
3. Run on device; paste a generated sing-box config (or one produced by
   `sakamoto` on the host) in Config → Save → Regenerate + Reconnect.
4. Optional: store a Tailscale auth key (Tailscale tab) — or leave it empty
   and use the auth-URL login flow on first status.
5. Config generation on-device (internal/gen via mobilecore) remains the
   known follow-up; the Config screen states this boundary in-product.
