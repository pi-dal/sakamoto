# sakamoto iOS integration

Real integration between an iOS front end and the sakamoto core, on
sing-box **v1.14.2** `experimental/libbox` — with **built-in Tailscale**
(the sing-box Tailscale endpoint embeds the Tailscale client inside the
tunnel process; this is not a CLI probe and not an external-daemon count).

## What exists now

| Piece | Status |
|---|---|
| Combined `Libbox.xcframework` (libbox + mobilecore, iOS + simulator, `with_tailscale` + `with_gvisor`) | builds via `scripts/build-libbox.sh`; one Go runtime per process |
| Xcode app + PacketTunnel extension targets | `project.yml` → `xcodegen generate` (verified) |
| `ExtensionPlatformInterface` (iOS subset, upstream port) | `Extension/ExtensionPlatformInterface.swift` (verified by unsigned build) |
| Provider compiling against real Libbox API | `Extension/SakamotoPacketTunnelProvider.swift` (verified by unsigned build) |
| App-side `CoreCommanding` (mode/selection/URL test/groups) | `App/Sources/LibboxCoreCommanding.swift` |
| Built-in Tailscale adapter (status stream, exit node, logout, ping) | `App/Sources/TailscaleController.swift` + `SakamotoKit/TailscaleVocabulary.swift` |
| Auth-key storage | Keychain (`App/Sources/TailscaleKeychainStore.swift`), injected into the endpoint config at start (`TailscaleConfigInjection`) |
| Config tab: Import / Policy / Nodes & sources / Generate·Apply | `App/Sources/ConfigView.swift` + `ConfigStore.swift`; parsing/validation via `pkg/mobileconf` + `pkg/mobilecore/importer.go` (real, tested) |
| iCloud source sync (Settings → Sync sources to iCloud) | `SakamotoKit/ICloudSyncStore.swift` (actor, conflict-safe baseline pass, unit-tested in memory) + `App/Sources/ICloudSyncModel.swift` + real ubiquity container / `NSFileCoordinator` writes; entitlements are real declarations with a **placeholder container id** |
| Unsigned simulator build | `xcodebuild … CODE_SIGNING_ALLOWED=NO build` (verified) |
| Home Screen widgets (small/medium/large) | `Widgets/SakamotoWidgets.swift`; interactive on iOS 17+, opens the app to execute VPN actions |
| Shortcuts / Siri | Connect, Disconnect, Toggle and Get VPN status in `Shared/SystemSurfaceIntents.swift` |
| Control Center control | Native VPN toggle on iOS 18+, directly operates the saved profile without opening the app |
| App Store Connect signing | distribution export verified with Team `6Y2YB464VU`, NetworkExtension, shared App Group and production iCloud entitlements; device VPN behavior still needs runtime verification |

## SimAgentationPlus simulator debugging

With a simulator already booted, run from the repository root:

```sh
mise run ios:inspect
# Select a specific already-booted simulator:
IOS_SIMULATOR_UDID=<simulator-UDID> mise run ios:inspect
```

This builds `project.sim-agentation.yml` as the separate
`SakamotoInspector.xcodeproj`, using SimAgentationPlus **0.2.2** and iOS 17.
Only the App target links the SDK; PacketTunnel and Widget targets do not.
The inspector starts on the window root in Debug simulators. Home, Config,
Data and Settings carry source tags, with finer tags for connection, routing,
configuration selection/import/apply and Experiment. The normal `project.yml`
continues to target iOS 16 with **no SDK dependency**; use it for every release.

Open the existing SimAgentation host at `http://127.0.0.1:38470` to annotate.
Verify the running SDK with `curl http://127.0.0.1:38471/snapshot`; it reports
`com.pidal.sakamoto` and visible tags with Swift file/line locations. Snapshot
content can include private UI data; keep it local and do not publish it.

## Widgets, Shortcuts and Control Center

