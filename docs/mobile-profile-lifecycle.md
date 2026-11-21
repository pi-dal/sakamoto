# Mobile profile lifecycle verification

Run from the repository root using disposable simulators. The Android scenario imports synthetic profiles through the app and replaces the staged configuration on that emulator. Use the project Go and sing-box versions, PyYAML, and the current native mobile bindings.

## Export and inspect

```sh
export FIXTURE_DIR="$(mktemp -d)"
uv run --with pyyaml python scripts/check-mobile-profile-lifecycle.py "$FIXTURE_DIR"
uv run --with pyyaml python scripts/check-private-exports.py
python3 scripts/check-privacy-generation.py
mise run go:test
```

The SOCKS fixture uses `tmux` to open the built desktop TUI against the disposable settings. It sets Latency tolerance to 250 ms, opens Config → Proxy groups → OthersAuto, excludes the VLESS node, and presses Ctrl-S. `tui-group-save.json` records both saved settings. The export check reads the tolerance from the packaged outbound. `tolerance-first.json` and `tolerance-restart.json` record the value from the iOS runtime configuration at import, after each generation, and after a fresh launch.

`exports.json` records the automatic group members and the explicit direct and proxy routes after decompiling the packaged SRS resources. The fixture generator also writes `legacy-groups.sakamoto` for the iOS upgrade test.

`implicit-first.json` and `implicit-restart.json` record `OthersAuto`, `ManualPick`, and the first SOCKS exit’s detour for a desktop chain with two SOCKS nodes and no explicit exit. The first report also records the recovery instruction for an older export containing only an allowlisted Shadowrocket node.

The Generated config editor scenario removes the TUN inbound from a working configuration and presses Save through the editor model. It checks the error, draft and manifest bytes, then connects from Home and sends an explicitly proxied request in each process. `editor-first.json` and `editor-restart.json` record the status and the profile and rule revision comparison.

The automatic connection form test forces its first preference read to fail, attempts Save, and verifies zero writes before retrying the read. A second scenario holds the preference save during disconnect and requests Connect. The simulator checks zero tunnel stops, a connected session, and the original on-demand policy in a fresh process.

With on-demand connection paused, the test cancels Disconnect during the preference read. It checks the stop count, the connected state and a 204 from the original proxy route. It also opens the form after a manual disconnect, re-enables Domains and reads the saved trigger and probe URL in the next process.

The form test enters an invalid domain or probe URL while Domains is active. It checks that the saved rule survives the invalid edit, selects Off and saves. Both the saving process and a new process must read `isOnDemandEnabled == false` and an empty rule list. The observed flags are in `hidden-draft-first.json` and `hidden-draft-restart.json`.

The automatic connection test saves a domain trigger with a loopback probe URL, switches it off, and faults the preference reload after the write. The simulator fixture archives the native NetworkExtension rules. A second process reads that archive and checks the enabled flags, rule order, domains and probe URL. The reports are `on-demand-first.json` and `on-demand-restart.json`.

The shortcut test invokes `ConnectTunnelIntent.perform()` with a simulator VPN preference fixture. It injects a reload failure, a profile change and a rejected start after B is saved. For each case it reloads the preferences, checks A’s profile ID and payload digest, and sends a proxied request using that payload. `shortcut-first.json` and `shortcut-restart.json` record the HTTP responses.

The rejected Apply test confirms a working proxy route, generates a REJECT rule for the same domain, and exercises provider rollback after a lost reload acknowledgment. Home reconnects in both processes. The test then retries the candidate through Apply and checks the confirmed revision. Results are in `apply-rejected-first.json` and `apply-rejected-restart.json`.

The saved-subscription scenario submits an editable subscription from the Config form, receives HTTP 404 on Apply, disconnects, and reconnects from Home. `subscription-first.json` and `subscription-restart.json` record the pending subscription, the selected rule revision and the proxied response.

The Add exit & generate failure scenario starts from a connected export with a working subscription. It records the current nodes and rule revision, then directs the subscription server to return HTTP 404. `exit-failure-first.json` and `exit-failure-restart.json` record the configuration comparison and the response from an explicitly proxied request after the failure and after a fresh launch.

The mixed-source fixture combines an allowlisted Shadowrocket entry, a manual node, and a Reality link with a desktop uTLS override. `mixed-first.json` and `mixed-restart.json` record the members and fingerprint from import, two generations, Apply and cold launch. The cancellation check holds a subscription response on loopback until the test cancels generation, then compares the profile manifest bytes and reopens the store to check the rule revision.

## Regional DNS after iOS generation

Build the current Go bridge with `mise run ios:libbox` and generate the lifecycle fixtures above. On a booted disposable simulator with the test host installed, set `SAKAMOTO_TEST_SIMULATOR` to its UDID and run

    python3 scripts/check-ios-regional-generation.py "$FIXTURE_DIR/regional-ios.sakamoto" "$FIXTURE_DIR/ios-regional-generation.json"

The simulator imports the desktop package, generates and applies it twice, then resolves the confirmed provider reference in a fresh process. The script replays each captured payload with the host sing-box. Loopback ingress and a temporary test certificate allow a DNS query through the approved VLESS relay to a local DoH endpoint. The relay accepts only the fixture resolver address and port. The report records each stage's answer and the request count at that endpoint.

To exercise device settings on an accepted legacy export, run both commands on the disposable simulators. The fixture removes the package provenance version and starts with Regional DNS disabled. Each client imports the package, saves a relay and resolver locally, applies the settings, and reconnects in a fresh process. The Android report includes query counts at both the regional and ordinary resolvers.

    python3 scripts/check-ios-regional-generation.py "$FIXTURE_DIR/regional-ios.sakamoto" "$FIXTURE_DIR/ios-regional-local.json" --legacy-local
    python3 scripts/check-android-profile-vpn.py "$FIXTURE_DIR" --legacy-regional

The manual route scenario imports a legacy package, adds an exact proxy rule for `manual.fixture.example` through the Android policy form or iOS generated configuration editor, and checks for a 204 response through the selected proxy after Apply and a process restart. Run these commands sequentially because the fixtures share a loopback port.

    python3 scripts/check-ios-profile-vpn.py "$FIXTURE_DIR" --legacy-manual
    python3 scripts/check-android-profile-vpn.py "$FIXTURE_DIR" --legacy-manual

## Historical desktop learning rules

On a disposable iOS simulator with the app installed, supply the historical desktop export containing the fixture learning record, a clean control export containing the explicit rules, and a fresh result directory. The check imports through Config, relaunches the app and decompiles the selected rule files in both processes.

	historical=path/to/baseline/groups.sakamoto
	python3 scripts/check-ios-legacy-import.py "$historical" "$FIXTURE_DIR/groups.sakamoto" "$(mktemp -d)"
	python3 scripts/check-ios-legacy-import.py "$historical" "$FIXTURE_DIR/groups.sakamoto" "$(mktemp -d)" --move-proxy-up

	python3 scripts/check-ios-legacy-import.py "$historical" "$FIXTURE_DIR/groups.sakamoto" "$(mktemp -d)" --format

The format run feeds the historical configuration through `sing-box format` and checks the resulting scalar domain match. The second run moves `rs-proxy` up one position as the legacy route editor did. Both runs restore a previously selected profile and its confirmed VPN settings, check the re-export prompt on launch and relaunch, and compare the manifest and rule files with their original bytes.

On the disposable Android emulator with VPN permission and the two APKs installed, check both rule orders with the service start action. In each installed configuration the test taps Repair on Home, reads the re-export instruction from the page and checks the core instance count and manifest digest. The script also checks ZIP import and cold starts using the formatted historical configuration. It restores the emulator's previous manifest after the run.

	python3 scripts/check-android-legacy-installed.py path/to/baseline/A.zip "$FIXTURE_DIR/android-legacy-installed.json"

For the clean Android export control, run `python3 scripts/check-android-profile-vpn.py "$FIXTURE_DIR" --export-control`. Compare the recorded direct and proxy responses, failed-origin SYN counts and health endpoint requests for A and B.

## Android upgrade with global learning history

Start with a fresh disposable emulator. Use sing-box 1.14.2 on PATH and the current debug APKs. The upgrade fixture replaces the emulator's active manifest and legacy experiment preferences with synthetic data. Build the historical export from commit `be44dfc` in a temporary directory.

    legacy=$(mktemp -d)
    git archive be44dfc | tar -x -C "$legacy"
    uv run --with pyyaml python scripts/build-legacy-learning-fixture.py "$legacy" "$FIXTURE_DIR/installed-B.zip"
    python3 scripts/check-android-profile-vpn.py "$FIXTURE_DIR" --installed-learning "$FIXTURE_DIR/installed-B.zip"

