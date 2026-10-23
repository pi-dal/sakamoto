# Configuration repair

## Investigation

Read-only inspection on 2026-10-23 found terminal CLI 0.2.16, a running supervisor using the legacy protocol (`info` is unknown), and no `recovery/last-good.json`. The current source's `repair --check` verified the existing native API and protected DNS while retaining the same core PID. The reported GUI failure was not reproduced against that live process.

The code review identified three separate gaps. Installing a new GUI does not replace an already-running supervisor, so older services cannot provide configuration rollback. The GUI previously reduced several failures to a generic repair error. The TUI generator also wrote into active rule files before validating its result. API credentials or endpoints that differed between the sidecar and core configuration could leave the GUI unable to connect while a core-only probe appeared healthy.

Release 0.2.21 adds explicit configuration recovery, public failure categories, a controlled background-service update, and generation validation before active files change. These changes address those gaps; they do not establish the original transient failure's cause.

## Commands and GUI

Use the CLI from 0.2.21 or newer. The signed macOS app bundles the matching CLI; a separately installed Homebrew CLI can have another version.

```sh
sakamoto repair --check
sakamoto repair
sakamoto repair --fallback --restart
sakamoto repair --regenerate --restart
```

- Ordinary repair retains a healthy process and can start a stopped core. It verifies the native API and configured DNS.
- Fallback restores the complete verified working runtime, including its matching engine, API settings, learned state and local assets. It can recover corrupt current JSON or YAML without reading those as valid configuration.
- Regeneration reads saved settings and configured sources, requires supported proxy nodes, retains the API identity and DNS policy, builds a private candidate, and runs the installed engine's native check. It neither creates replacement credentials nor substitutes a direct-only profile.
- Replacing a running configuration requires `--restart`. Omit it for a stopped core. Failed preparation leaves the existing process and active files intact.

On macOS, **Check & repair** offers **Restore working configuration…** and **Regenerate configuration…** with separate confirmation. It offers **Update background service…** when the running supervisor lacks the capability. No usable working copy means fallback is unavailable; regeneration still requires valid saved settings and accessible sources. Windows/Linux external cores retain their service manager's ownership.

For a legacy supervisor:

```sh
sakamoto service-update --check
sakamoto service-update --reconnect
```

The first command only inspects the identified installation. The second requires a local terminal, explicit confirmation and administrator authorization. It prepares independent offline rescue before disconnecting, verifies the old core's exit and restoration of captured network settings, retains both service executables outside Homebrew and the app bundle, then replaces only the canonical supervisor job. Failed bootstrap restores the retained service when possible. An incomplete rollback keeps maintenance held and reports failure. A disconnected VPN remains disconnected; an approved live reconnect must pass fresh API/DNS checks.

This operation does not migrate unidentified legacy jobs or create a missing installation. Use the documented setup/migration procedure for those cases. Ordinary `repair` never silently replaces a service executable or plist.

A configuration becomes a working fallback only after the unchanged runtime meets the existing five-minute stability and readiness requirements. Successful parsing, `check`, regeneration or service installation alone does not create that receipt.

## Recovery contract

Configuration replacement uses the existing learning transaction boundary and checks input fingerprints before installation. New edits, cancellation, maintenance and manual stop supersede stale work. A reconnect waits for the owned predecessor to release its resources; another connect cannot bypass an in-progress repair. Rejected files remain private.

An activation failure can use one complete working fallback. If that fails, automatic attempts stop. A healthy retained data plane is not killed merely because a probe or unrelated website fails. The GUI accepts only approved states, actions and problem codes; raw engine diagnostics, paths, URLs and credentials never enter its repair view model.

TUI import/regeneration also builds and checks an independent directory before staging files. Checked rule assets remain available after installation. An input edit during preparation prevents a stale install. TUI generation does not initiate a VPN connection.

## Failure scenarios defined before implementation

| Fault | Required result |
| --- | --- |
| Healthy existing core | Ordinary repair retains PID; no configuration writes |
| Legacy supervisor | Ordinary repair remains compatible; advanced repair refuses before disconnect and offers service update |
| Corrupt JSON, valid retained bundle | Fallback validates and restores settings, assets and compatible engine, then verifies the new API |
| Corrupt sidecar, valid retained bundle | Fallback remains available without loading the broken sidecar |
| Missing/corrupt retained bundle | Fallback fails before stopping an existing core |
| Live core without reconnect consent | Configuration repair refuses before replacing files |
| Missing/corrupt sidecar or missing API identity | Regeneration fails without synthesizing credentials or routing policy |
| Invalid source, failed fetch or zero supported nodes | Generation fails; active files and PID remain intact |
| Candidate rejected by native check | Active runtime stays intact; unused candidate is removed |
| Custom API port | Generated sidecar and API agree; verification reaches the new process |
| API identities differ | Health reports configuration mismatch instead of success |
| Pending API rotation | Configuration recovery refuses to consume or silently replace it |
| Local rule/TLS inputs | Config refers to retained checked assets |
| Inputs edited during preparation | Reject stale installation; preserve newer edits |
| Manual stop during preparation or reconnect grace | Cancel work; prevent late activation or orphaned owned process |
| Generated runtime exits before readiness | One working fallback, then verification; block if it also fails |
| Concurrent actions | Repair serializes; connect cannot bypass owned shutdown |
| Unknown/private error content | Public UI contains only approved text |
| Service bootstrap fails | Retained executable/plist restore; no implicit VPN start |
| Foreign/unidentified launchd job | Refuse before changing it |
| Hidden or closed panel | Pending confirmation disappears; submitted action remains bounded |

## Verification and limits

Run `mise run go:check`, `mise run go:recovery`, `mise run gui:check` and `mise run ci:check`. Recovery CI includes the TUI generation scenario. JSON receipts are written under `dist/recovery-e2e`; input-driven UI captures are under `dist/menubar-verification`.

Core scenarios use isolated runtime directories, real Unix sockets, real native checks and real API child processes with TUN/network takeover removed by the launcher. Service-update scenarios execute isolated OS-command subprocess fixtures and cover successful replacement, retained rollback, rescue staging and foreign-runtime rejection. They do not exercise privileged launchd replacement on the owner's live network. The owner installation was inspected read-only.

Native checks establish configuration/schema validity. API and protected-DNS probes establish their specific readiness; they do not prove reachability of every proxy destination or website. Failed source access cannot regenerate missing subscription data. A working retained bundle provides the offline path.

## Research references

- [Native configuration reference](https://sing-box.sagernet.org/configuration/) and [migration guide](https://sing-box.sagernet.org/migration/): generated configurations depend on engine schema/version. Fallback therefore retains the compatible engine and assets; regeneration checks against the intended installed engine.
- [NetworkManager checkpoint API](https://networkmanager.dev/docs/api/1.52.0/gdbus-org.freedesktop.NetworkManager.Checkpoint.html): bounded rollback and explicit commit are an established approach to network changes. Sakamoto applies that principle through private preparation, readiness verification and a single retained-runtime rollback; it does not use NetworkManager on macOS.
- [Sakamoto offline recovery contract](stability-recovery.md): independent rescue, maintenance, crash budgets, complete working bundles and ownership boundaries remain authoritative.