Connect once in the app to create and authorize its VPN profile. System actions
load only the sakamoto provider's saved profile, read current system state and
request start/stop without opening the app. Both the app and widget targets
carry the NetworkExtension entitlement; signed-device operation still needs
verification. Starting and Stopping are requests, not connection confirmation.
Manual disconnect pauses on-demand connection to prevent immediate restart. On iOS 16 the widget opens Home; interactive widget buttons require
iOS 17, and the Control Center toggle requires iOS 18. Actions refresh the saved
manager on the main actor and submit start/stop requests; starts briefly allow
system status to catch up before refreshing controls. The system/provider confirms
the eventual state. Repeated Connect while Starting
and Disconnect while Stopping are idempotent; Toggle while Starting can cancel
connection. The Control Center switch sets an explicit state: On connects, Off
disconnects, and repeated On never reverses the request. On during Stopping waits
up to two seconds for teardown before submitting one start; a newer Off in the
same intent host supersedes that wait. This does not wait for VPN establishment.
A cold Start briefly waits, at most one second, for NE to publish Connecting or
Connected before refreshing system controls. Expiry remains an unconfirmed
Starting request and never causes an automatic restart. If NE rejects Start with
configuration-stale, reload the saved profile and retry once; other errors and
accepted starts are not retried. A following Off in the same intent host can
cancel an accepted Start even before NE publishes Connecting. After an accepted
Start, a separate Control Center value read can also wait within that request's
two-second window if NE still reports Disconnected. It returns the observed
native On/Off state; an expired request never makes the control appear connected.
The request timestamp is separate from network-probe freshness. Off, a provider
terminal snapshot or an observed terminal state removes it. Rejected system
requests retain their latest sanitized error/domain/code in Config → Advanced →
Diagnostics even after a later successful start.
A newer request supersedes older preference preparation. Controls
refresh on errors, and all provider startup failures publish a failed snapshot
and request a system-surface refresh. Widget buttons do not become disabled by stale timeline
states. A temporary status-read failure uses a recent display snapshot for retry.

Opening Home reads system VPN status without provider IPC or reloading the VPN.
Subscriptions are installed once. An established command channel survives a
Starting/reasserting observation and closes on actual stop. Until the first system
read completes, Home displays a status check rather than a misleading Off switch.

The app and provider write a display-only App Group snapshot. Widgets show the
system VPN state, node label, mode and measured latency; no config, credentials or
logs are copied into widget storage. Latency and network-check results expire
after 120 seconds. Widget timelines are scheduled for refresh, with their actual
execution cadence controlled by iOS.

Provision the same App Group for the app, tunnel and WidgetKit extension. The
WidgetKit extension is registered in Simulator, but desktop rendering, Shortcuts
execution and VPN actions need runtime verification with a working WidgetKit host
and a signed NetworkExtension profile. Compile success alone does not validate
those operations.

## Automatic connection

Settings → Automatic connection installs system VPN On Demand rules for any
network, Wi-Fi, cellular, or selected destination domains. Connect and apply the
configuration once before enabling. The saved system profile persists across
restarts; startup depends on network availability and access to protected data.
This is not an iOS boot receiver or an MDM Always On VPN.

Domain conditions accept domains or HTTP/HTTPS URLs, normalize them to hosts
and match subdomains. iOS connects if DNS resolution fails or an optional check
URL does not return HTTP 200; it does not promise to start for every request to
a directly reachable host. The condition starts the VPN, while the generated
configuration controls proxy routing. Selecting Domains with an empty list imports
host-based PROXY rules from the selected `.conf` and its local includes. The
“Use PROXY domains from .conf” button merges those hosts into the editable list;
current generated local rule snapshots also supply downloaded rule-set domains.
Changed sources must be generated before their remote rules are available. IP
ranges and keyword predicates cannot be converted to iOS on-demand domains.
Review the list and save to install the system conditions. Trigger lists are
limited to 128 distinct hosts and 32 KiB of input; these are application budgets,
not Apple-documented limits. Conf imports and expanded binary rule snapshots
are limited to 2 MiB and return a clear error rather than truncating a larger
list. Complete proxy rules remain available after the VPN connects. Import runs
off the UI thread and cannot overwrite domains or a profile changed meanwhile.
The same screen can stage PROXY policy rules; apply them in Config before enabling on-demand.

