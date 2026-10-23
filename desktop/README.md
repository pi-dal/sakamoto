# Shared desktop client

Sakamoto uses one native Go view and controller across macOS, Windows and Linux.
MyGo v0.6.2 owns the OS shell; Onevra v0.2026.14 supplies themed controls and
Lucide icons. This separate Go module leaves the CLI/mobile dependency graph
unchanged. No JavaScript frontend is used.

## Platform behavior

| Capability | macOS | Windows | Linux |
| --- | --- | --- | --- |
| Shared panel, themes, search, node selection, latency, traffic | Yes | Yes | Yes |
| Routing mode through the authenticated native API | Yes | Yes | Yes |
| Resident entry point | Menu bar status item | Notification area | AppIndicator menu |
| Panel placement | Native NSPopover | Taskbar flyout | Desktop/compositor-managed utility panel |
| Tunnel connect/disconnect | Existing sakamoto supervisor | External core service | External core service |
| Advanced editing | Bundled TUI and config folder | Config folder | Config folder |
| Launch at login | Opt-in OS integration | Opt-in OS integration | Opt-in desktop autostart |

The shared UI does not make privileged services interchangeable. Windows/Linux
currently use **external-core mode**: start/configure a compatible core separately,
then supply its sakamoto sidecar YAML with the native API URL and secret. The
client can select nodes, confirm routing modes, request latency tests and observe
traffic. It cannot start/stop that service or take over DNS. It displays **Core
online** and **Managed externally**, and omits a nonfunctional connection button.
API availability is never presented as proof that the client owns a TUN.

macOS keeps its existing supervisor and staged-key connect workflow. It can also
use `--external-core` explicitly. Neither mode connects on application launch.
Closing a tray panel keeps the client resident; quitting preserves the independent
service. If no tray library is available, the app opens a regular window and
closing that window quits the client.

Linux AppIndicator reports neither normal clicks nor icon coordinates. Its
persistent menu offers Open Sakamoto, Settings and Quit. The utility panel follows
the compositor's positioning rather than pretending it can anchor to a tray icon.
A Linux desktop needs GTK 3, Cairo, Pango and a graphical session. Ayatana
AppIndicator/AppIndicator plus a compatible status area enables residency;
otherwise the normal-window fallback is used. Native Go views do not require a
WebKit frontend. Windows uses native system drawing/text APIs and a colored tray
icon. macOS uses a monochrome template icon tinted by the system.

## Build and run

Use the repository's mise toolchain (Go 1.27.1, Python 3.14). macOS bundles require
macOS 12+ and Xcode command-line tools. The other targets build without CGO;
building a Windows/Linux binary on a Mac does not validate its native runtime.

```sh
mise run gui:build     # package the current host's OS and architecture
mise run gui:demo      # open labeled fixtures; no real config or service changes
mise run gui:check     # isolated API/controller and view workflows
mise run gui:verify    # packaged native demo lifecycle + content captures
```

To run the built live client:

```sh
mise exec -- python scripts/run-desktop.py
mise exec -- python scripts/run-desktop.py --config /path/to/sakamoto.yaml
```

Exit a demo before starting the live client. Demo data never reads the live
configuration or changes the login preference. Advanced CLI setup on macOS
remains documented in the root README. Windows/Linux require an existing core
service and matching native API configuration; no administrative installer or
system DNS adapter is supplied by this preview.

To package explicit targets:

```sh
# On a Mac, package all six OS/architecture targets:
mise exec -- python scripts/build-desktop.py --all
# Cross-build portable Windows/Linux bundles from any supported host:
mise exec -- python scripts/build-desktop.py --target windows-amd64 --target linux-amd64
# Optional race checks on a host with a supported native C compiler:
mise exec -- python scripts/check-desktop.py --race
```

Outputs are under `dist/desktop/`:

- `darwin-<arch>/Sakamoto.app` and a ZIP, with the bundled CLI/setup resources.
- `windows-<arch>/Sakamoto/bin/sakamoto-desktop.exe` and a portable ZIP; no console window.
- `linux-<arch>/Sakamoto/bin/sakamoto-desktop` and a portable tarball.
- `SHA256SUMS` and `release-metadata.json`, including target and lifecycle capability.

