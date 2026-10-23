disposition: recapture

## recapture

Native visual acceptance remains open for the shared 400 x 640-point panel on
macOS, Windows and Linux. A valid capture must show the actual native window
with the tray/status-area integration, home and settings contents, and both
appearances. macOS system capture fails in the current session; Windows/Linux
are cross-compiled locally and have no local native desktop session to inspect.
No cross-build or shared render is labeled as a native OS capture.

Content evidence is available in `dist/menubar-verification/panel-*.png`,
`settings-*.png`, `repair-*.png` and `shared-external-*.png`. These are shared MyGo
view renders on the test host. `macos-*.png` is the packaged application content
with recorded capture provenance, including an opaque Radix Slate background
when system capture is unavailable. These establish content hierarchy and
legibility, not the system frame, shadow, tray positioning or dismissal behavior.

The refinement groups the primary status/action, demotes Disconnect to a
secondary action, aligns labeled traffic, tightens group/search/list spacing and
uses consistent node-row targets. The first batched inspection found a stale
list anchor after clearing a long-name search; filter/group changes now reset
scroll. External ownership uses Core online / Managed externally instead of
claiming an API connection means this client owns a TUN. Long labels retain full
accessible names and action labels.

The recovery refinement preserves this visual vocabulary: a footer/tray entry,
back navigation, timestamped public diagnosis, adjacent check/repair controls,
separate confirmed reconnect, and readable watcher acknowledgements. Light/dark
repair content renders and pointer-driven confirmation/cancel evidence live in
`repair-*.png` and `repair-view-e2e.json`. The recovery state uses section dividers
without adding cards or another window. Duplicate result text is suppressed in
the footer when it is already present in the page. Idle/protocol evidence is
separate in `resident-repair-e2e.json`; content renders do not establish idle CPU
or privileged service success.

The Radix refinement uses Slate scale roles for neutral surfaces, Indigo for
action/selection/focus and semantic step-11 state colors. Content has three text
sizes (16/13/12), 0.5-point borders/focus outlines, concentric control/popup corners
and immediate state changes. It removes middle-dot separators, routine footer
success messages, repeated headings and explanatory paragraphs. The remaining
copy preserves errors, partial reachability, external ownership, actual repair
results, pending route tests and interruption consequences at confirmation.

`controls-view-e2e.json` and settings/menu renders cover pointer/keyboard routing,
group choice, appearance persistence, Escape-to-clear, disabled demo startup,
explicit live startup callbacks, exact Radix background pixels on a material
host and no resting animation frames. Popup controls use MyGo bases so styling
does not replace native text input, focus and accessibility behavior. Radix's
license ships in all six platform packages.

Native IME, screen readers, taskbar/Wayland/multi-monitor/full-screen positioning,
outside-click/Escape and login startup need interactive acceptance on each OS.
The packaged demo reproduces fixture views without private data using
`mise run gui:demo`; `gui:verify` records lifecycle and capture provenance.
Windows/Linux CI jobs now execute native API/view tests and packaging; their
remote outcome is not asserted by this local work.

Impeccable's reviewer/documenter fallback roles ran inline because this harness
has no available subagent interface. The scope and preserved native utility
identity are recorded in `docs/menubar-surface.md` and `DESIGN.md`.

The Connections extension keeps the same content vocabulary: a text action in the
home header, back navigation, one search input, active count and 60-point virtual
rows. Targets/applications/outbounds align left; transfer totals align right.
The Configuration page extends the same vocabulary with grouped labels, native
switches, selects and bordered text inputs. Save is the only primary configuration
action; regenerate and reconnect remain in Check & repair. No introduction or new
card treatment was added. `connections-light.png`, `connections-dark.png` and
packaged `macos-connections-*.png` establish content
hierarchy; `connections-e2e.json` and `connections-native-e2e.json` separately
verify demand-scoped tracking and preservation of the actual data stream. The
content review is recorded in `dist/menubar-verification/connections-design-review.md`
with disposition `ship` at content scope; the native acceptance limits above
remain open. `configuration-light.png` and `configuration-dark.png` cover the
new editor. This review also used the inline fallback.
