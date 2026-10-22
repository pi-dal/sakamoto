disposition: recapture

## recapture

Native visual acceptance remains open for the shared 400 x 640-point panel on
macOS, Windows and Linux. A valid capture must show the actual native window
with the tray/status-area integration, home and settings contents, and both
appearances. macOS system capture fails in the current session; Windows/Linux
are cross-compiled locally and have no local native desktop session to inspect.
No cross-build or shared render is labeled as a native OS capture.

Content evidence is available in `dist/menubar-verification/panel-*.png` and
`shared-external-*.png`. These are shared MyGo view renders on the test host.
`macos-*.png` is the packaged application content with recorded capture provenance,
including the neutral background composite when system material is unavailable.
These establish content hierarchy and legibility, not system material, shadow,
tray positioning or dismissal behavior.

The refinement groups the primary status/action, demotes Disconnect to a
secondary action, aligns labeled traffic, tightens group/search/list spacing and
uses consistent node-row targets. The first batched inspection found a stale
list anchor after clearing a long-name search; filter/group changes now reset
scroll. External ownership uses Core online / Managed externally instead of
claiming an API connection means this client owns a TUN. Long labels retain full
accessible names and action labels.

Native IME, screen readers, taskbar/Wayland/multi-monitor/full-screen positioning,
outside-click/Escape and login startup need interactive acceptance on each OS.
The packaged demo reproduces fixture views without private data using
`mise run gui:demo`; `gui:verify` records lifecycle and capture provenance.
Windows/Linux CI jobs now execute native API/view tests and packaging; their
remote outcome is not asserted by this local work.

Impeccable's reviewer/documenter fallback roles ran inline because this harness
has no available subagent interface. The scope and preserved native utility
identity are recorded in `docs/menubar-surface.md` and `DESIGN.md`.