The A control makes one failed request and one direct learning probe. B starts from the installed legacy files, with A's threshold set to one and its pending transaction matching B's configuration. Check `vpn-A.json`, `vpn-upgrade-B.json` and `vpn-upgrade-restart.json`. The expected failed-origin SYN counts are 2, 1 and 1. Both B reports also check the health endpoint, explicit routes, visible learning records and preservation of the old pending transaction.

For the reverse upgrade case, start a second fresh emulator and build the historical package with the desktop mode set to auto. The fixture writes the old phone preference as off. It observes the first connection, relaunches the process, then selects auto in Experiments, confirms the choice and applies it to the running VPN.

    uv run --with pyyaml python scripts/build-legacy-learning-fixture.py "$legacy" "$FIXTURE_DIR/installed-auto.zip" --export-mode auto
    python3 scripts/check-android-profile-vpn.py "$FIXTURE_DIR" --installed-learning "$FIXTURE_DIR/installed-auto.zip" --confirm-learning

The expected SYN counts are 2 for the positive control, 1 for each upgraded launch, and 2 after the user confirms learning. The upgrade reports also record whether the settings page displayed the instructions.

For the fallback upgrade check, use a fresh disposable emulator and the same historical checkout. The desktop export enables fallback and the legacy phone preferences disable it.

    uv run --with pyyaml python scripts/build-legacy-learning-fixture.py "$legacy" "$FIXTURE_DIR/installed-fallback.zip" --fallback
    python3 scripts/check-android-profile-vpn.py "$FIXTURE_DIR" --installed-fallback "$FIXTURE_DIR/installed-fallback.zip"

The four `vpn-fallback-*.json` reports record the active selector, the response from its actual proxy exit, and connections to a probe-only candidate. Both upgraded launches should use ManualPick (HTTP 202) and produce zero candidate probes. Enabling fallback in Experiments and applying the configuration should test the candidate and switch to OthersAuto (HTTP 204). The final cold start should retain the enabled setting and reach the same exit.

To verify an installed phone that switched off a desktop proxy override, use a fresh disposable emulator and the historical checkout prepared above.

    uv run --with pyyaml python scripts/build-legacy-learning-fixture.py "$legacy" "$FIXTURE_DIR/installed-on.zip" --export-mode on
    python3 scripts/check-android-profile-vpn.py "$FIXTURE_DIR" --installed-final "$FIXTURE_DIR/installed-on.zip"

The fixture stages the historical phone save with a direct FINAL and desktop mode on. The four `vpn-oldfinal-*.json` reports record requests to `unmatched.fixture.example` through the ordinary router. Upgrade uses direct (200). Choosing on in Experiments and applying uses the proxy (204), including after a cold start. Choosing off and applying restores direct (200).

## Saved Tailscale key across profiles

Use a fresh disposable Android installation, the exported A and B fixtures, and the current debug and instrumentation APKs. The test stores a synthetic key with Android Keystore, starts A with a loopback Tailscale control URL, imports B after disconnecting, applies B, cold-starts B, and returns to A. The six `vpn-tskey-*.json` files record key retention and the HTTP responses from the explicit direct and proxy routes (200 and 204).

    python3 scripts/check-android-profile-vpn.py "$FIXTURE_DIR" --tailscale-key

The Android save phase attempts to replace the existing key from Tailscale settings while the private directory is read-only. It checks the visible save error and compares the original ciphertext byte for byte. The save phase also makes the Tailscale ciphertext unreadable during Apply. The rejected reload must leave the same core instance serving explicit direct and proxy requests. Restoring file access allows a subsequent Apply to succeed. The later launches verify that the same key can still be read and injected. The delete phase makes the directory read-only again, checks the error in the confirmation dialog, restores access and retries deletion. The last cold start checks the endpoint has no key.

The iOS native test checks key access during endpoint removal and restoration. Set `SIMULATOR_ID` to the test simulator UUID.

    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SIMULATOR_ID" -parallel-testing-enabled NO -only-testing:SakamotoNativeTests/NativeTailscaleIntegrationTests/testPreparedTailnetConfigurationCanBeReappliedAndDisabled test

To check migration from the App Apply entry, use a disposable simulator with an empty shared Tailscale keychain item and the exported `groups.sakamoto` in the app Documents directory. The test seeds a legacy item, applies the export, enables the endpoint and applies again. The first Apply preserves the legacy item. During Apply with the endpoint enabled, a simulated shared-keychain write failure must surface in the App while the previous tunnel configuration and legacy item stay available. The next Apply restores shared writes and injects a legacy-item deletion failure. The test verifies that the previous tunnel still serves the profile and both keychain items exist. Retrying with deletion enabled removes the legacy item and injects the migrated key.

    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SIMULATOR_ID" -parallel-testing-enabled NO -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testApplyMigratesKeyOnlyForTailscaleEndpoint test

The iOS credential editor regression drives Settings on a disposable simulator. It attempts to replace an existing Tailscale key with a different value while legacy cleanup fails, cancels, relaunches, and checks the key supplied to the endpoint. The S3 editor shows a read error, opens a new draft on retry, and discards edits on cancel. The S3 endpoint test below verifies signed GET requests in the same session and after a process restart.

    xcodebuild -project ios/Sakamoto.xcodeproj -scheme Sakamoto -destination "platform=iOS Simulator,id=$SIMULATOR_ID" -parallel-testing-enabled NO -only-testing:SakamotoEditorUITests/NodeGroupUITests/testCredentialEditorsPreserveOriginalAfterCancelAndRelaunch test

The legacy-key deletion regression starts with the only copy in the old keychain item. Settings reports the injected shared-keychain deletion error. After relaunch, the app prepares the same endpoint with its original key. On the reopened Settings page, a one-time keychain error makes the next deletion fail. Retrying on that same page must replace the error with “Stored auth key deleted”. A final cold launch checks that the endpoint carries no key.

    xcodebuild -project ios/Sakamoto.xcodeproj -scheme Sakamoto -destination "platform=iOS Simulator,id=$SIMULATOR_ID" -parallel-testing-enabled NO -only-testing:SakamotoEditorUITests/NodeGroupUITests/testLegacyOnlyKeySurvivesDeleteFailureAndCanBeDeletedAfterRelaunch test

The Tailscale profile-switch regression starts with all four endpoint options set in A. From the open Settings page, it switches to B through the Home configuration menu, returns to Settings, enables the endpoint and taps Apply. The simulator's Apply receiver checks the four defaults. A cold launch verifies them again in B's system connection options. The result is recorded in `ios-tailnet-profile-switch.json`.

    DOCS="$(xcrun simctl get_app_container "$SIMULATOR_ID" com.pidal.sakamoto data)/Documents"
    cp "$FIXTURE_DIR/groups.sakamoto" "$FIXTURE_DIR/mixed.sakamoto" "$DOCS/"
    xcodebuild -project ios/Sakamoto.xcodeproj -scheme Sakamoto -destination "platform=iOS Simulator,id=$SIMULATOR_ID" -parallel-testing-enabled NO -only-testing:SakamotoEditorUITests/NodeGroupUITests/testTailscaleDraftsFollowSelectedProfile test

The proxy-chain regression uses the same two exports in Documents. In Settings → Configuration details → Proxy chain, enter a synthetic Trojan link in A and switch to B from Home before submitting. The editor must clear the link and disable generation. Adding B's own link must generate successfully. The cold launch checks B's `nodes.txt` and connection configuration for B's server and credential and for the absence of A's values. Rebuild the shared Go binding before running this case if the link parser changed.

    xcodebuild -project ios/Sakamoto.xcodeproj -scheme Sakamoto -destination "platform=iOS Simulator,id=$SIMULATOR_ID" -parallel-testing-enabled NO -only-testing:SakamotoEditorUITests/NodeGroupUITests/testChainExitDraftStaysWithItsProfile -only-testing:SakamotoEditorUITests/NodeGroupUITests/testVLESSChainExitSavesReopensAndDisables -resultBundlePath "$RESULT_DIR/chain.xcresult" test

The Experiment profile-switch regression uses the two exports above. A has one synthetic failed domain and B has an unreadable runtime history file. The test edits a priority chain in A, opens its failed-domain picker, cancels and switches to B through Home. It checks that B has empty priority fields, disables the empty save and reports unavailable history. After cold launch it compares B's saved Experiment settings and explicit rules to their original values. The result is in `ios-experiment-profile-switch.json`.

    xcodebuild -project ios/Sakamoto.xcodeproj -scheme Sakamoto -destination "platform=iOS Simulator,id=$SIMULATOR_ID" -parallel-testing-enabled NO -only-testing:SakamotoEditorUITests/NodeGroupUITests/testExperimentDraftAndUnreadableHistoryFollowProfile -resultBundlePath "$RESULT_DIR/experiment.xcresult" test

