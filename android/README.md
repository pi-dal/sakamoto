# sakamoto Android integration

A real, buildable Gradle/Kotlin client over sing-box **v1.14.2**
`experimental/libbox` — with **built-in Tailscale** (the sing-box Tailscale
endpoint embeds the Tailscale client inside the tunnel process; this is not
a CLI probe and not an external-daemon count), and with ALL Home-page state
semantics delegated to the shared Go bridge (`pkg/mobilecore` → Mobilecore
AAR).

## Honest status

| Piece | Status |
|---|---|
| Gradle Kotlin project (app + manifest + res + wrapper) | complete, **not compiled on this machine** — no JDK / no Android SDK (see below) |
| `VpnService` + libbox `CommandServer` wiring (`bg/`) | ported from upstream SFA source, structure-correct, **pending SDK build verification** |
| Home page phase/mode/probe semantics | real — calls `Mobilecore.sessionPhase/nextRoutingMode/probeStateOf/nodeStatus/configStateTransition` (AAR); no state logic copied |
| Built-in Tailscale: 4 command RPCs + status model | real calls in `CommandClientRuntime` (SubscribeTailscaleStatus / SetTailscaleExitNode / TailscaleLogout / StartTailscalePing, v1.14.2 signatures), pure state model in `TailscaleModels`, **pending SDK build verification** |
| Tailscale auth key security | Android Keystore AES-256-GCM + app-private file (`security/TailscaleAuthKeyStore`), injected at start/reload only (`TailscaleConfigInjection`), masked display, never logged |
| Five TUI pages (Home/Config/Data/Settings/About) | bottom-nav five pages + Settings→Tailscale entry; About carries GPL-3.0, sing-box attribution, non-affiliation |
| Config / Data pages | skeletons + state model, boundaries stated in-product |
| JVM unit tests (pure models) | written: `TailscaleModelsTest` (11 cases) + `TailscaleConfigInjectionTest` (5 cases) — **not executed here** (no JDK; they run with `./gradlew test`) |
| `scripts/build-libbox.sh` | delegates to sing-box's own `cmd/internal/build_libbox -target android` (which builds `with_tailscale` by default in v1.14.2) |
| `scripts/build-mobilecore.sh` | `gomobile bind ./pkg/mobilecore` → `mobilecore.aar` |
| Compilation / test execution | **BLOCKED: no JDK, no Android SDK on this machine** — nothing is reported as built or passing that did not run |

## Sources this structure is based on (read for this integration)

- sing-box v1.14.2 `cmd/internal/build_libbox/main.go` — the official Android
  builder: `gomobile bind -target android -androidapi 24 -javapkg=io.nekohasekai
  -libname=box ./experimental/libbox`; `sharedTags` include `with_tailscale`
  and the `ts_omit_*` set; it emits **two** variants: `libbox.aar` (API 24,
  full tags) and `libbox-legacy.aar` (API 21, without `with_naive_outbound`);
  it requires **openjdk 17 exactly** and an Android SDK.
  <https://github.com/SagerNet/sing-box/blob/v1.14.2/cmd/internal/build_libbox/main.go>
- sing-box v1.14.2 `experimental/libbox` — the API surface this client codes
  against: `NewCommandServer(handler, platformInterface)` +
  `Start()/StartOrReloadService(content, OverrideOptions)/CloseService()/Close()`;
  `NewCommandClient(handler, options)` with `AddCommand(Libbox.CommandStatus /
  CommandGroup)` and `StatusInterval` (a `time.Duration`, i.e. nanoseconds);
  handler callbacks `Connected/Disconnected/WriteStatus(StatusMessage)/
  WriteGroups(OutboundGroupIterator)/...`; `OutboundGroup.Selectable/Selected`
  + `OutboundGroupItem.URLTestDelay` (the selected-dot vs reachability split);
  `TunOptions` getters incl. `GetDNSMode() *StringBox` and the API-33 route
  split; `PlatformInterface` method set incl. `TailscaleHostname()`.
  <https://github.com/SagerNet/sing-box/tree/v1.14.2/experimental/libbox>
- SagerNet/sing-box-for-android (SFA) — the reference app: package
  `io.nekohasekai.sfa`, `BoxService : CommandServerHandler`, `VPNService :
  VpnService, PlatformInterfaceWrapper` with `openTun(TunOptions)` →
  `Builder().establish().fd` and `autoDetectInterfaceControl(fd) = protect(fd)`;
  foreground service `foregroundServiceType="systemExempted"`; build values
  minSdk 21 / targetSdk 35 / compileSdk 35.
  <https://github.com/SagerNet/sing-box-for-android>
- sing-box build-from-source docs — `with_tailscale` build tag semantics.
  <https://sing-box.sagernet.org/installation/build-from-source/>

## API-level decision (differs from SFA on purpose, documented)

