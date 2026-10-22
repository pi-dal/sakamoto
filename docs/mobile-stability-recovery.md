# Mobile stability and repair contract

This document extends `stability-recovery.md` to iOS and Android. Working-source implementation and published app capabilities are separate facts.

## Failure scenarios (recorded before implementation)

The integration lane must exercise these failures before accepting recovery behavior:

1. A process dies after persisting Starting or Running; the next process must consume the same durable consecutive-failure budget. Reopening the app must not erase it.
2. Three retry attempts exhaust the automatic budget. Backoff is 2, 4, 8 seconds; failed starts count. Only an explicit user start/repair or five minutes of the same verified core clears the episode.
3. Stop arrives during native startup or configuration reload. It invalidates work immediately; late native results cannot publish Running, reopen TUN, reset a stop hold, or restart the service.
4. A stale stable-timer, repair response or operation ticket belongs to a predecessor. It cannot alter the current journal or promote its configuration.
5. A healthy core is checked/repaired. Its running instance and existing traffic remain intact. An unreachable website alone does not restart it.
6. A core is alive but its control path is unavailable. Default repair reports degraded; reconnection requires the explicit Reconnect action and confirmed teardown before starting another instance.
7. Journal corruption or inability to persist recovery state prevents automatic startup. Error messages must not contain configuration, credentials or source URLs.
8. Local files referenced by a retained profile are removed or changed. A stable configuration snapshot retains its own assets, validates all hashes, and is revalidated by the installed native core before reuse.
9. The app was updated. Installed app, shared recovery protocol, native core version and running instance are reported separately; mobile configuration recovery never claims to restore an old engine binary.
10. A repair is queued, not completed. Starting is not repaired; a confirmed core instance is distinct from a DNS/HTTPS reachability check.
11. Explicit disconnect cancels recovery, including app-side pending work. iOS disconnect disables on-demand. Android retains START_NOT_STICKY and respects system VPN revocation; Always-on/lockdown remain system policy.
12. Neither platform introduces high-frequency background probing. Stable promotion uses a single delayed check, canceled on stop/replacement.

## Common semantics

The shared Go recovery policy owns the durable episode, backoff, stable interval, generation fencing, private configuration snapshots and repair decisions. Swift and Kotlin perform platform VPN operations and render its results. The status/result vocabulary matches desktop repair: healthy, repaired, degraded, stopped, starting, blocked, failed; action and verified are independent fields.

A native running instance confirms core service liveness. DNS/TLS/HTTPS checks remain separate observations and are never inferred from VPN status. Retained mobile profiles use the currently installed signed core, undergo native preflight again, and pause learning while recovering an immutable configuration. Android native fatal signals can kill the app process; recovery resumes when Android or the user next launches it. iOS extension relaunch is owned by NetworkExtension. This is bounded admission and repair, not a promise that an app can schedule itself after every OS termination.

## Independent escape

An app-side Reconnect uses system VPN stop before starting again and waits for confirmed teardown. iOS Settings > VPN and Android Settings > Network > VPN remain available independently of the in-app native bridge. On iOS, disabling the profile's on-demand connection prevents immediate reactivation. Android Always-on and Block connections without VPN must be changed in Android settings when ordinary networking is desired.

Private journals and retained profiles stay in the app container/app group, are excluded from cloud/source sync and Android backup, and are never included in public diagnostics. iOS marks the recovery directory excluded from system backup.

## Implemented entry points

Home exposes **Check and repair VPN** on both platforms. Connection details exposes **Check core only** and the native core identity/recovery protocol. A healthy native instance is retained. A degraded result offers a confirmed **Reconnect VPN** action, which waits up to ten seconds for teardown before starting another service. A queued start remains unverified; repair waits for a fresh running native instance and reports incomplete verification separately. DNS/TLS/HTTPS remains **Check connection**.