The cancelled-source regression uses the same two exports in Documents. It enters a token-bearing loopback URL in A's configuration import and subscription sheets, cancels both, selects B through Home, and reopens both sheets. It checks for empty fields, disabled submit buttons, zero connections to the listener, and an identical source bundle for B after a cold launch. See `ios-cancelled-source-forms.json`.

    xcodebuild -project ios/Sakamoto.xcodeproj -scheme Sakamoto -destination "platform=iOS Simulator,id=$SIMULATOR_ID" -parallel-testing-enabled NO -only-testing:SakamotoEditorUITests/NodeGroupUITests/testCancelledSourceFormsStayWithProfile -resultBundlePath "$RESULT_DIR/source.xcresult" test

The import-details test uses the same two installed exports and three local files. Set `LOCAL_FILES` to the disposable simulator's **On My iPhone** Files provider directory. The test imports the valid sources through Config → Add sources → From Files, then tries a malformed JSON file in A. It checks A's error and its previous DNS and FINAL values. After switching to B through Home, it checks B's source details and verifies that A's failed filename is absent from both Import details and Config. The run summary is in `ios-import-details.json`.

    (set -C; printf '{invalid' > "$LOCAL_FILES/PrivateFailedFixtureA.json")

    (set -C; printf '[General]\ndns-server = 192.0.2.31\n[Rule]\nFINAL,DIRECT\n' > "$LOCAL_FILES/ReportFixtureA.conf")
    (set -C; printf '[General]\ndns-server = 192.0.2.32\n[Rule]\nFINAL,PROXY\n' > "$LOCAL_FILES/ReportFixtureB.conf")
    xcodebuild -project ios/Sakamoto.xcodeproj -scheme Sakamoto -destination "platform=iOS Simulator,id=$SIMULATOR_ID" -parallel-testing-enabled NO -only-testing:SakamotoEditorUITests/NodeGroupUITests/testImportDetailsFollowSelectedSource -resultBundlePath "$RESULT_DIR/report.xcresult" test

The node-file feedback regression uses the same two exports and local Files directory. In A, import `NoticeFixtureA.txt`, try the LF file, import `NoticeFixtureA.txt` again, and try the CRLF file. The two malformed files have distinct valid first lines. Both must report line 2 and leave the first line uncommitted. The duplicate import must report zero additions. Select B from Home and import its single node. See `ios-node-file-notice.json`.

    (set -C; printf '%s\r\n' 'trojan://fixture@partialcrlf.invalid:443#PartialCRLF' 'invalid-node' > "$LOCAL_FILES/NoticeFixtureCRLF.txt")
    (set -C; printf '%s\n' 'trojan://fixture@partial.invalid:443#PartialImport' 'invalid-node' > "$LOCAL_FILES/NoticeFixtureInvalid.txt")

    (set -C; printf '%s\n' 'trojan://fixture@noticeonlya.invalid:443#NoticeOnlyA' 'trojan://fixture@noticeothera.invalid:443#NoticeOtherA' > "$LOCAL_FILES/NoticeFixtureA.txt")
    (set -C; printf '%s\n' 'trojan://fixture@noticeonlyb.invalid:443#NoticeOnlyB' > "$LOCAL_FILES/NoticeFixtureB.txt")
    xcodebuild -project ios/Sakamoto.xcodeproj -scheme Sakamoto -destination "platform=iOS Simulator,id=$SIMULATOR_ID" -parallel-testing-enabled NO -only-testing:SakamotoEditorUITests/NodeGroupUITests/testNodeFileImportNoticeFollowsSelection -resultBundlePath "$RESULT_DIR/notice.xcresult" test

For the comment-only manual node list, copy the freshly exported `sr-only.sakamoto` into the disposable simulator’s **On My iPhone** directory. This package has one approved Shadowrocket node and a comment in `nodes.txt`. The UI test imports it through Files and reads `OthersAuto` from the configuration editor after import, after each of two generations, and after a cold launch. The run summary is in `ios-comment-only-nodes.json`.

    cp "$FIXTURE_DIR/sr-only.sakamoto" "$LOCAL_FILES/"
    xcodebuild -project ios/Sakamoto.xcodeproj -scheme Sakamoto -destination "platform=iOS Simulator,id=$SIMULATOR_ID" -parallel-testing-enabled NO -only-testing:SakamotoEditorUITests/NodeGroupUITests/testCommentOnlyManualNodesInShadowrocketExport -resultBundlePath "$RESULT_DIR/groups.xcresult" test

## Android import and restart

On a disposable rootable emulator, run `python3 scripts/check-android-s3-profile.py "$FIXTURE_DIR"` after building the two Android APKs. The test uses the settings page and a loopback HTTPS object store. `s3-profile.json` records the filenames and marker presence from each actual upload, together with the Android source type before and after restarting B.

The nested-include check generates a desktop ZIP, imports it on Android and compares the first S3 PUT against the exported main file and both relative includes. The deepest include contains the explicit proxy rule recorded in `s3-download-nested.json`.

```sh
python3 scripts/check-private-exports.py "$FIXTURE_DIR"
python3 scripts/check-android-s3-profile.py "$FIXTURE_DIR" nested
python3 scripts/check-android-s3-profile.py "$FIXTURE_DIR" node-text
python3 scripts/check-android-s3-profile.py "$FIXTURE_DIR" duplicates
python3 scripts/check-android-s3-profile.py "$FIXTURE_DIR" nested-edit
CHECK_LEGACY_NODES=1 python3 scripts/check-android-s3-profile.py "$FIXTURE_DIR" nested-edit
```

The `duplicates` check also needs `B.zip` from the lifecycle fixture generator. It archives both recovery files, imports B through the package picker, then exports both complete files from Source Files after a force-stop. The fixture compares the exported bytes and checks the signed S3 requests for B.

Run the S3 upload and credentials checks with the same fixtures and emulator:


```sh
for scenario in upload-control upload-cancel upload-switch credentials clipboard qr; do
    python3 scripts/check-android-s3-profile.py "$FIXTURE_DIR" "$scenario" || exit 1
done
```

The clipboard and QR checks confirm a text import in Config, open S3 settings and sync. The endpoint checks `conf/imported.conf` against the complete imported body, including its explicit domain rule.

The upload control edits a local source and lets its conditional PUT finish. The cancellation case closes the settings page while the GET is waiting. The endpoint checks the PUT count, marker presence and object digest. The USB switch case installs B through the host-sync provider during the same GET with the S3 page still open. It compares the installed source files to B.zip and checks for an unchanged object digest and zero update PUTs. The credentials case rejects a settings save with unreadable ciphertext and another with a read-only directory. It compares the ciphertext bytes after each failure, force-stops the app, and verifies the next sync's AWS signature with the original target and secret.

To exercise S3 downloads, use the local HTTPS endpoint to return a changed policy. The control should adopt the policy and mark its sources pending. The cancel case leaves the settings page while the GET is pending. The disk case makes the configuration directory read-only before releasing that GET. Both cases compare the complete manifest before and after the attempt, then restart the app and send direct and proxy requests through the VPN.

```sh
python3 scripts/check-android-s3-profile.py "$FIXTURE_DIR" control
for fault in cancel disk; do
    python3 scripts/check-android-s3-profile.py "$FIXTURE_DIR" "$fault" &&
        python3 scripts/check-android-profile-vpn.py "$FIXTURE_DIR" --s3-restart "$fault" || exit 1
done
```

Set `ANDROID_SERIAL` to an isolated running emulator. Use `mise -E android exec --` for SDK commands if required.

```sh
(cd android && mise -E android exec -- ./gradlew :app:assembleDebug :app:assembleDebugAndroidTest)
adb install -r -g android/app/build/outputs/apk/debug/app-debug.apk
adb install -r android/app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk
adb shell run-as com.pidal.sakamoto mkdir -p cache
for profile in A B; do
  adb shell "run-as com.pidal.sakamoto sh -c 'cat >cache/$profile.zip'" <"$FIXTURE_DIR/$profile.zip"
done
for phase in fixed restart; do
  adb shell am force-stop com.pidal.sakamoto
  adb shell am instrument -w -e profileIsolation "$phase" com.pidal.sakamoto.test/com.pidal.sakamoto.NativeBindingSmokeTest
  adb exec-out run-as com.pidal.sakamoto cat "cache/isolation-$phase.json" >"$FIXTURE_DIR/android-$phase.json"
done
```

Expect the instrumentation to report PASS twice. The scenario feeds a correlated failure candidate into the learning worker with an instrumented origin prober. A simulator loopback socket counts direct-probe calls. Profile A should produce one connection, B zero, and B zero again after a process restart. A rejected import must retain A. The scenario also checks that B starts with its own learning history and transaction journal.