SFA declares `minSdk 21`; its 21–23 floor is served by shipping
`libbox-legacy.aar` (built with `-androidapi 21`). This project wires only
the **main** libbox variant (`-androidapi 24`), so the declared floor is
**minSdk 24 / targetSdk 35 / compileSdk 35**. `scripts/build-libbox.sh`
still produces `libbox-legacy.aar`; wiring it as a product flavor for API
21–23 devices is a documented follow-up, not a silent gap.

## Project layout

| Path | What |
|---|---|
| `settings.gradle.kts` / `build.gradle.kts` / `gradle.properties` | Gradle 8.9 + AGP 8.7.3 + Kotlin 2.0.21 (stable pairing, JDK 17 toolchain) |
| `gradle/wrapper/*`, `gradlew`, `gradlew.bat` | wrapper pinned to gradle-8.9-bin.zip; `gradle-wrapper.jar` is the official artifact from the gradle v8.9.0 tag |
| `app/build.gradle.kts` | `fileTree("libs")` picks up `libbox.aar` + `mobilecore.aar` (never committed); viewBinding on |
| `app/src/main/AndroidManifest.xml` | INTERNET, FOREGROUND_SERVICE(+SYSTEM_EXEMPTED), POST_NOTIFICATIONS; `SakamotoVpnService` with `BIND_VPN_SERVICE` + `android.net.VpnService` intent filter, `systemExempted` FGS type |
| `SakamotoApplication` | notification channels + context accessors |
| `runtime/MobilecoreRuntime.kt` | single owner of Home state; every word comes from the Mobilecore AAR |
| `runtime/TailscaleModels.kt` | PURE Tailscale state model (backend state incl. verbatim `Unrecognized`, exit-node candidates, capabilities, key masking, ping rendering) — libbox-free so JVM tests cover it |
| `runtime/TailscaleRuntime.kt` | Tailscale page UI state holder (status flow) |
| `runtime/TailscaleBinding.kt` | the ONLY libbox-touching Tailscale file: maps `TailscaleEndpointStatus/TailscalePeer/TailscalePingResult` → pure summaries |
| `security/TailscaleAuthKeyStore.kt` | auth key at rest: AndroidKeyStore AES-256-GCM, ciphertext+IV in filesDir (MODE_PRIVATE), `allowBackup=false`; no log/toString ever carries the key |
| `security/TailscaleConfigInjection.kt` | pure port of the iOS injection: merges the stored key into `endpoints[type=tailscale]` at start/reload; stored key wins; no-endpoint → loud error |
| `runtime/ConfigRepository.kt` | staged config/policy/nodes/subscriptions JSON in filesDir (the nodes/links are secrets: app-private storage, never logged) |
| `bg/SakamotoVpnService.kt` | `VpnService` + full `PlatformInterface` (openTun port, protect(), getInterfaces, tailscaleHostname = device name; root/USB/shell/bridge legs fail loudly as "not supported") |
| `bg/TunnelBoxService.kt` | `CommandServerHandler` + `CommandServer` lifecycle, foreground notifications, `startOrReloadService` with the staged generated config |
| `command/CommandClientRuntime.kt` | app-side `Libbox.newCommandClient` (status + group streams); mode cycle = `Mobilecore.nextRoutingMode` → `setClashMode`; **Tailscale RPCs**: `subscribeTailscaleStatus` / `setTailscaleExitNode` / `tailscaleLogout` / `startTailscalePing` |
| `ui/*` | Home (phase/mode/selected≠reachable/URL test/ConfigState), Config, Data, Settings (→ Built-in Tailscale entry), **About** (GPL-3.0, sing-box attribution, non-affiliation), **Tailscale** (status stream, auth-URL login, Keystore-backed key, exit-node pick/clear, ping, unsupported list verbatim) |
| `app/src/test/java/...` | JVM unit tests for the pure layer: `TailscaleModelsTest`, `TailscaleConfigInjectionTest` (JUnit 4; `org.json:json` test artifact shadows the android.jar stubs) |
| `scripts/build-libbox.sh` | upstream builder, both AAR variants → `app/libs/` |
| `scripts/build-mobilecore.sh` | `gomobile bind -javapkg=com.pidal.sakamoto -libname=mobilecore ./pkg/mobilecore` → `app/libs/mobilecore.aar` |

## State semantics: one source of truth (same as iOS)

```
internal/core (Go constants + PhaseOf/NextRoutingMode/ClassifyProbe)
  → pkg/mobilecore (gomobile bind → mobilecore.aar)
  → com.pidal.sakamoto.mobilecore.Mobilecore  (this repo's Android UI renders only)
```

The phase folding (`Disconnected / Starting / TUNRunning / Reachable /
Unverified / Conflict / Unavailable / Stopping`), the `Rule → Global →
Direct` cycle, probe classification and the config state machine are NOT
reimplemented in Kotlin. `MobilecoreRuntime` is a thin scheduler around the
bridge; `OutboundGroupItem.URLTestDelay` feeds `Mobilecore.nodeStatus`, so
the Home page shows **selected ≠ reachable** in the TUI's own vocabulary.