Manual disconnect in Home, Shortcuts, the widget or Control Center disables
on-demand until the user enables it again in Settings. Unapplied profile edits
block automatic cold starts. Per-app VPN on iOS requires managed apps and device
management. Settings → Automatic connection → Connect when an app opens
provides a native Shortcuts link and setup instructions. The same entry is in
Widgets & Shortcuts. App launch refreshes the four preconfigured App Shortcuts;
no download is required.

In Shortcuts, create Automation → App → choose apps → Is Opened → Run
Immediately → New Blank Automation → sakamoto → Connect VPN. On older iOS,
disable Ask Before Running. Optionally create a separate Is Closed automation
with Disconnect VPN. Use explicit Connect/Disconnect rather than Toggle so
reopening an app cannot reverse the VPN state. Is Closed also fires when
switching away; it stops the device VPN and pauses on-demand, potentially
interrupting other apps or background transfers. These automations switch the
whole VPN and do not provide per-app traffic isolation. The user must choose
apps and save automations in Shortcuts; there is no public API to create those
personal triggers inside sakamoto.

## App icon

`App/Assets.xcassets/AppIcon.appiconset` contains opaque icons derived from owner-provided artwork for
all supported iPhone/iPad sizes. Xcode applies the native corner mask.
Regenerate both mobile platforms with `mise run icons` from the
repository root. Attribution and licensing are in `docs/portrait-license.md`
and the app's About page.

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

## Experiment

Settings → Experiment exposes the TUI traffic policy controls: unmatched
`off` / `on` / `auto`, direct failure threshold (1–20) and CF region auto-proxy.
It also provides automatic fallback, recovery-round threshold, editable ordered
selector priorities, manual recovery tests and review/removal of learned routes.
Settings belong to the selected profile and activate with Apply or Connect.
Complete `.sakamoto` exports carry host Experiment settings and fallback chains.

The PacketTunnel process owns monitoring, so UI backgrounding does not stop it.
It subscribes to real Libbox connection events, error logs, groups and Clash mode.
Automatic learning uses the shared Go `mobileexperiment` evidence tracker:
correlated unmatched TCP failures, spaced attempts and recent health of the
selected proxy are required. Explicit DIRECT/REJECT rules retain priority.
CF learning needs an explicit core regional-block signal; generic 403s and
challenge pages do not count. HTTPS response content is not intercepted, so the
feature cannot observe every regional-block page seen by a browser.

Fallback rounds use only fresh URL-test results, preserve manual selections and
require consecutive healthy rounds before recovery to a higher priority. Pending
profile edits pause automatic changes. Rule insertion is checked with Libbox;
a rejected reload restores the old runtime. Only committed learned domains are
restored on a cold start. Runtime domains/status remain private App Group files
per profile, outside source sync. Real-device VPN and background behavior require
hardware verification; simulator integration tests do not establish connectivity.

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

Settings → Tailscale → **Force DERP relay** saves a per-profile transport policy;
use Apply & reconnect after switching it. Old profiles default to normal direct
peer connectivity. The policy travels in `TunnelStartOptions`, outside sing-box
JSON, and survives regeneration. The provider closes the previous service before
changing the shared atomic policy, including when rolling back a failed reload.
The build patches only `debugAlwaysDERP` in a disposable copy of the pinned iOS
Tailscale dependency. Its existing DERP-only bind path substitutes blocking UDP
sockets; other debug knobs remain disabled and the Go module cache is untouched.
Direct-versus-relay peer probes on a signed device remain the runtime check.

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
  → pkg/mobilecore (bound alongside libbox in Libbox.xcframework)
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

## Config sources, generation and Apply