## Android Repair after import

Run `python3 scripts/check-android-profile-vpn.py "$FIXTURE_DIR" --repair-switch` on the disposable emulator. A learns a fixture domain. The test waits for the five-minute stable interval and checks the snapshot for A’s cache ID. After disconnect, it imports B and taps Repair on Home before B’s first connection. The local server counts requests to each profile’s health endpoint. The emulator counts direct SYNs to a refused origin. The expected totals are two for A and one for B, including the original request in each phase. The results are recorded in `vpn-repair-A.json` and `vpn-repair-B.json`.

## Android experiment page after import

Run `python3 scripts/check-android-profile-vpn.py "$FIXTURE_DIR" --experiment-screen` on the disposable emulator. A learns the fixture origin through its running VPN. The test stops A, imports B and opens Experiments in the same process before connecting B. It checks the displayed count, the empty list and the absence of A’s domain. `vpn-screen-A.json` records the UI observations and the A packet count. `vpn-B.json` and `vpn-restart.json` record the direct SYN counts after B connects and after relaunch.

## Android VPN packet count

Build both Android APKs as above. Set `ANDROID_SERIAL` to a disposable, rootable arm64 emulator. The command installs those APKs and the generated packages, grants VPN consent on that emulator and starts the app's VPN service. Loopback DNS, VMess and HTTP endpoints provide distinguishable direct and proxy responses.

```sh
python3 scripts/check-android-profile-vpn.py "$FIXTURE_DIR"
```

The test submits hostname requests through the running core's ordinary router via JNI. Its normal log and connection subscriptions receive the resulting failure. An emulator firewall counter measures outgoing TCP SYNs to the failed origin. For one user request per phase, `vpn-A.json` should record two direct SYNs and one learning record. `vpn-B.json` and `vpn-restart.json` should each record one direct SYN and zero learning records. Each phase checks that the explicit direct and proxy routes reach their respective endpoints. The two exports use distinct local health endpoints and intervals (A 4m, B 7m). The test checks the applied URL and interval. The HTTP server counts requests to each endpoint across import and restart. Only the active profile’s endpoint should receive requests. The A phase also attempts to import `legacy-B.zip` with a broader device group and checks that rejection preserves the previous profile. It temporarily makes the configuration parent directory read-only and exercises the group editor, `.conf` and `nodes.txt` source saves, and the package importer. A second import cancels its coroutine job after the new manifest has been staged and before it replaces the active manifest. The test compares the manifest and source bytes, checks that the failed revision was removed, and runs Libbox validation against the original rules.

The USB scenario installs A with `sync-host.py DEVICE_SERIAL`, interrupts the upload of B after half of its ZIP has reached the emulator, and compares the active manifest and every revision file by SHA-256. It restarts the app and runs the VPN checks against A. `usb-interruption.json` and `vpn-usb-A.json` contain the results. Two further runs delete the last subscription from its Android row or download a replacement HTTPS source. Each attempts USB sync, restarts the app and checks the saved edit, pending generation flag and hashes of the manifest and revision files. See `usb-source-conflicts.json`. Run the Android script with PyYAML available for the USB helper.

The stale editor tests open the learning threshold, generated JSON, new node, new subscription and HTTPS source URL forms on A. Each test installs B over USB, submits the open form and compares B’s manifest byte for byte. The URL test counts resolver calls and checks whether a download started. The delete and undo tests publish B after the row action prepares its edit and before the repository saves it. Deletion exercises USB sync. Undo exercises the package importer with a pending deletion. Each test runs the resulting VPN and measures direct SYNs and learning records. Pass `--stale-only` to repeat these cases with the A learning control and a final process restart.

The USB chain scenario saves a two-hop proxy chain on the device, installs an unchained configuration through USB and repeats the request after an app restart. The two local HTTP exits return 202 and 204. Separate HTTPS listeners count DNS queries at each exit. Run the VPN script with `--chain-only` to repeat these three phases.

The Regional DNS scenarios import two additional packages with the same fixed TLS relay. Only A enables a dedicated resolver. Each phase sends an A query for `regional.fixture.example` through the VPN and checks the returned documentation address. `vpn-regional-{A,B,restart}.json` records the number of complete DNS messages received by each HTTPS listener. A must reach the dedicated resolver. B and the restarted B must reach the ordinary resolver with zero dedicated queries. The script temporarily trusts its generated certificate on the disposable emulator and removes it on exit. Pass `--regional-only` after the fixture directory to repeat these three phases.

The Android URL import scenario uses a temporary certificate, a loopback HTTPS server and a counting resolver. The protected host must produce zero resolver calls. An allowed host must download a configuration. When a package replaces the profile during a delayed response, its source and generation state must survive. Read the receipt with `adb exec-out run-as com.pidal.sakamoto cat cache/source-download.json`.

Pass `--deferred-only` to exercise the Android package picker, QR result, clipboard offer and delayed VPN reload. Each import starts on A and receives its result after USB installs B. The test checks the conflict message and compares B’s manifest and revision files byte for byte. The reload control confirms the candidate rejects a request that the original profile accepts. The timeout case delays native confirmation by 31 seconds and sends three requests after Apply returns failure. It leaves the candidate on disk, disconnects and force-stops the app. A new process reconnects normally and verifies the original HTTP 200 response and the candidate manifest. Explicitly applying the candidate then rejects the same request while the proxy control returns 204. Use `--reload-only` to repeat the timeout and cold reconnect phases together.

Run `--snapshot-only` to inject two Android filesystem faults. The first denies creation of the next snapshot directory. The second denies further writes inside it. Each case checks the live request, disconnects, force-stops the app, reconnects to the confirmed profile and explicitly retries the preserved edit. A separate control saves the same edit and reconnects after an ordinary disconnect.

`--revision-only` exercises two saves in the Android source editor and compiles the resulting `.conf` and `nodes.txt` with the desktop exporter. The instrumentation harness stages that output on the same profile revision. Android uses the previous direct rule after an Apply write failure and a fresh process reconnect. An explicit retry switches the request to the proxy.

`--pending-only` opens the Android source editor while connected, changes an explicit DIRECT rule to PROXY and leaves the source pending generation. The test disconnects from Home. In a fresh process it taps the Home switch once and checks the confirmed direct and proxy responses and the saved source receipt.

`--reconnect-fault-only` saves a generated edit, disconnects and makes the next snapshot directory read-only before the first Home reconnect. On a disposable emulator without an app-wide stable snapshot, one Home click restores the confirmed direct response. Restoring write access and explicitly applying the preserved edit rejects that request. The proxy control returns 204.

`--policy-only` starts the exported automatic-learning profile with an empty learned list. The Android policy form adds PROXY and then DIRECT for one host. Applying each edit returns 204 and 200 respectively. Removing the DIRECT rule restores the proxy response, including after process restart.

Run `python3 scripts/check-android-profile-vpn.py "$FIXTURE_DIR" --learning-recovery` to verify recovery of a learned route. After the fixture domain returns 204 through the proxy, the test kills the process while its VPN is running. The next process reconnects and requests the same domain. A separate run learns the domain and expires the Apply deadline after native replacement. The emulator resets its direct SYN counter before Apply. Both recovery requests must return 204 with zero direct SYNs. See `vpn-learn-kill.json`, `vpn-learn-restart.json` and `vpn-learn-rollback.json`.

Run `python3 scripts/check-android-profile-vpn.py "$FIXTURE_DIR" --history-fault` to deny history writes before the first learning probe. The experiment reports the save failure. Two requests then reach the proxy endpoint through the same VPN instance. After relaunch, the test checks that the history is empty and the learned domain still reaches the proxy. `vpn-history-denied.json` and `vpn-history-restart.json` record both HTTP responses and the direct SYN counts.

`--forget-fault` learns a fixture domain and verifies its proxy response before deleting it through Experiments with the app’s preferences directory read-only. The harness force-stops the process, restores the directory permissions and reconnects. `vpn-forget-edit.json` and `vpn-forget-restart.json` record the learned count, direct SYNs to the refused fixture origin and an independent proxy response. The denied-write control makes the manifest directory read-only during deletion. It checks that the saved manifest bytes match the original. `vpn-forget-denied-restart.json` records one learned domain, its HTTP 204 proxy response and zero direct SYNs after relaunch. The device manifest commits the learned list and generated rules in one atomic replacement. Installed profiles with a preferences-based list adopt the new format on the next verified addition or deletion.

`--promotion-only` starts profile A and sends a request that fails on the direct exit. After the core verifies the proxy route, the test opens Experiments, selects the domain in “Add recent failures to config” and removes its learning record. The explicit policy and HTTP 204 proxy response must survive Apply and a fresh process restart with an empty learned list.

