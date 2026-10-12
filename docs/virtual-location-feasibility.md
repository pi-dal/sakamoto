# Virtual location feasibility for sakamoto

Research date: 2026-10-12. Scope: change the location delivered to other apps on a normal phone, rather than move a map marker inside sakamoto. Updated after the owner confirmed iOS 26 and authorized implementation: both mobile apps now include opt-in fixed-coordinate controls. Native device behavior still needs verification; the implementation does not establish a universal GPS override.

## Result

| Product path | Feasibility | Required environment | Delivery recommendation |
| --- | --- | --- | --- |
| Android app supplies system mock location | Supported by public APIs | User selects sakamoto in Developer options → Select mock location app | A practical optional Android feature |
| iOS TestFlight/App Store app directly supplies system mock location | No supported public API identified | Ordinary app sandbox cannot publish other apps' Core Location coordinates | Distinguish direct injection from the network-response technique below |
| On-device iOS Apple WLOC response interception | Conditional implementation evidence exists | Trusted device-specific CA, relevant traffic through a proxy, compatible OS and location behavior | Evaluate a scoped MITM prototype inside the existing tunnel; not a guaranteed system-wide override |
| iOS simulation controlled by a computer | Available through developer tooling | Compatible device/OS, pairing/trust, Developer Mode and appropriate developer services | Separate CLI/TUI companion experiment |
| Location fixture inside our own app | Possible on both platforms | An app-owned test abstraction | Useful for development; does not change other apps |
| VPN exit IP changes geographic appearance | Available through existing VPN routing | An appropriate exit/proxy server | Changes IP-based geolocation, not GPS/Core Location |

## Android

Android's developer settings explicitly allow choosing an installed mock-location app [1]. `LocationManager.addTestProvider` can replace a provider of the same name, and `setTestProviderLocation` injects a location [2]. These calls require the mock-location AppOp to be allowed. Installing an APK or granting VPN consent does not grant this authorization; the user must select the app.

This can affect other apps using the overridden providers. It does not make every client accept the result: Android marks injected fixes as mock (`Location.isMock`, or the older equivalent), and clients can reject them or compare independent signals. There is no claim of undetectable substitution.

Google Play services provides a separate fused-location route. `FusedLocationProviderClient.setMockMode(true)` and `setMockLocation` affect all FLP clients, including other processes and derived APIs such as geofencing [3]. On Android M+ the documentation requires declaring `ACCESS_MOCK_LOCATION` and being selected in developer settings. Exiting mock mode clears mock fixes from the FLP cache. We should evaluate this adapter on Google-certified devices, while keeping an AOSP `LocationManager` path for devices without GMS. The Android app now includes the optional Play services location adapter, with an AOSP `LocationManager` path when GMS is unavailable.

A first implementation should offer:

- Explicit fixed WGS 84 latitude/longitude, saved places, start, stop and restoration status. Map picking and GPX routes can follow after a fixed-point prototype works.
- A clear setup state when the mock-location AppOp is absent, with a link to developer settings. Catch permission revocation while running.
- Fresh wall-clock and monotonic timestamps, provider, accuracy and the other required fix fields; periodically refresh a stationary fix at a conservative cadence.
- A dedicated lifecycle for location simulation. VPN and simulation should be independently controllable, with a visible ongoing notification and a stop action for any persistent mode.
- Removal of installed test providers and `setMockMode(false)` on an orderly stop. Process death, force-stop, permission revocation and device reboot require device tests; restoration must not be assumed just because cleanup exists.
- A documented coordinate system. Chinese map services can use GCJ-02/BD-09; convert a map selection to WGS 84 before injecting instead of silently accepting mismatched coordinates.

Our app currently targets API 35 and has a `systemExempted` foreground service for its VPN. Do not assume this VPN service type also authorizes persistent location simulation. Android 14+ foreground-service types and their prerequisites must be checked for the chosen design [4]. The documented `location` type requires location services to be enabled, location runtime permission and `FOREGROUND_SERVICE_LOCATION`. A prototype should establish which type applies to injection-only use before adding permissions or background behavior.

Device validation should cover Android 12–15/current supported releases, at least a Google-certified and a non-GMS device, `LocationManager` and FLP consumers, permission revocation, stop/restart, screen lock, process death and concurrent VPN use. The first prototype should be user-started and should not silently resume after boot.

## iOS

### On-device WLOC interception: correction after reviewing location-spoofer

The initial assessment overlooked a network-response technique and overstated the computer requirement. Absence of a public location-injection API does not rule out influencing network-derived location through a proxy.

