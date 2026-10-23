# Shared desktop surface

Mode: Operate. Scope: one resident desktop client for macOS, Windows and Linux,
with a shared view/controller and native shell adapters. The user asked to unify
these desktop platforms and polish the existing menu bar UI. This is refinement
of the established native utility, not a replacement visual identity.

## Direction contract

THESIS: A compact connection control center whose state explains both the next
available action and which service owns the connection.

OWN-WORLD: System typography, Radix Slate surfaces, a restrained Indigo accent,
Lucide icons and labeled MyGo base controls. The content is opaque in every host;
AppKit owns the NSPopover frame, Windows the notification-area flyout and Linux
an AppIndicator menu plus compositor-managed utility panel. System appearance
remains the default. Use only 16/13/12-point text, 0.5-point authored borders,
6-point controls with concentric tracks/popups and immediate state changes.
No decorative eyebrows, content shadows, cards or middle-dot separators.

STORY: Read observed connection/API state, connect or disconnect when supported,
choose a routing mode, then inspect or change a node. An external core exposes
its API controls without suggesting this client owns its privileged lifecycle.

FIRST VIEWPORT: 400 x 640 points. Product and settings at top; state and a compact
supported connection action share a row. Routing segments, active route and
aligned traffic precede a tighter node-group/search/list region. The selected
node has both a check and a bold name. Settings replaces the panel body with a
back action. Check & repair also replaces the body, with a read-only check,
ordinary repair, explicitly confirmed reconnect and watcher acknowledgement.
Opening this page does not initiate a repair. Configuration replacement uses
separate restore/regenerate confirmation; a legacy service shows its bundled
update terminal action in that section. An unavailable supervisor offers
a deliberate bundled repair terminal action; external cores only expose API
checks and their configuration folder. Long labels truncate visually while
action names stay complete. The home header's Connections action replaces the
body with search, an active count and a virtual list. Each 60-point row aligns
target/application/outbound on the left and per-connection totals on the right.
This is read-only inspection, with no explanatory introduction or extra cards.
Connection events subscribe only while this page is visible. Navigating away or
hiding releases its data and bindings; reopening starts from a fresh snapshot.

FORM: Keep the released MyGo tray/flyout interaction. The shared list and row
system adapts to native host typography. Clearing a filter resets its scroll;
reopening creates new engine-owned list state. A missing tray falls back to a
reachable ordinary window. Closing a tray panel preserves operations and the
independent service. Hiding cancels all GUI observation, including polling,
subscriptions, conflict subprocess checks and network probes. Opening refreshes
state immediately. Tray menus contain navigation rather than stale lifecycle
actions. A requested bounded repair can finish after hiding.

FINISH: Record content render/event evidence for light/dark and long Unicode
names, real local API/supervisor workflow receipts, per-target build metadata and
accurate OS ownership. Native visual review remains recapture until system-window
captures and interactive OS acceptance exist; a cross-build never implies them.