`--routing-changes-only` uses the routing and experiment pages. In B it selects MainProxy for unmatched traffic, turns the experiment on and off, applies and restarts. Both unmatched requests must return 204. In A it learns a domain, moves `rs-proxy` up, removes the learned domain and turns learning off. Its unmatched requests must reach the refused direct origin while the explicit proxy control returns 204.

`--priority-only` exercises the saved rule order through an `auto → off → auto` round trip in the experiment settings. After a verified learned route returns 204, the user adds REJECT for the same host and saves the order `rs-proxy → REJECT → learned rule` on the routing page. The test applies each mode change. After re-enabling learning and again in a fresh process, the core must reject the request, preserve the learned record and return 204 for the independent proxy control. The firewall counter checks for direct connection attempts.

## iCloud sync during profile selection

On a disposable simulator with the app installed, copy the two exported profiles to Documents. The test uses the Settings sync model with a temporary destination. It starts sync on A and selects B before the task resumes. It checks that the directory is empty and B's source bundle matches its pre-sync contents. It then selects A and compares each uploaded file with A's sources. The before/after XCTest counts are in `ios-icloud-profile-selection.json`.

    DOCS="$(xcrun simctl get_app_container "$SAKAMOTO_TEST_SIMULATOR" com.pidal.sakamoto data)/Documents"
    cp "$FIXTURE_DIR/groups.sakamoto" "$FIXTURE_DIR/mixed.sakamoto" "$DOCS/"
    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testICloudSyncUsesProfileSelectedAtTap -resultBundlePath "$FIXTURE_DIR/icloud-selection.xcresult" test

## Editing nodes after an iCloud download

With the same simulator and exported groups profile, save node A, sync it, then change the test folder to node B. After Settings sync downloads B, save A again through the node editor's save path and reload the profile. The next sync must upload A. The regression compares both the cloud file and the selected profile with the saved A bytes.

    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testICloudUploadsSavedReversionAfterDownload -resultBundlePath "$FIXTURE_DIR/icloud-edit.xcresult" test

The XCTest counts are in `dist/mobile-profile-lifecycle/ios-icloud-saved-edit.json`. Downloads now record a batch ID in the same manifest write as their sources. A legacy pending baseline without a batch ID requires conflict review when the two copies differ.

## iCloud write failures and cold restart

Use the disposable simulator and `groups.sakamoto` from the preceding check. The first process establishes a baseline with conf sync disabled. Enabling conf sync and updating `nodes.txt` in the test folder makes the next pass upload rules and download nodes. The test removes write permission from the baseline directory in one fixture and the destination directory in the other. Both passes must report failure. The second process compares the saved manifest byte for byte, checks source contents, group edits and regeneration state, then retries and verifies the uploaded conf and adopted nodes. The `mixed-pass-relaunch` marker selects the second phase and is removed after it passes.

    rm -f "$DOCS/mixed-pass-relaunch"
    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO -test-iterations 2 -test-repetition-relaunch-enabled YES -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testICloudMixedPassKeepsProfileOnWriteFailure -resultBundlePath "$FIXTURE_DIR/icloud-write-failure.xcresult" test

The before-fix failure count and both passing iterations are recorded in `dist/mobile-profile-lifecycle/ios-icloud-write-failure.json`.

## Editing nodes after an interrupted iCloud upload

Use the same disposable simulator and `groups.sakamoto`. Start both copies at A, save B on the phone and start a sync. One fixture denies the baseline write before the cloud replacement. The other denies it immediately after replacing the cloud file. Save A again and relaunch. The first fixture must report up to date with A on both sides. The second must show a conflict with A still on the phone and B in the folder. Choosing the device copy must then put A on both sides.

    rm -f "$DOCS/upload-edit-relaunch"
    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO -test-iterations 2 -test-repetition-relaunch-enabled YES -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testICloudUploadFailureAndNodeEditAcrossRelaunch -resultBundlePath "$FIXTURE_DIR/icloud-upload-edit.xcresult" test

`dist/mobile-profile-lifecycle/ios-icloud-upload-edit.json` records the original failure and the combined run of all four iCloud regressions across both process launches.

## First upload followed by a cloud conflict choice

Use the disposable simulator with `groups.sakamoto` in Documents. The fixture creates an empty remote folder, uploads node A and injects a baseline write failure immediately after the remote file is created. It saves B locally, reviews the conflict and chooses cloud A. The next process must load A and report up to date. The same test then restores the five-field baseline written by older builds, saves B and checks that both sides contain B after sync.

    rm -f "$DOCS/fixture.icloud-first-upload/legacy.json"
    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO -test-iterations 2 -test-repetition-relaunch-enabled YES -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testICloudFirstUploadConflictUsesCloudAcrossRelaunch -resultBundlePath "$FIXTURE_DIR/icloud-first-upload.xcresult" test

The original cold-launch failure and the five passing regressions across both processes are recorded in `dist/mobile-profile-lifecycle/ios-icloud-first-upload.json`.

## iCloud credential downloads and device backup

Use the disposable simulator and exported groups profile from the preceding checks. The fixture seeds the old sync cache with a synthetic proxy password and subscription token. It downloads both source files through Settings. One profile accepts the download and the other rejects the manifest write. Each process checks the backup exclusion on the sync directory and the absence of its legacy source cache. The second process verifies the adopted sources on success and reads the original manifest and connection configuration after failure.

    rm -f "$DOCS/credential-download-relaunch"
    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO -test-iterations 2 -test-repetition-relaunch-enabled YES -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testICloudCredentialDownloadsAcrossRelaunch -resultBundlePath "$FIXTURE_DIR/icloud-credential-download.xcresult" test

The before and after counts are in `dist/mobile-profile-lifecycle/ios-icloud-credential-download.json`.

## Recovering Config sources after a draft write fails

Use the disposable simulator with `groups.sakamoto` in Documents and set `SAKAMOTO_TEST_SIMULATOR` to its UUID. The first launch adopts new nodes and a new subscription through Settings while the draft directory is read-only. The second launch loads the Config editor, saves another node and regenerates the runtime. The local subscription server checks which feed generation requested. The runtime must include the adopted node, the added node and the node returned by the adopted feed.

    python3 scripts/check-ios-stale-draft.py "$FIXTURE_DIR/stale-draft.xcresult"

The same command also tests upgrading an installed profile with both legacy source preferences present. A read-only draft directory leaves both preferences available for retry. Once migration succeeds, each preference is removed and its exact bytes are retained in a protected recovery file under the backup-excluded PrivateDrafts directory. Both profiles regenerate with the selected subscription. The original editor regression is recorded in `dist/mobile-profile-lifecycle/ios-stale-draft.json`. The combined run is recorded in `dist/mobile-profile-lifecycle/ios-legacy-draft-migration.json`.

## Retrying a configuration with an unreadable manifest

Install `groups.sakamoto` and `mixed.sakamoto` in the disposable simulator's Documents directory. The first launch selects A and removes read access to B's manifest. The second launch tries B through the configuration picker's selection handler, checks A's selection and connection content, restores read access and compares B's manifest with the original bytes. Selecting B again must load its sources and build its connection configuration.

    rm -f "$DOCS/fixture.unreadable-manifest"
    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO -test-iterations 2 -test-repetition-relaunch-enabled YES -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testRetryUnreadableManifestAcrossRelaunch -resultBundlePath "$FIXTURE_DIR/manifest-retry.xcresult" test

The before and after results are in `dist/mobile-profile-lifecycle/ios-manifest-retry.json`.

## Recovering the configuration list on upgrade

With the same two packages in Documents, run the upgrade and manifest retry tests across two simulator processes. The upgrade fixture checks that a temporary read failure preserves the old runtime preference and that a successful launch clears it. It then corrupts an index entry and attempts another import. Config's recovery action reloads the ordered manifest IDs and restores the original selection. The fixture imports a third profile and reopens the store to select each original configuration.

    rm -f "$DOCS/fixture.upgrade-index-recovery" "$DOCS/fixture.unreadable-manifest"
    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO -test-iterations 2 -test-repetition-relaunch-enabled YES -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testUpgradeRuntimeAndIndexRecoveryAcrossRelaunch -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testRetryUnreadableManifestAcrossRelaunch -resultBundlePath "$FIXTURE_DIR/index-recovery.xcresult" test

See `dist/mobile-profile-lifecycle/ios-index-recovery.json` for the execution counts.

## Retrying the first migration of a legacy runtime

