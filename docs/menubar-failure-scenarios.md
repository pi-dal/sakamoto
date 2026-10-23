# Menu bar implementation failure scenarios

Written before implementation and integration tests.

- A second action in one controller must not issue Connect when a lifecycle operation is already active.
- A connect or disconnect against a custom runtime must never use the user's default supervisor socket, including staged credential handling.
- Opening or closing the GUI must not implicitly connect or disconnect the tunnel.
- Missing configuration, unavailable supervisor, disconnected tunnel and unavailable native API must remain distinct.
- Lost supervisor or API streams must clear stale actionable state and retry; an old successful probe must not survive disconnect/reconnect.
- A node selection must preserve the automatic-group to ManualPick to MainProxy sequence; missing targets and unavailable ManualPick members must fail before changing selection.
- A routing mode must be supported by the live API and verified after mutation.
- A URL-test acknowledgement must not appear as a successful latency measurement. Pending tests must resolve from a fresh snapshot or expire as failed.
- Hiding the panel must cancel traffic subscriptions without canceling application-owned operations. Closing the controller must cancel subscriptions and must never disconnect the tunnel.
- The UI must not expose API credentials, subscription URLs or server addresses in notices, screenshots or receipts.
- Demo/verification modes must remain isolated from user configuration and clearly identify synthetic state.
- The packaged app must have LSUIElement, retain its status item without a visible panel, and avoid duplicate instances.
- Build outputs and verification captures must stay under ignored dist paths.
- Cross-platform entrypoints must compile without macOS-only service or Objective-C dependencies.
- An external-core client must observe and control the authenticated API without issuing supervisor connect/disconnect commands; lifecycle controls explain that limitation.
- Linux's AppIndicator needs a persistent menu because it reports neither tray clicks nor icon coordinates; lack of a tray must leave an accessible window.
- Windows/Linux must not offer a macOS Terminal action or use a monochrome black tray icon on a dark taskbar.
- Search, selected rows, test feedback and settings must remain legible in both themes, with long Unicode names and empty results.
- Reopening the panel must preserve search/group choice while creating new engine-owned list state.

## Minimal residency and repair failure scenarios

Written before implementation and end-to-end verification.

- A hidden or never-opened panel must perform no status/config polling, conflict subprocess checks, API subscriptions or network probes.
- Hiding must cancel in-flight observations, reject late stream results and retain only cached, explicitly stale display state. Reopening must immediately refresh and create fresh subscriptions.
- An explicitly requested bounded repair may finish after the panel closes; neither hiding nor quitting may disconnect the independent tunnel.
- Check health is read-only. Ordinary repair must retain a healthy process, recover a stopped core through the existing supervisor policy and respect maintenance holds.
- Reconnect requires an explicit in-panel confirmation and must not be issued by cancel, opening the repair page, or a repeated click while another operation is active.
- A watcher acknowledgement of queued/busy/cooldown/manual/disabled/maintenance is not proof of restored connectivity. Unknown replies must fail safely.
- Repair failures, API credentials and private server errors must never appear verbatim in the GUI or receipts.
- External-core mode can diagnose its API but must not repair/restart a service or contact macOS supervisor/watcher sockets.
- An unavailable supervisor must offer a deliberate service-recovery terminal action using the bundled CLI; opening that terminal must be explicit and paths must remain shell-quoted.
- The idle tray must not offer stale Connect/Disconnect actions; Linux must remain hidden at startup when a tray is available.

## Signed desktop release failure scenarios

- A stapled ticket must survive final ZIP creation and extraction; source-bundle validation alone is insufficient.
- Apple CloudKit transport failures during stapling/validation must retry a bounded number of times, then fail closed. A retry must never restaple an extracted release ZIP or skip signature/Gatekeeper checks.
- Failed signature/stapler/Gatekeeper checks must retain their tool output and public ZIP diagnostics. Never restaple the extracted archive merely to hide a packaging failure.

Written before release implementation and verification.

- A clean Go module cache must download license-bearing modules before reading their source directories; metadata lookup alone may omit `Dir`.
- A release must fail before building when its tag differs from the source version or signing credentials are missing.
- Apple Development, Apple Distribution and ad-hoc identities must never be accepted as Developer ID release identities.
- Both the app and its bundled CLI/rescue helper must have a secure timestamp, hardened runtime and the expected signing team.
- A rejected, timed-out or unavailable notarization must not publish an archive or update Homebrew.
- A release archive must contain the stapled app; hashing a pre-staple ZIP is invalid.
- Both macOS architectures must have their own verified archive, checksum and notarization receipt.
- Release metadata must preserve signing/notarization evidence when combining platform packages.
- The tap must download the exact public release archives and verify their hashes before generating a cask.
- The desktop cask must coexist with the CLI formula and must not install services, connect a tunnel or delete user configuration.
- Temporary CI signing keys and keychain search-list changes must be cleaned up on failure as well as success.
- Pull-request CI must build without release secrets; only the dedicated desktop release workflow can consume signing credentials.
- A repeatable local verification must reject an ad-hoc archive in release mode and record a verification receipt under ignored dist paths.

Verification uses real packaged archives and macOS codesign, stapler and Gatekeeper tools. Local ad-hoc verification exercises the packaging/manifest/cask path, plus rejection by the release policy. The release workflow performs the positive Developer ID/notarization checks and the tap CI installs the published cask without opening it.

Verification uses a real Unix supervisor fixture and a real local gRPC core fixture, with no privileged service or TUN. The repeatable receipt records lifecycle, manual selection, mode confirmation, fresh latency, visibility and recovery outcomes. Native UI verification uses a labeled demo backend with the same controller-to-view snapshot contract.