`pkg/mobileconf` is the platform-independent parser (stdlib only, no
sing-box imports). The app and extension link one combined dynamic
`Libbox.xcframework`; the app embeds it in `Frameworks`, and the extension
loads that copy through its `../../Frameworks` runpath. The framework builder
converts the upstream archive into a dylib while preserving the combined
public bindings. Its umbrella header exports both Libbox and Mobilecore APIs
through `import Libbox`. Each process initializes one Go runtime; binding the
packages separately can initialize two runtimes and crash at startup.
`internal/gen` delegates to it, so the iOS importer validates with the exact
host semantics. On device:

- **Import config**: `.conf` URL fetch (URLSession) or Files App document,
  parsed in-process via `MobilecoreParseConfContentJSON` — rule counts,
  pending include/RULE-SET references (masked) and unsupported-section
  notes ([URL Rewrite]/[MITM]/[Script]). A failed fetch/parse keeps the
  last-known-good import (`ConfigStore.commitImport` is the only writer).
- **Policy**: match/action validated by `MobilecoreNormalizePolicyRule`,
  staged on device and compiled during Generate.
- **Nodes & sources**: share links validated by `MobilecoreParseShareLink`
  (credential-free summary, leak-tested), stored nodes.txt-compatible;
  subscription metadata is staged and fetched/decoded during Generate.
  Raw links and URLs render masked until revealed and never enter action
  strings or logs.
- **Generate**: `pkg/mobilegen` reuses `internal/gen` to fetch subscriptions
  and remote rule lists, merge `.conf` includes, nodes and policy, and compile
  binary rule sets in-process. It is bound into the same Libbox archive;
  `mobilecore` remains a pure validation/vocabulary package. Candidates use
  immutable App Group rule snapshot directories and pass Libbox semantic
  validation before replacing the previous saved runtime. Failed generation
  keeps the old runtime. Node-only profiles can generate without a `.conf`.
- **Apply**: the main Config page has one Apply changes action that generates
  changed sources, validates and activates the candidate. Single profiles do
  not need a picker. Rule-source choice, generation without connecting, JSON
  edits and import diagnostics are available under Advanced. File imports
  detect `.sakamoto`, JSON, `.conf` and `nodes.txt` rather than offering a
  separate menu entry for every format.
- **Remote generation**: iOS supplies a URLSession fetcher to Mobilegen so
  importing and generation use Apple's system networking/proxy configuration.
  Each request is bounded, transient failures retry once, and permanent HTTP
  failures keep their stage, host and status in sanitized diagnostics. HTML
  login/challenge pages are rejected as source data. Missing rules never get
  silently omitted from a generated candidate.
- **Activation**: merges Keychain Tailscale authentication at start and activates
  the selected snapshot. It starts a stopped tunnel or reloads a running one;
  queued startup is not marked applied until the provider confirms Running.
  Source sync never auto-connects or publishes generated configs/credentials.
  iCloud discovery preserves runtime-relative names and local dependencies;
  multiple root confs require explicit selection in Config.

Reports cross the gomobile boundary as one JSON string (`[]string` struct
fields and returns are silently skipped by gomobile bind — a silent drop
would fake completeness, so the contract is explicit JSON pinned by Go
tests and mirrored by the Swift `Codable` side).

## iCloud sync: what is real, what is placeholder

`SakamotoKit/ICloudSyncStore.swift` is a faithful port of
`internal/icloud/sync.go` + `sources.go` semantics onto the app's staged
state (docs/icloud.md is the authority):

- **Scope (allowlist + denylist double guard).** Only what the app stages:
  `nodes.txt` (manual share links), `policy.json`, `subscriptions.json`
  (feed metadata — URLs can carry access tokens, which the enable dialog
  names), and `conf/<file>.conf` (the locally imported Shadowrocket conf,
  gated by the include-conf consent). NEVER synced — rejected by name at any
  depth (`ICloudSyncPaths.isValidSourceName`, the `ValidSourceName` port):
  generated config.json, `.srs`, `*.db`, sockets/locks, logs, key material
  (`.key/.pem`), `auth.json`/`secrets.zsh`, API-rotation state, the sync's
  own `icloud-state.json`, hidden/traversal components, the `logs/` top
  dir. Generated sing-box content and the Keychain auth key never enter the
  payload at all.