On the disposable simulator, the first process seeds an isolated preference domain with a standalone runtime and makes the Profiles directory read-only. It checks the original UTF-8 bytes and the absence of a new index or selection. The second process retries with permissions restored, compares the manifest with the original runtime, opens the new selection and prepares the connection configuration. The run also checks the previously installed profiles using the two packages above.

    rm -f "$DOCS/fixture.first-runtime-migration" "$DOCS/fixture.upgrade-index-recovery" "$DOCS/fixture.unreadable-manifest"
    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO -test-iterations 2 -test-repetition-relaunch-enabled YES -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testFirstRuntimeMigrationRetriesAfterWriteFailure -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testRetryUnreadableManifestAcrossRelaunch -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testUpgradeRuntimeAndIndexRecoveryAcrossRelaunch -resultBundlePath "$FIXTURE_DIR/first-runtime-migration.xcresult" test

The execution counts are in `dist/mobile-profile-lifecycle/ios-first-runtime-migration.json`.

## Importing a package after the first migration fails

Use the disposable simulator with `mixed.sakamoto` in Documents. The first process denies the initial legacy manifest write, restores permission and imports the package through Config's file action. The next process checks the imported selection, selects the migrated configuration, compares its original runtime bytes and prepares connection content for both profiles.

    rm -f "$DOCS/fixture.import-after-first-migration"
    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO -test-iterations 2 -test-repetition-relaunch-enabled YES -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testImportAfterFailedFirstMigrationAcrossRelaunch -resultBundlePath "$FIXTURE_DIR/import-after-first-migration.xcresult" test

The failure before the fix and the combined run of four migration and recovery checks are recorded in `dist/mobile-profile-lifecycle/ios-import-after-first-migration.json`.

## Import recovery with a missing index or a failed preference flush

Use the disposable simulator with `groups.sakamoto` and `mixed.sakamoto` in Documents. Config imports two packages. Four fixtures remove the primary index, empty it, retain only the first of two valid records, or make the next index flush return false. Each fixture attempts a third import through the file picker handler and checks the error, the original index bytes, the backup IDs and the selection. After relaunch, Config recovers the three damaged indexes and restores the saved selection. The partial-index fixture also checks that the connection entry point refuses to prepare the wrong profile before recovery. Each fixture prepares connections for both original profiles and retries the third import.

    rm -f "$DOCS/index-failure-relaunch"
    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO -test-iterations 2 -test-repetition-relaunch-enabled YES -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testImportWithIndexFailureAcrossRelaunch -resultBundlePath "$FIXTURE_DIR/index-flush.xcresult" test

The initial flush regression is recorded in `dist/mobile-profile-lifecycle/ios-index-flush.json`. The partial-index failure and the combined run after repair are recorded in `dist/mobile-profile-lifecycle/ios-index-partial.json`.

## Choosing a configuration after losing the saved selection

Use the disposable simulator with the groups and mixed packages in Documents. The first launch imports A and B. Five fixtures cover a missing selection with a complete or partial index, a selection pointing to a nonexistent C, a legacy store with only B’s selection ID, and two indexes containing only A while the selection and manifest for B are intact. The second checks that the system connection entry point rejects the request, reads the recoverable manifests and selects B through the configuration picker handler. The third checks the persisted selection and compares the runtime with B's exported configuration. For the last fixture it also switches back to A and checks A’s exported runtime. The five-fixture execution counts are in `dist/mobile-profile-lifecycle/ios-unindexed-selection.json`.

    rm -f "$DOCS/missing-selection-phase"
    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO -test-iterations 3 -test-repetition-relaunch-enabled YES -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testMissingSelectionRequiresChoiceAcrossRelaunch -resultBundlePath "$FIXTURE_DIR/missing-selection.xcresult" test

## Recovering when a backup index references a missing manifest

On the disposable simulator, import the groups and mixed packages. The fixture leaves B selected and removes A's manifest. The primary index lists B and the backup lists A and B. The recovery action shows B and identifies the missing manifest by ID with instructions to reimport its package. The test checks the original preferences before choosing B. A fresh process then loads B and prepares its connection configuration.

    rm -f "$DOCS/fixture.missing-backup-manifest" "$DOCS/missing-selection-phase"
    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO -test-iterations 3 -test-repetition-relaunch-enabled YES -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testMissingBackupManifestAcrossRelaunch -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testMissingSelectionRequiresChoiceAcrossRelaunch -resultBundlePath "$FIXTURE_DIR/missing-backup-manifest.xcresult" test

Execution counts are in `dist/mobile-profile-lifecycle/ios-missing-backup-manifest.json`.

## Retrying a damaged configuration after recovery

Use the disposable simulator with the groups and mixed packages in Documents. The corrupt-manifest fixture truncates A's JSON, leaves A in the backup index, and keeps B selected. The recovery picker identifies the unreadable ID and offers B. The test compares the preferences before confirmation. The third process prepares B's connection, verifies the damaged bytes and retries A after restoring its manifest. The adjacent tests exercise a missing manifest and saved selection recovery.

    rm -f "$DOCS/fixture.corrupt-backup-manifest" "$DOCS/fixture.missing-backup-manifest" "$DOCS/missing-selection-phase"
    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO -test-iterations 3 -test-repetition-relaunch-enabled YES -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testCorruptBackupManifestAcrossRelaunch -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testMissingBackupManifestAcrossRelaunch -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testMissingSelectionRequiresChoiceAcrossRelaunch -resultBundlePath "$FIXTURE_DIR/corrupt-backup-manifest.xcresult" test

See `dist/mobile-profile-lifecycle/ios-corrupt-backup-manifest.json` for the original failure and the execution counts after repair.

## Recovering a selected profile with a damaged source bundle

With the groups and mixed exports in the disposable simulator's Documents directory, the test truncates the selected mixed profile's embedded source JSON. It checks that recovery offers the groups profile, compares both indexes and the damaged manifest before confirmation, and prepares the groups connection in a fresh process. The run also covers missing and corrupt outer manifests and a missing selection.

    rm -f "$DOCS/fixture.corrupt-selected-bundle" "$DOCS/fixture.corrupt-backup-manifest" "$DOCS/fixture.missing-backup-manifest" "$DOCS/missing-selection-phase"
    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO -test-iterations 3 -test-repetition-relaunch-enabled YES -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testCorruptSelectedSourceBundleAcrossRelaunch -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testCorruptBackupManifestAcrossRelaunch -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testMissingBackupManifestAcrossRelaunch -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testMissingSelectionRequiresChoiceAcrossRelaunch -resultBundlePath "$FIXTURE_DIR/corrupt-selected-bundle.xcresult" test

The execution counts are in `dist/mobile-profile-lifecycle/ios-corrupt-selected-bundle.json`.

## Policy editing after an ordinary restart with damaged sources

Import the groups and mixed exports on the disposable simulator. The fixture leaves both indexes intact and truncates the selected mixed profile's embedded source JSON. On restart, submitting an explicit rule through Config reports how to recover. The test compares the damaged manifest bytes and saved indexes before choosing the groups profile. A third process prepares the groups connection and checks the mixed manifest again.

    rm -f "$DOCS/fixture.intact-index-corrupt-bundle"
    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO -test-iterations 3 -test-repetition-relaunch-enabled YES -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testPolicySaveWithCorruptSelectedBundleAcrossRelaunch -resultBundlePath "$FIXTURE_DIR/intact-index-corrupt-bundle.xcresult" test

The execution summary for this and the four adjacent recovery tests is in `dist/mobile-profile-lifecycle/ios-intact-index-corrupt-bundle.json`.

## Reimporting the only installed profile after source corruption

Place the mixed desktop export in the disposable simulator's Documents directory. The first process imports the package and damages its embedded source JSON. The second runs Recover configuration list, injects an index flush failure during file import, compares the original preferences and manifest bytes, and retries the same package. The test also compares the profile directory listing across the failed import and checks each original rule file against the desktop package. The third process prepares the connection and compares the restored sources and runtime configuration with the desktop export. The before-and-after test results for the directory rollback are in `dist/mobile-profile-lifecycle/ios-import-directory-rollback.json`.

    rm -f "$DOCS/fixture.reimport-only-profile"
    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO -test-iterations 3 -test-repetition-relaunch-enabled YES -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testReimportOnlyDamagedProfileAcrossRelaunch -resultBundlePath "$FIXTURE_DIR/reimport-only-profile.xcresult" test

Execution summary for the six recovery tests: `dist/mobile-profile-lifecycle/ios-reimport-only-profile.json`.

## Replacing an applied profile with a damaged manifest

With the groups desktop export in the simulator's Documents directory, run:

    python3 scripts/check-ios-profile-vpn.py dist/mobile-profile-lifecycle --lost-manifest