## Build from clean (once the toolchain exists)

```sh
# 1. Toolchain (see "Missing on this machine" below for exact installs)
#    JDK 17 (upstream checkJavaVersion requires openjdk 17 exactly):
mise use -g java temurin-17
#    Android SDK + NDK:
brew install --cask android-commandlinetools
export ANDROID_HOME="$HOME/Library/Android/sdk"
sdkmanager "platforms;android-35" "build-tools;35.0.0" "ndk;27.2.12479018"

# 2. gomobile (GOPATH/bin; mise users: the explicit GOBIN matters)
GOBIN="$(go env GOPATH)/bin" go install github.com/sagernet/gomobile/cmd/gomobile@latest
GOBIN="$(go env GOPATH)/bin" go install github.com/sagernet/gomobile/cmd/gobind@latest
export PATH="$PATH:$(go env GOPATH)/bin"

# 3. AARs (libbox ~10 min; tailscale+gvisor dominate; mobilecore is fast)
android/scripts/build-libbox.sh        # → app/libs/libbox.aar (+ libbox-legacy.aar)
android/scripts/build-mobilecore.sh    # → app/libs/mobilecore.aar

# 4. Build + test
cd android
./gradlew :app:assembleDebug           # debug APK
./gradlew :app:assembleRelease         # needs signing config (below)
./gradlew test                         # unit tests (state models; bridge calls need the AARs)
```

## Missing on this machine (honest blockers, verified)

Checked 2026-10-04: `ANDROID_HOME` unset, `~/Library/Android/sdk` absent,
`/usr/bin/java` is the macOS stub ("Unable to locate a Java Runtime"), no
standalone `gradle`. Consequence: **the Kotlin sources have not been compiled
here** — they are written against the exact v1.14.2 bindings and upstream
sources cited above, with every place that depends on the generated AARs
marked `STATUS PENDING SDK BUILD VERIFICATION`. Nothing in this tree claims a
build that did not happen. No Android-SDK-sized installs were performed
without owner approval.

## Signing (placeholders, like iOS)

`applicationId com.pidal.sakamoto` is a development placeholder. Release
builds need a real keystore (`keytool -genkeypair -v -keystore
sakamoto-release.keystore ...`), configured in `app/build.gradle.kts`
`signingConfigs`. Debug builds use the auto-generated debug keystore. The
VPN permission (`VpnService.prepare`) is a user grant, not a signing
requirement; no special Google entitlement is needed for VpnService.

## Built-in Tailscale: real capabilities, same split as iOS

The AAR is built with `with_tailscale` (part of the v1.14.2 builder's
default tag set), so the endpoint `{"type": "tailscale", ...}` is available
in configs exactly like on iOS/macOS. `tailscaleHostname()` reports the
device name, mirroring `ExtensionPlatformInterface` on iOS.

**Wired in code** (each maps to a v1.14.2 `CommandClient` method, same set
as the iOS app):

| Capability | libbox v1.14.2 API | Android surface |
|---|---|---|
| Live tailnet status (state, tailnet name, self, peers, exit node, auth URL) | `SubscribeTailscaleStatus(TailscaleStatusHandler) → TailscaleStatusSubscription` | `CommandClientRuntime.subscribeTailscaleStatus()` → `TailscaleRuntime` |
| Pick / clear exit node | `SetTailscaleExitNode(endpointTag, stableID)` (empty ID clears) | exit-candidate buttons / `clearExitNode` |
| Log out of the tailnet | `TailscaleLogout(endpointTag)` | confirm-dialog logout |
| Per-peer latency (direct vs DERP) | `StartTailscalePing(endpointTag, peerIP, TailscalePingHandler)` | "Ping exit node" (target = first Tailscale IP, else DNS name) |
| Login | status-driven `BackendState == NeedsLogin` + `authURL` → system browser | "Open login URL" (visible only during the login flow) |
| Auth key | `TailscaleAuthKeyStore` (Keystore AES-GCM) → `TailscaleConfigInjection` at start/reload | Settings→Tailscale page; masked display only |

**Explicitly unsupported** (rendered verbatim on the Tailscale page; build
and API facts — the `ts_omit_*` tags are in the v1.14.2 builder's
`sharedTags` for ALL platforms, Android included):

- Taildrop (`ts_omit_taildrop`)
- Tailscale SSH (`ts_omit_ssh`)
- serve/funnel (no libbox API)
- login-by-auth-key as a client RPC (it is config input, not an action)
- any external-CLI/system-daemon probing (Android apps cannot query a
  system tailscaled; faking it would be fake support)

`TailscaleBackendState` maps the wire strings `Stopped / Starting /
NeedsLogin / NeedsMachineAuth / Running` and carries unknown values verbatim
(`Unrecognized`) instead of guessing — pinned by `TailscaleModelsTest`.
