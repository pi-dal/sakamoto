# Mobile observation and power behavior

Mobile statistics intervals use native nanoseconds. The desktop interval correction in `29d0662` is separate from these mobile changes.

## Subscription ownership

| Owner and demand | Commands | Periodic byte updates |
| --- | --- | --- |
| iOS foreground action channel | Groups, ClashMode | None |
| iOS visible Data page | Status, Connections, Log | 1 second |
| iOS provider, all automatic features disabled | None | None |
| iOS provider, fallback only | Groups, ClashMode | None |
| iOS provider, learning or CF evidence enabled | Groups, ClashMode, Connections, Log | 5 seconds |
| Android service action channel | Groups, ClashMode | None |
| Android observer, Data visible | Status, Connections; Log only when learning also needs it | 1 second |
| Android observer, learning without Data | Connections, Log | 5 seconds |
| Android observer, neither Data nor learning | None | None |

The action channel owns mode, selection, reload, close-connection and Tailscale actions. Opening or closing Data changes only the observation client. It does not reload the core, TUN or Tailscale endpoint. iOS stops UI command observation in the background while retaining the desired running state for foreground restoration. Android keeps the service-owned action channel for automatic policies and system status.

New and closed connection evidence still arrives immediately. The five-second interval reduces periodic byte-delta work that learning does not use. Initial historical log batches are discarded. Android builds and sorts UI connection rows only when Data is visible. iOS cancels Data consumers on departure and replays the newest full snapshots on return.

Clients reject callbacks from obsolete attachments. iOS waits for availability events instead of polling every 500 milliseconds. Unexpected disconnects receive a new action handler, preventing canceled old streams from invalidating the new connection. Android similarly assigns a generation to each action client and observation profile.

## Health checks and recovery

Successful fresh recovery rounds progressively change the idle delay from 30 to 60, 120 and 300 seconds. Learning caps the delay at 60 seconds and still requires selected-proxy health within the existing 120-second evidence lifetime. Failure, a monitored selector change or an actual network-path change resets the schedule. Network changes invalidate health from the previous network and cancel an outstanding recovery round before scheduling another.

Signal-strength and ordinary capability updates do not independently reset Android's probe schedule. Network identity or link changes do. iOS includes native address, flag and MTU changes when identifying a new path. Explicit provider wake requests a health refresh.

Fallback checks park when the user selects outside the configured priority chains. Rule-mode changes and monitored selection events wake them again. Learning can still check the selected proxy without changing a manual selection. Config edits continue to block automatic changes until Apply or reconnect; Android's saved settings become active runtime policy on that boundary.

Native URL tests run at most ten leaves per batch and allow fifteen seconds per leaf. Recovery waits `5 + ceil(uniqueLeafCount / 10) * 15` seconds for the largest requested group. Nested groups deduplicate leaves and terminate cycles conservatively. Seven members receive twenty seconds; eleven receive thirty-five. Both platforms cross a whole-second boundary before starting a round, so prior cached tests cannot satisfy the round. Manual selection changes during the wait are preserved.

A stable fallback-only profile can wait up to five minutes before starting another check when a failure produces no network, selection or native health event; the native result window follows. This is the explicit tradeoff for fewer active background probes. A latency observation still expires after 120 seconds; reduced probing does not keep it labeled fresh.

## Event-driven projections

Android Home collects state and network facts only while resumed. Saved group parsing is cached until staged configuration changes. The notification/widget owner combines service, group, experiment, network and persisted-settings events. It reads settings when they change and schedules one deadline for the selected latency's expiration, replacing the old two-second polling loop. Android Data retains its one-second presentation cadence only while resumed.

## Verification

The native Android instrumentation uses a synthetic direct-only core with no TUN or owner configuration. It switches the actual Data/Home tabs, checks disabled/fallback/learning monitoring, cancels recovery on a network event, exercises rapid stop/start and verifies action-client/core identity across observation changes. Its repeatable artifact is `cache/power-observation.json`:

```sh
mise -E android exec -- android/gradlew -p android :app:assembleDebug :app:assembleDebugAndroidTest :app:testDebugUnitTest :app:lintDebug
# Install both debug APKs in an isolated emulator first.
adb -s "$SAKAMOTO_TEST_SERIAL" shell am instrument -w -e power true com.pidal.sakamoto.test/com.pidal.sakamoto.NativeBindingSmokeTest
adb -s "$SAKAMOTO_TEST_SERIAL" shell run-as com.pidal.sakamoto cat cache/power-observation.json
```

`NativePowerIntegrationTests` runs the real iOS libbox core and command bridge with DataModel consumers, tests visibility and foreground restoration, and forces a synthetic transport restart. It keeps a `power-observation` JSON attachment in the Xcode result bundle. The simulator fixture uses an authenticated loopback endpoint because simulator App Group paths exceed the Unix socket address limit; production uses the existing App Group socket. Run simulator tests with ad hoc signing so App Group access is available:

```sh
xcodegen generate --spec ios/project.yml
xcodebuild -project ios/Sakamoto.xcodeproj -scheme Sakamoto -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" CODE_SIGNING_ALLOWED=YES CODE_SIGN_IDENTITY=- -only-testing:SakamotoAppTests test
swift test --package-path ios
```

These checks establish subscription/lifecycle correctness. They do not measure physical-phone battery savings. Comparing screen-off, foreground Data, fixed traffic and network handoff on signed physical devices is still required before claiming a percentage reduction in energy use.