The portable packages include licenses and icons resized from the owner-provided
`assets/app-icon.png`. Linux also includes an XDG desktop entry and a 256-pixel
hicolor icon. To use that launcher, install the binary on PATH and the `share/`
contents under a user-selected XDG prefix. The Windows ICO is shipped as a
resource; this preview does not embed it into the executable's file icon.

Local macOS builds use an ad-hoc signature; Windows/Linux artifacts are unsigned.
The [desktop release workflow](../docs/desktop-release.md) adds Developer ID
signing, Apple notarization, stapling and a separate `sakamoto-desktop` Homebrew
cask. Release signing credentials are configured for the expected Apple team.
`scripts/build-menubar.py` remains the macOS packaging entry point and accepts
`--sign-identity`, `--team-id`, `--notarize` and `--notary-profile`. The shared
packaging entry point forwards these options and preserves signing evidence.
Signed macOS bundles include both the CLI and the offline rescue helper.

## Panel behavior

The shared 400 x 640-point panel keeps connection/API state and its action at
the top. A supported routing segment, active route and aligned traffic values
precede the searchable node list. The node group is explicit; a check and bold
name indicate selection in that group. Selection is independent of latency or
verified network reachability. Long names truncate visually while accessible
action labels retain their full value. Changing the group/filter resets list
scroll, avoiding a stale partial row after a search is cleared.

Selecting a leaf in an automatic group first selects it in `ManualPick`, then
routes `MainProxy` to `ManualPick`, preserving watcher behavior. Mode changes are
confirmed by reading the API back. Hiding a panel cancels its traffic stream;
application-owned observations and actions continue. List view state is recreated
for every new popover engine so a reopened panel cannot reuse an invalid engine.

Settings includes System/Light/Dark appearance, opt-in login startup and a config
folder action. The TUI entry appears where a native CLI bundle is available.
Configuration remains user-owned; appearance is a separate application preference.

## Verification and limits

`gui:check` uses temporary config, a Unix supervisor fixture on Unix hosts, a local
native gRPC service and an HTTP probe fixture. It covers duplicate connect
rejection, ordered manual selection, API-confirmed/refused modes, redacted errors,
fresh latency, hidden-panel traffic unsubscription and quit without disconnect.
The external-core workflow works without a supervisor socket and verifies that
connect/disconnect cannot cross that capability boundary. Its generic fixture
also compiles on Windows.

The shared view workflow drives pointer and keyboard events, settings, Unicode
search, long names, node selection, test feedback, empty results and both themes.
CI adds Windows/Linux jobs to execute their API/view tests and native builds.
Those jobs have been configured here; their remote results are not asserted by
a local cross-build.

Artifacts under `dist/menubar-verification/` include:

- `controller-e2e.json`, `external-core-e2e.json`: service/API boundaries.
- `panel-workflow.json`, `panel-*.png`: shared event workflow and renders.
- `shared-external-*.png`: Unicode/long-name and external ownership renders.
- `native-app.json`, `macos-*.png` or `desktop-*.png`: packaged demo lifecycle/capture provenance.

Native verification always requires `--demo`. macOS tries system window capture
first, then records a content-only fallback when the current session cannot
capture the screen. Fallbacks are composited over the theme background and do
not prove the system frame, material, shadow, tray placement or dismissal gestures.
A short hidden-demo CPU/peak-RSS sample is not a live-runtime benchmark.

Windows/Linux are cross-compiled locally, with native runtime acceptance pending.
VoiceOver/screen readers, native IME, taskbar/Wayland/multi-monitor placement,
outside-click/Escape and login startup require interactive acceptance on each OS.
The native visual review remains **recapture** for that evidence. See
[design evidence](../DESIGN.md), [review](../docs/menubar-design-review.md),
[failure scenarios](../docs/menubar-failure-scenarios.md) and
[original research](../docs/menubar-gui-research.md).