- **Conflict rules.** sha256 baseline in the app-support
  `icloud-state.json`; one-sided first use copies; identical sides refresh
  the baseline; both sides changed without a shared baseline → the pass
  STOPS with a `.conflict` status naming the files — nothing is ever
  overwritten; deletions are not propagated and a cloud-side deletion stops
  the pass instead of being silently restored; the whole graph is
  preflighted before any copy; a mid-pass failure keeps completed baselines.
- **Real iCloud, no fakery.** `FileManager.url(forUbiquityContainerIdentifier:)`
  resolves the container; cloud writes/reads go through `NSFileCoordinator`
  with atomic (temp+rename) writes, mode 0600 / dirs 0700; symlinks refused.
  No account/entitlement → status `unavailable`, local features untouched.
  No UserDefaults is pretending to be a cloud.
- **Unit-tested in memory.** 19 tests (`ICloudSyncTests`) cover the denylist,
  include parsing, baseline, conflict, deletion, mid-pass failure, symlink
  refusal, size caps and a two-device convergence scenario — filesystem and
  clock are injected protocols, so no test touches iCloud.

**Placeholder until a real Team signs the app:** the ubiquity container id
`iCloud.com.pidal.sakamoto` (three places, replaced together:
`App/Sakamoto.entitlements`, the `NSUbiquitousContainers` entry in
`project.yml`, `UbiquityICloudContainer.placeholderContainerID`). The
`com.apple.developer.icloud-*` entitlements are real declarations; Apple
grants the iCloud capability only to a provisioned Team — until then sync
reports `unavailable` instead of pretending. The PacketTunnel target
deliberately has NO iCloud entitlements: sync is an app-process feature.

## Directory map

| Path | What |
|---|---|
| `project.yml` | XcodeGen source of truth (targets, entitlements wiring, frameworks). The `.xcodeproj` is generated, git-ignored. |
| `App/Sources/` | App-target sources (compiled by Xcode only): entry, Home/Config/Data/Settings/About, Tailscale tool page, `ConfigStore` (saved config + state machine), LibboxCoreCommanding, TailscaleController, Keychain store, app-process `LibboxSetup` |
| `Extension/` | Tunnel-target sources: `SakamotoPacketTunnelProvider`, `ExtensionPlatformInterface`, `ExtensionSupport` (all carry upstream GPLv3 headers) |
| `Sources/SakamotoKit/` | SwiftPM vocabulary (`Vocabulary`, `TailscaleVocabulary`, `DataVocabulary`, `SettingsSemantics`, `TailscaleEndpointProvisioning`), IPC codec, `TunnelStartOptions`, `CoreCommanding` protocol, iCloud sync core (`ICloudSyncVocabulary/Filesystem/Store`) |
| `Sources/SakamotoNE/` | `NETunnelController` (NEVPNManager / provider messages) |
| `Tests/SakamotoKitTests/` | contract alignment + IPC codec + Data folding + Settings merges + Tailscale vocabulary/injection/provisioning tests |
| `contract/vocabulary.json` | golden generated from Go |
| `scripts/build-libbox.sh` | reproducible Libbox.xcframework build (see notes below) |
| `scripts/build-mobilecore.sh` | compatibility alias for the combined bind |
| `scripts/flatten-gomobile-framework.sh` | gomobile deep→shallow bundle fix (required for Xcode 26 embedding) |
| `Frameworks/` | built xcframeworks — **git-ignored, never committed** |

## Build from clean (verified sequence)

```sh
# From the repository root (Xcode must already be installed).
mise trust
mise install
mise run ios:build

# Fast verification when frameworks already exist:
mise run ios:verify

# Regenerate both platforms' icons:
mise run icons
```

## CI