The owner supplied [xweiba/location-spoofer](https://github.com/xweiba/location-spoofer). Inspected source revision: `4aa4c6aa6cf0ae0d134c317c4c36eb90f21b21b7` (2026-09-24), reviewed on 2026-10-12 [10–12]. Its on-device Go HTTP proxy uses a CA trusted by the user to decrypt selected location-service connections. For a matching `POST /clls/wloc` response, it rewrites Wi-Fi/cellular location records in Apple's WLOC payload, including latitude, longitude and accuracy. The parser handles several envelopes and gzip-compressed input. This influences an input to location estimation; it does not replace the GPS receiver or install a Core Location mock provider.

Its two modes have different operating requirements:

- **App Mode:** an HTTP proxy inside the phone app, manual proxy configuration for the current Wi-Fi, and installation/full trust of its CA. Ongoing location operation does not require a paired computer or jailbreak. Installation/signing may separately require a computer.
- **Third-party Proxy Mode:** another client owns the VPN/proxy, MITM, certificate and persistent coordinate settings. Shadowrocket is the project's stated tested client; the supplied configurations for several other clients are not all verified. Wi-Fi/cellular coverage depends on the proxy client.

The README explicitly says that **iOS 27 beta 6 and subsequent betas block MITM interception of `gs-loc.apple.com`, making this approach unusable until a workaround exists** [10]. This is a maintainer-reported compatibility limit, not a result reproduced here. No current-phone success or workaround has been established. Older OS versions are candidates for testing rather than an unconditional support promise. Issue #10 reports iOS 26.1 briefly working and then stopping, with successful patch logs; it is evidence that response rewriting alone does not establish sustained location replacement [13]. GPS availability, location-source selection and caches can affect the observed result.

### Candidate integration into sakamoto

Our iOS `ExtensionPlatformInterface` already configures `NEProxySettings`, including HTTP/HTTPS servers and match domains. This is a candidate way to direct relevant system HTTP proxy traffic to a local helper while the existing VPN runs. Whether `locationd` follows those settings on each OS and on Wi-Fi/cellular still needs device validation. `NEProxySettings` does not itself decrypt TLS. Also, forwarding raw TLS on port 443 into a CONNECT-based HTTP proxy is insufficient: a proper explicit-proxy or transparent-TLS adapter is required.

Current sakamoto import/generation explicitly omits `[MITM]`, `[URL Rewrite]` and scripts; it has no compatible script engine. Importing the reference Shadowrocket module will therefore not activate this feature. A real implementation needs a native WLOC processing component, certificate/trust setup and runtime configuration/control.

A concrete first experiment should:

1. Generate a unique device CA with its private key stored in Keychain, support user-driven certificate installation/full trust, and test trust independently from Apple-host TLS compatibility. A trusted local certificate does not prove the system accepts interception of Apple's service.
2. Start an opt-in HTTP proxy in the existing PacketTunnel lifecycle, initially scoped to the Apple WLOC hosts needed by the experiment. Preserve unrelated routes, the existing HTTP-proxy behavior and upstream core dialing, with explicit loop prevention.
3. Use the existing combined Go framework/runtime for a helper rather than embedding the reference project's separate C-archive Go runtime. Its App Mode uses silent-audio keepalive; sakamoto should use its existing tunnel process instead.
4. Support WGS 84 fixed coordinates and explicit clear/disable. Separate enabled intent, CA trust, last relevant request, accepted TLS connection, successfully patched response and a measured Core Location observation. An empty recent-request history means unverified, not active replacement.
5. Bound concurrent work, compressed and decompressed body size, total time and diagnostic retention. The reference caps the incoming response at 1 MiB but its gzip expansion uses an unbounded `io.ReadAll`; this needs a separate decompression budget before co-locating it with the VPN core. Unknown formats should pass through unchanged.
6. Verify Wi-Fi, cellular, lock screen, foreground/background, restart, cache refresh and restoration on compatible physical devices. Blocking OS behavior must be surfaced as unsupported, without retrying indefinitely or disrupting the VPN.

The reference root code and its bundled WLOC scripts are AGPL-3.0; sakamoto's root license is GPL-3.0. Direct reuse requires preserving attribution and addressing the resulting AGPL requirements. No code from the project has been copied into sakamoto in this research step.

Validation performed here: all **21 reference Go core tests passed with `-race`** on the inspected revision, covering payload rewriting, pass-through behavior, coordinate snapshots and certificate services. These tests use local/synthetic inputs and do not demonstrate a real iPhone accepting or applying the rewritten location. Upstream distributes unsigned IPA files; that is not evidence of TestFlight/App Store distribution approval.

### Public APIs and computer-assisted simulation

The public Core Location API delivers location to an app. The reviewed API surface offers no counterpart to Android's test-provider injection for an ordinary distribution app. Constructing a `CLLocation` or `CLLocationSourceInformation` only constructs data; it does not publish a device-wide fix. A PacketTunnel entitlement controls network packets, not Core Location. TestFlight or a different signing method does not create a location-injection entitlement.

Apple documents GPX simulation through the Xcode debugger, and `CLLocationSourceInformation.isSimulatedBySoftware` exposes whether a fix is simulated [5]. Simulation can therefore be observable to clients. Apple also distinguishes test-plan simulation, which affects code in the test bundle, from UI automation that sets `XCUIDevice.shared.location` [6]. Neither mechanism is a production-app API for publishing locations to unrelated apps. Xcode 27 Device Hub adds device/simulator inspection and location simulation controls [7]; the relevant capability still belongs to developer tooling.

A computer-assisted path is technically credible. The inspected `pymobiledevice3` implementation exposes iOS 17+ DVT `simulate-location set`, `clear` and GPX `play`, calling `com.apple.instruments.server.services.LocationSimulation` with `simulateLocationWithLatitude:longitude:` and `stopLocationSimulation` [8]. This provides concrete implementation evidence, not a supported App Store API. Device trust, Developer Mode, developer-image/service availability, tunnel setup, OS-version compatibility and persistence/reset behavior remain prerequisites to validate on real phones [9]. Wireless developer transport is not evidence that the same operation works inside an unprivileged iOS app.

A separate iOS experiment can live in the macOS CLI/TUI: list a paired device, check prerequisites, set a fixed location, replay a GPX file and explicitly clear it. This remains useful when network-location interception is unavailable or unsuitable. Keep that tool's device transport separate from the VPN core. It must report unsupported versions and loss of device connection clearly. Do not ship either phone or computer controls as a working system-wide replacement before that path has real-device evidence. Our repository does not currently include this computer companion. The on-device WLOC component is implemented below.

Jailbreak tweaks and private on-device services would create a different product with substantially different compatibility and distribution constraints. They are not a stable continuation of the current TestFlight app. External accessory source metadata also does not establish an arbitrary location-injection capability for our app.

## Implemented fixed-coordinate feature

Both apps expose **Settings → Virtual location** with decimal WGS84 latitude/longitude, accuracy, favorites, explicit enable/stop, a reported-location check and a shareable diagnostic report. Map selection, coordinate-system conversion and GPX playback are outside this first fixed-point feature.

On **iOS through version 26**, `pkg/mobilelocation` implements independent, scoped HTTPS response processing inside the existing PacketTunnel and combined Go runtime. The reference is used for mechanism and protocol facts; its AGPL implementation is not embedded. Device CA keys live in the existing App Group's Keychain with `AfterFirstUnlockThisDeviceOnly` accessibility [14]. A public-only configuration profile can be downloaded in Safari or exported. The app checks CA trust using a local HTTPS connection with default iOS certificate validation; this verifies trust independently of Apple-service interception.

The helper recognizes only the configured location-service hosts and `POST /clls/wloc`. Location upstream streams enter the existing core router; unrelated explicit-proxy traffic uses the original local core proxy. Enable/disable updates NE proxy settings, with rollback if the update fails. Coordinate updates use provider IPC and do not reload the core or reset Tailscale. The helper has 1 MiB compressed/decompressed processing budgets, four simultaneous intercepted TLS connections, a bounded certificate cache, 12-second request and 15-second TLS-stream deadlines, limited request/header input, and explicit closure of hijacked sockets at VPN shutdown. Unknown complete responses pass through; incomplete upstream responses return a gateway failure. New coordinate rewrites are never published after disable or a coordinate update. This remains network-derived positioning: GPS, caches, pinning and `locationd` proxy behavior can take precedence. The feature is disabled on iOS 27+ according to the reviewed compatibility report.

On **Android**, a dedicated non-sticky `location` foreground service installs GPS/network test providers and optionally enables Google fused mock mode. The user must select Sakamoto as the mock-location app, enable system Location Services and grant location runtime permission. Fixes receive fresh wall-clock and monotonic timestamps at a one-second cadence. The VPN remains independent. Stop removes owned providers and confirms fused mock-mode exit; a failed/timed-out cleanup is retained as a recovery requirement, including across process restarts. The service never resumes automatically after boot. Battery saving can delay background updates and consumers can reject Android's mock flag.

Diagnostics distinguish requested state, actual service/proxy state, TLS connections, parsed requests, rewritten responses, injection confirmation and a separately measured location. Reports contain coordinates, versions and timestamps, but no CA private keys, VPN configuration or authentication keys. Local HTTPS integration/race tests verify rewrite, disable, original-proxy forwarding, cancellation budgets and certificate handling. These are synthetic/loopback tests; Wi-Fi/cellular, physical GPS precedence, lock screen, force-stop and permission revocation still require device checks.

## Recommended device validation

Install the new app, finish each platform's setup and test a fixed coordinate with the VPN in use. On iOS, use request/rewrite counters and then independently check the reported location; on Android, verify GPS and fused consumers and Stop simulation. Keep computer-assisted DVT simulation as a separate alternative when network-response processing is unsuitable.


## Sources

1. Android Developers, [Configure on-device developer options](https://developer.android.com/studio/debug/dev-options), “Select mock location app”.
2. Android Developers, [LocationManager](https://developer.android.com/reference/android/location/LocationManager), `addTestProvider`, `setTestProviderLocation`, `removeTestProvider`; mock AppOp requirements and mock-fix marking.
3. Google Play services, [FusedLocationProviderClient](https://developers.google.com/android/reference/com/google/android/gms/location/FusedLocationProviderClient), `setMockMode` and `setMockLocation`; scope across processes and restoration.
4. Android Developers, [Foreground service types](https://developer.android.com/develop/background-work/services/fgs/service-types), location prerequisites and system-exempted scope.
5. Apple, [isSimulatedBySoftware](https://developer.apple.com/documentation/corelocation/cllocationsourceinformation/issimulatedbysoftware), GPX/Xcode simulation and detectable source metadata.
6. Apple, [Simulating location in tests](https://developer.apple.com/documentation/xcode/simulating-location-in-tests), test-bundle scope and UI automation.
7. Apple WWDC26, [Get the most out of Device Hub](https://developer.apple.com/videos/play/wwdc2026/260/), developer device controls and simulation.
8. pymobiledevice3, [DVT location commands](https://github.com/doronz88/pymobiledevice3/blob/master/pymobiledevice3/cli/developer/dvt/simulate_location.py) and [location service implementation](https://github.com/doronz88/pymobiledevice3/blob/master/pymobiledevice3/services/dvt/instruments/location_simulation.py), source inspected on the research date.
9. pymobiledevice3, [iOS 17+ tunnels and support matrix](https://doronz88.github.io/pymobiledevice3/guides/ios17-tunnels/). Third-party implementation evidence; not an Apple compatibility guarantee.
10. location-spoofer, [README at reviewed revision](https://github.com/xweiba/location-spoofer/blob/4aa4c6aa6cf0ae0d134c317c4c36eb90f21b21b7/README.md), App Mode/third-party modes, CA requirements, and the iOS 27 beta 6 MITM block.
11. location-spoofer, [proxy implementation](https://github.com/xweiba/location-spoofer/blob/4aa4c6aa6cf0ae0d134c317c4c36eb90f21b21b7/Core/proxy.go) and [WLOC patcher](https://github.com/xweiba/location-spoofer/blob/4aa4c6aa6cf0ae0d134c317c4c36eb90f21b21b7/Core/wloc_patch.go), scoped TLS interception, matching response conditions, binary field rewriting and pass-through behavior.
12. location-spoofer, [background lifecycle](https://github.com/xweiba/location-spoofer/blob/4aa4c6aa6cf0ae0d134c317c4c36eb90f21b21b7/Shared/BackgroundKeepAlive.swift), [C bridge](https://github.com/xweiba/location-spoofer/blob/4aa4c6aa6cf0ae0d134c317c4c36eb90f21b21b7/Core/bridge.go), and [license](https://github.com/xweiba/location-spoofer/blob/4aa4c6aa6cf0ae0d134c317c4c36eb90f21b21b7/LICENSE), implementation and direct-reuse constraints.
13. location-spoofer, [issue #10](https://github.com/xweiba/location-spoofer/issues/10), user-reported iOS 26.1 brief operation and patch logs; anecdotal evidence, not a compatibility guarantee.
14. Apple, [Sharing access to keychain items among a collection of apps](https://developer.apple.com/documentation/security/sharing-access-to-keychain-items-among-a-collection-of-apps), existing app-group names can serve as Keychain access groups without a separate Keychain Sharing entitlement or Team ID prefix.