Starting/Stopping states do not permit another repair. Disconnect remains usable while native startup or repair is pending. Pending app repair requests and platform startup callbacks carry generations; manual stop supersedes them before native lifecycle waits. Android closes the published TUN fd immediately, rejects late fd publication, and keeps native create/reload/close on one lifecycle worker. Native reload ACK is returned only after completion; preflight failure retains the old native instance, and failed replacement restores the previous captured profile with learning paused. iOS provider IPC has a three-second ping deadline and guards against late/double replies; app-side system disconnect does not require a provider response.

The Go journal lives at `recovery/mobile/mobile-runtime.json`, and immutable configuration/assets at `recovery/mobile-profiles/`. Admission is saved before entering the native core. An unclean Starting/Running marker consumes the persisted episode on the next automatic launch. Platform relaunch scheduling is owned by iOS/Android; the apps do not promise to launch themselves after every fatal signal. Explicit repair may archive a corrupt private regular journal and reset it; automatic launches never do so. Unsafe/unreadable files remain an error.

Snapshots stream local rule/TLS inputs into private retained assets, rewrite their paths, and start the native core from those captured bytes. A prepared replacement cannot delete the active core's assets or receive a stable receipt before native application. Stable promotion requires five minutes of the same confirmed instance, unchanged captured inputs, and verified snapshot hashes. Retention preserves current dependencies and a predecessor. Learning pauses during fallback so newly staged rules cannot overwrite the recovered runtime. Staged UI configuration is not reported as applied when a different retained profile is running.

Recovery protocol **1**, native core version, and running instance are independent facts. Both native builders validate the pinned upstream source and embed **`1.14.2-sakamoto`** through the upstream linker flags, rather than relying on a missing Git tag in a temporary module copy; native integration tests assert that the loaded library reports it. iOS app/extension and Android JNI bind `pkg/mobilecore` alongside libbox in one Go runtime per process. Platform recovery never installs or executes an unsigned/previous engine. Android's current native service shares the app process: an irrecoverably hung native call may require Android App info > Force stop, then reopening the app; system VPN settings remain available to release routing. Do not start a second core while shutdown is unconfirmed.

## Repeatable verification

Run the Go process/filesystem fault lane with a persistent artifact directory:

```sh
SAKAMOTO_RECOVERY_ARTIFACT_DIR=/tmp/sakamoto-mobile-recovery/e2e \
  go test -race ./internal/recovery ./internal/restorepoint ./internal/svc \
  -run 'TestMobileRecovery|TestRecovery|TestRepair' -count=1 -timeout=90s
```

The iOS `project-native-tests.yml` scheme includes `NativeRecoveryIntegrationTests`: real libbox service/command transport plus shared admission, stable-interval rejection, stop holds, generation rejection and failed-fallback blocking. Its `mobile-core-recovery` JSON attachment records native identity and outcomes. This simulator lane does not claim to exercise a real NetworkExtension TUN or OS jetsam relaunch. `SakamotoEditorUITests/RecoveryUITests` separately checks the actual Home entry and read-only check interaction, including an explicit unavailable result when simulator NetworkExtension IPC cannot be used; screenshot attachments record both states.

Android `NativeBindingSmokeTest -e recovery true` exercises the real JNI core and shared recovery contract. The separate **isolated, consented emulator only** lane uses `-e recoveryVpn true`; it starts a real foreground VpnService/TUN, stops during native startup at several timings, clicks the Home repair entry, verifies retained healthy identity, rejects missing staged files without replacing the live core, forces a native bind failure and verifies configuration rollback, then explicitly reconnects. Receipts are `cache/mobile-core-recovery.json` and `cache/mobile-vpn-recovery.json`, plus `cache/mobile-recovery-home.png`. It preserves and restores fixture configuration/recovery files and refuses a preexisting VPN owner.

```sh
adb -s <isolated-emulator> shell appops set com.pidal.sakamoto ACTIVATE_VPN allow
adb -s <isolated-emulator> shell am instrument -w -e recovery true -e recoveryVpn true \
  com.pidal.sakamoto.test/com.pidal.sakamoto.NativeBindingSmokeTest
```

The source verification entry points enumerate `cmd`, `internal`, `pkg` and `tools` explicitly. Generated investigative Go files under ignored `ios/build` directories are not application source and must not alter module dependencies or verification results.