The test applies A through Config, then deletes its manifest or replaces it with truncated JSON. Config recovers the list and imports B under a new ID. A simulated index write failure after B's reload receipt restores A's live payload and VPN preference. The test probes A, retries Apply and probes B. The second process resolves the saved VPN reference, checks B's ID and probes its explicit proxy route. The four current-format reports contain the HTTP statuses for B after Apply and cold launch. The upgrade cases remove the finalized transaction marker from A’s revision index. They cover A both as the first applied profile and after successfully applying a separate profile P. In the latter case the test checks that A’s VPN reference identifies P as its predecessor. With A’s manifest missing, truncated, or reduced to valid JSON containing only its ID, Config stops Apply before writing preferences and explains how to replace the VPN through Settings. The five `lost-vpn-legacy-*.json` reports record the preference write count and A’s proxy response.

## A newer VPN preference survives cancellation

Place the groups desktop export in the disposable simulator's Documents directory and set `SAKAMOTO_TEST_SIMULATOR` to its ID. Run the two-process regression with:

    python3 scripts/check-ios-profile-vpn.py dist/mobile-profile-lifecycle --newer-vpn

Config starts Apply B with A installed. During the preference reload, the fixture installs C's reference and cancels B. The first process checks that B stays pending and the controller leaves C's complete reference in place. The second process resolves the saved reference, verifies C's ID and sends a request through its explicit proxy rule. `newer-vpn-cancel.json` records extra preference writes and `newer-vpn-restart.json` records the HTTP response.

To exercise takeover after B submits its start request, run the same fixture with the following option.

    python3 scripts/check-ios-profile-vpn.py dist/mobile-profile-lifecycle --takeover-vpn

The simulator VPN manager installs C after B starts. Its session answers the controller’s confirmation ping with C’s receipt. The test checks that B stays pending and C stays connected, then sends an explicit proxy request using the live payload. A fresh process reads C’s saved VPN reference and repeats the request. The three `takeover-vpn-*.json` reports record stop requests, extra preference writes and the two HTTP responses.

For takeover during hot reload, run the same fixture with the following option.

    python3 scripts/check-ios-profile-vpn.py dist/mobile-profile-lifecycle --reload-takeover-vpn

The VPN session starts on A, applies B and drops its reload reply. The controller queries the session and receives B’s receipt. When the restore request arrives, the simulated provider installs C before evaluating its condition. The Packet Tunnel extension checks the expected receipt inside its configuration operation gate. The test compares the resulting session payload and saved reference with C, checks that B remains pending, and probes the explicit proxy rule using the live payload and the payload resolved after relaunch. The three `reload-takeover-vpn-*.json` reports record both HTTP responses, stop requests and extra preference writes.

## Native service failure during cross-profile Apply

    python3 scripts/check-ios-profile-vpn.py dist/mobile-profile-lifecycle --native-replace

Use the disposable simulator and groups export described above. Config applies B while A is running. The simulator VPN manager carries the Apply request to a Libbox command server using the extension’s live replacement transaction. The fixture substitutes loopback ingress and reserves the requested port. B passes native preflight and fails to bind after the old service closes. The test checks that B stays pending, the complete VPN reference returns to A, and the controller issues zero stop requests. It probes A’s explicit proxy rule on the same command server and repeats the request from the saved reference in another process. The script exercises both delivery and loss of the failure reply. After reply loss it first delivers a queued snapshot from before B finished, then a snapshot with the completed request ID and A’s running receipt. `native-replace-dropped-live.json` records both snapshot checks, the stop count and the live HTTP status. `native-replace-dropped-restart.json` records the cold-start response. A third case starts B successfully and drops both the reload reply and the reply to the conditional restore of A. The first ping during restoration reports `starting`. The next returns A’s receipt and the restore request ID. `native-replace-restore-live.json` records the restore command count, both observations, the stop count and the proxy response. `native-replace-restore-restart.json` records the response after relaunch. The fourth case rejects the initial `sendProviderMessage` synchronously. The controller restores A’s saved VPN reference and leaves the running instance alone. `native-replace-unsent-live.json` records the send rejection, instance continuity, stop count and proxy response. `native-replace-unsent-restart.json` records the response after relaunch. The fifth case rejects the first conditional request to restore A before delivery. The controller queries B’s running receipt again and retries within the original deadline. The provider accepts one restore command. `native-replace-retry-live.json` records the rejection, receipt check, delivered command, stop count and proxy response. `native-replace-retry-restart.json` records the cold-start response. A sixth case delivers the first restore request and the provider rejects it while busy. The controller observes the completed request ID with B’s receipt, checks the live receipt again and sends a second conditional restore. The controller permits up to three restore attempts per Apply. `native-replace-busy-live.json` records the two delivered commands, the rejection and A’s proxy response. `native-replace-busy-restart.json` records the cold-start response. In the overlap case, a competing Apply finishes between the starting and running snapshots. The provider retains completed request IDs in memory for 120 seconds, so the controller can verify its own restore completed while B remained live. `native-replace-overlap-live.json` records the competing completion, retry, stop count and proxy response. `native-replace-overlap-restart.json` records the response after relaunch.

## Upgrading VPN preferences without a profile ID

Place the desktop groups export in the disposable simulator's Documents directory. The test loads legacy VPN preferences containing configContent, with on-demand enabled and no profile ID. With the pending-Apply flag clear, the Automatic Connection form tries to replace the existing Wi-Fi rule with Any Network. The test checks the Apply prompt, the original Wi-Fi rule and zero VPN preference writes. It then checks migration, imports the export through Config, rejects one start and checks that on-demand stays disabled. A second Apply starts the selected profile. The form saves Any Network and the next process cold-starts from that saved VPN reference. Both processes probe the explicit proxy domain through the loopback VMess endpoint.

    python3 scripts/check-ios-profile-vpn.py dist/mobile-profile-lifecycle --no-id-vpn

Set `SAKAMOTO_TEST_SIMULATOR` to the booted simulator ID. The HTTP statuses are recorded in `no-id-apply.json` and `no-id-restart.json`.

## Home connection interrupted before startup

    python3 scripts/check-ios-profile-vpn.py dist/mobile-profile-lifecycle --home-write

Use the disposable simulator with the groups desktop export in Documents. Config confirms two profiles with different FINAL routes. The test selects the clean direct profile B and supplies the fixture VPN manager with the saved preference for proxy profile A. Two preference reads fail after Home writes B. Resolving the saved preference still selects A. A successful Home retry with B’s running receipt makes B eligible for automatic startup.

The fixture then reinstalls the captured A preference. Home writes its provisional B reference and the test process exits with code 86 before submitting the VPN start. The next process loads the actual reference saved by the fixture VPN manager and checks A’s profile ID. Libbox sends a request through A’s FINAL rule to the local proxy endpoint. The new process also reads the B reference captured after Home connected successfully. It passes nil start options through the provider resolver, applies B with the Packet Tunnel startup function and prepares the learning rules with the extension’s experiment runner. The test checks the learning settings and compares the revision index bytes before and after startup. Libbox probes the direct loopback endpoint through B. The same process invokes Connect VPN with the legacy widget host’s configuration for each saved reference. It passes the fixture session’s actual start arguments to the provider startup code and checks the resolved profile and local endpoint. A DEBUG observer counts payload file reads inside the widget Intent call and the Packet Tunnel resolver separately. Both paths run in the simulator test process with the fixture VPN session. Each scenario checks zero reads during the Intent and a positive count during provider resolution. It disconnects through the widget intent between starts. `home-write-result.json` records 204 for A’s proxy and 202 for B’s direct route through both Home cold startup and the widget.

## Live forwarding across an interrupted Apply

On the disposable simulator, import `groups.sakamoto` and use the Config Apply action. A separate host peer runs the provider's `ApplyLease` observer across the app's deliberate exit. One sing-box process handles SOCKS requests throughout the run. The fixture retains the exported outbound groups and FINAL setting, replaces the transport nodes with a local SOCKS relay, and uses two HTTP origins to identify the exit. The first reload must reach the direct origin. After the app exits, the same forwarder must reach the proxy origin before the second reload is acknowledged. The exiting process archives the exact reference from its simulated system VPN preferences. The next process loads that reference and checks the pending edit. The first retry reaches the direct origin, then fails to save the manifest. The host peer applies the controller's explicit rollback request. Three subsequent probes must reach the proxy origin. The final retry must reach the direct origin and commit the manifest.

    python3 scripts/check-ios-interrupted-apply.py dist/mobile-profile-lifecycle/ios-live-apply-rollback.json
    SAKAMOTO_APPLY_CASE=cross-confirmed python3 scripts/check-ios-interrupted-apply.py dist/mobile-profile-lifecycle/ios-cross-confirmed-rollback.json
    SAKAMOTO_APPLY_CASE=cross-first python3 scripts/check-ios-interrupted-apply.py dist/mobile-profile-lifecycle/ios-cross-first-rollback.json
    SAKAMOTO_APPLY_CASE=cross-first SAKAMOTO_LEASE_CONTENTION=failure python3 scripts/check-ios-interrupted-apply.py dist/mobile-profile-lifecycle/ios-interrupted-contention-failure.json
    SAKAMOTO_APPLY_CASE=cross-first SAKAMOTO_LEASE_CONTENTION=success python3 scripts/check-ios-interrupted-apply.py dist/mobile-profile-lifecycle/ios-interrupted-contention-success.json

    SAKAMOTO_LEASE_CONTENTION=double python3 scripts/check-ios-interrupted-apply.py dist/mobile-profile-lifecycle/ios-interrupted-double.json