`.github/workflows/ios.yml` runs on iOS-related pushes and pull requests, and
supports `workflow_dispatch`. It uses a macOS runner with Apple-provided
Xcode; mise owns Go, Python, pinned gomobile, and task-scoped XcodeGen.
CI caches Go modules and generated native frameworks (keyed by Xcode build
and project inputs), then runs the same `mise run ios:verify` task as local
development. There is no separate Homebrew XcodeGen installation.

The workflow verifies Go tests, Swift tests, framework bundle layout, and an
unsigned simulator build. It does not claim a signed device build: that still
requires a real Apple Team, provisioning, NetworkExtension capability, and
iCloud entitlements.

## Packaging through mise

After `mise run ios:build`, package the simulator app:

```bash
mise run ios:package
```

Output: `ios/build/Sakamoto-simulator.zip` plus SHA-256. CI uploads both as
`sakamoto-ios-simulator-<commit>`. Unzip and install the `.app` into a booted
iOS simulator with `xcrun simctl install booted Sakamoto.app`. This bundle
cannot be installed on an iPhone and cannot run a real VPN tunnel.

Device packaging uses a real signing Team and provisioned App IDs, app
group, NetworkExtension and app-only iCloud capabilities:

```bash
IOS_DEVELOPMENT_TEAM=YOUR_TEAM_ID mise run ios:archive
IOS_EXPORT_OPTIONS_PLIST=/absolute/path/ExportOptions.plist mise run ios:ipa
```

Outputs: `ios/build/Sakamoto.xcarchive` and `ios/build/ipa/*.ipa`.
Use an export-options plist matching your distribution method and profiles
(App Store Connect, development, or ad hoc). By default certificates and profiles
must already be installed. Set `IOS_ALLOW_PROVISIONING_UPDATES=1` to let Xcode
use your signed-in Apple account for automatic provisioning.

For an App Store Connect Team without registered devices, prepare the archive
without a development profile, then export with distribution signing:

```bash
mise run ios:store-archive
IOS_ALLOW_PROVISIONING_UPDATES=1 \
IOS_EXPORT_OPTIONS_PLIST=/absolute/path/ExportOptions.plist mise run ios:ipa
```

The store archive has an ad-hoc signature preserving each target's declared
entitlements; it is not installable or distributable until export succeeds.
Export options use `method: app-store-connect`, `teamID`, automatic signing and
`destination: export` (IPA) or `destination: upload` (App Store Connect upload).
Set `testFlightInternalTestingOnly: true` for an internal-only TestFlight build.
Always verify the exported app and extensions retain NetworkExtension, App Group
and app-only production iCloud entitlements. The registered Widgets bundle ID is
`com.pidal.sakamoto.WidgetExtension`; the previous `.Widgets` identifier was
unavailable during registration. The provisioned Team is `6Y2YB464VU`. Build output and the
optional `ios/ExportOptions.local.plist` are ignored by git. No signed IPA
is uploaded by CI until signing credentials are configured separately.

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

## Device verification steps

The paid Team `6Y2YB464VU` now owns the App, PacketTunnel and WidgetExtension
identifiers, the shared App Group and the app-only iCloud container. Distribution
export has verified these grants. The empty default development Team keeps
simulator builds independent of a local Apple account.

1. Set `IOS_DEVELOPMENT_TEAM=6Y2YB464VU` for a development archive, with a
   registered test device and matching development profiles; refresh provisioning
   with `IOS_ALLOW_PROVISIONING_UPDATES=1` if necessary.
2. Install the TestFlight build or the development build. App and tunnel require
   NetworkExtension and the shared App Group; iCloud Documents belongs only to
   the app, while Widgets uses the App Group.
3. Run on device; paste a generated sing-box config (or one produced by
   `sakamoto` on the host) in Config → Save → Regenerate + Reconnect.
4. Optional: store a Tailscale auth key (Tailscale tab) — or leave it empty
   and use the auth-URL login flow on first status.
5. Config generation on-device (internal/gen via mobilecore) remains the
   known follow-up; the Config screen states this boundary in-product.