The double interruption uses four launches and two provider leases. The app first saves A, exits with code 84 before committing B, then imports C and exits with code 85 before committing C. The surviving peer reads the confirmed profile ID and digest from C’s lease. One forwarder reaches A’s proxy endpoint again after C’s owner exits. The fourth launch resolves the saved VPN reference to A and checks the pending state of both B and C.

The host peer holds the rollback slot when the app exits. It uses the extension’s lease observer and rollback admission function. The failure fixture returns the slot with B still owning the running profile. The forwarder first reaches the direct endpoint and then reaches A’s proxy endpoint when the abandoned lease restores A. The successful fixture transfers ownership to another configuration. Requests reach a separate loopback endpoint before and after the old observer finishes. The simulator checks that B remains pending on relaunch before explicitly retrying Apply.

The cross-profile cases import the desktop package twice to obtain independent IDs. A is running with a proxy FINAL. B has either a prior confirmed direct version or a first pending Apply. Both cases preserve the candidate VPN preference reference across process exit, fail the second manifest save, then check the running proxy exit, the restored A preference and the pending B edit before retrying.

Set `SAKAMOTO_TEST_SIMULATOR` to the booted fixture simulator and place `groups.sakamoto` in the app's Documents directory before running the command. The report records the six observed exits and the expected XCTest process exit code.

## Interrupted Apply and cold reconnect

Use the disposable simulator with the groups export in Documents. Arm the exit fixture, then run three iterations in one invocation. The first process imports the package. The second exits with code 84 after provider acknowledgement and revision-index publication, during the app manifest commit. The third checks the Home payload, the automatic-start resolver and the pending edit, then retries Apply. XCTest reports an overall failure for the deliberate exit. Check the two passing executions and that the only failure is exit 84.

    touch "$DOCS/arm-apply-interrupt"
    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO -test-iterations 3 -test-repetition-relaunch-enabled YES -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testHomeAfterInterruptedApply -resultBundlePath "$FIXTURE_DIR/interrupted-apply.xcresult" test
    rm -f "$(xcrun simctl get_app_container "$SAKAMOTO_TEST_SIMULATOR" com.pidal.sakamoto data)/Documents/arm-apply-interrupt"

See `dist/mobile-profile-lifecycle/ios-interrupted-apply.json` for the before and after results and the ordinary rollback controls.

## Apply rollback after provider confirmation

With the groups export in the disposable simulator's Documents directory, Config submits a new final route to the NetworkExtension controller. A simulated provider acknowledges the new route and an injected preferences failure prevents the app from saving its applied manifest. The test compares the restored live session, saved VPN preferences and confirmed launch configuration with the prior route. A second process checks the launch configuration and the pending edit. The adjacent revision test checks preservation and pruning across subsequent saves.

    rm -f "$DOCS/apply-final-save-rollback"
    xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO -test-iterations 2 -test-repetition-relaunch-enabled YES -only-testing:SakamotoNativeTests/NativeProfileIsolationTests/testApplyManifestFailureAfterProviderReceiptAcrossRelaunch -only-testing:SakamotoNativeTests/NativeRecoveryIntegrationTests/testRollbackReferenceSurvivesSavingAnotherRevision -resultBundlePath "$FIXTURE_DIR/apply-final-save.xcresult" test

The execution counts are in `dist/mobile-profile-lifecycle/ios-apply-final-save.json`.

## iOS import, generation and restart

The iOS S3 settings test needs a disposable simulator. It installs a temporary fixture CA into that simulator's trust store and reads `groups.sakamoto` from the export directory. Set `SAKAMOTO_TEST_SIMULATOR` to its UUID and select Xcode via `DEVELOPER_DIR`.

```sh
xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests \
    -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" \
    -derivedDataPath ios/build/mobile-recovery CODE_SIGN_IDENTITY=- build-for-testing
python3 scripts/check-ios-s3-profile.py "$FIXTURE_DIR"
```

During a delayed GET the test disables sync, selects a second imported profile, or edits the active source. Each case must preserve the remote object. A normal update must send one PUT containing the fixture marker. The local endpoint records update PUT counts and object hashes in `ios-s3-settings.json`. After Keychain rejects a replacement, the endpoint verifies a request signed with the original key at the original target both immediately after dismissing the draft and again after relaunch.

Set `SAKAMOTO_TEST_SIMULATOR` to a booted isolated simulator UUID. Select the installed Xcode through `DEVELOPER_DIR` if necessary. Build and install the simulator app once to create its data container.

```sh
mise run ios:build
xcrun simctl install "$SAKAMOTO_TEST_SIMULATOR" ios/build/DerivedData/Build/Products/Debug-iphonesimulator/Sakamoto.app
DOCS="$(xcrun simctl get_app_container "$SAKAMOTO_TEST_SIMULATOR" com.pidal.sakamoto data)/Documents"
cp "$FIXTURE_DIR/groups.sakamoto" "$FIXTURE_DIR/legacy-groups.sakamoto" "$DOCS/"
rm -f "$DOCS/profile-restart"
mise exec xcodegen@2.46.0 -- xcodegen generate --spec ios/project-native-tests.yml
run_groups() {
  xcodebuild -project ios/SakamotoNativeTests.xcodeproj -scheme SakamotoNativeTests \
    -destination "platform=iOS Simulator,id=$SAKAMOTO_TEST_SIMULATOR" -parallel-testing-enabled NO \
    -derivedDataPath ios/build/mobile-recovery -resultBundlePath "$FIXTURE_DIR/$1.xcresult" \
    CODE_SIGNING_ALLOWED=YES CODE_SIGN_IDENTITY=- -only-testing:SakamotoNativeTests/NativeProfileIsolationTests "$2"
}
run_groups first test
DOCS="$(xcrun simctl get_app_container "$SAKAMOTO_TEST_SIMULATOR" com.pidal.sakamoto data)/Documents"
touch "$DOCS/profile-restart"
run_groups restart test-without-building
```

To exercise the iOS editor and the Experiment settings with a loopback VMess exit, start with a new fixture directory and the booted simulator above, then run `python3 scripts/check-ios-profile-vpn.py "$FIXTURE_DIR"`. The script starts a local proxy and HTTP server. It captures the Apply payload and the cold-start payload and submits a hostname request to the native core on each run. `final-apply.json` and `final-restart.json` record the restored FINAL tag, mode and response status. The fixture uses the command server for ingress and disables the exported management API listener on the simulator.

The network fixture runs the lifecycle tests in two fresh application processes. Its SOCKS package has a single allowed SOCKS entry and an excluded VLESS entry with chaining disabled. `socks-first.json` records the automatic group at import and after each of two generations. `socks-restart.json` records the group read by the fresh process. The group-only command checks `testExportedGroupPolicyLifecycle` in each process. The first installs an exported package, prepares the runtime configuration, regenerates twice and checks the exclusions each time. It forces manifest replacement to fail during explicit policy, group and Regional DNS settings saves, generation and import. The policy form must retain its input, stay open and display a save error. It checks the selected profile, content and settings feedback, then reopens the store and verifies the previous revision and configuration. It exercises regeneration rejection for a legacy package missing exclusion intent. Delayed URL downloads exercise cancellation and profile changes. The test also switches profiles during Apply and Reconnect and checks the calls received by the tunnel. A simulated VPN preference store drives the NetworkExtension controller through two failures. Switching profiles during the post-save preference read must cancel the start. Failing the first read after a reload save must restore the previously persisted startup payload. The same Apply test queues a start and returns three provider responses. An extension failure or a receipt for the prior rules must restore the prior cold-start payload and leave the edit pending. A receipt matching the requested rules must keep the new payload and clear the pending flag. Switching the selected profile after the system accepts a start must stop the candidate tunnel, restore the prior preferences and leave the selection pending. The fixture rejects a second provider query. A successful Apply consumes exactly one matching receipt. A running-session fixture applies the candidate rules and drops its IPC reply. The controller must restore the old live rules and cold-start preferences and leave the edit pending. If the rollback reply also fails, the session must disconnect. The second run reads the saved profile in a fresh application process. The `profile-group-lifecycle` attachments record the observed outcomes. Network behavior on physical devices remains outside this simulator measurement.
