# Shared desktop surface

Mode: Operate. Scope: one resident desktop client for macOS, Windows and Linux,
with a shared view/controller and native shell adapters. The user asked to unify
these desktop platforms and polish the existing menu bar UI. This is refinement
of the established native utility, not a replacement visual identity.

## Direction contract

THESIS: A compact connection control center whose state explains both the next
available action and which service owns the connection.

OWN-WORLD: System typography, neutral MyGo theme surfaces, a restrained accent,
Lucide icons and labeled native controls. macOS uses AppKit NSPopover material;
Windows uses a notification-area flyout; Linux uses an AppIndicator menu and a
compositor-managed utility panel. System appearance remains the default.

STORY: Read observed connection/API state, connect or disconnect when supported,
choose a routing mode, then inspect or change a node. An external core exposes
its API controls without suggesting this client owns its privileged lifecycle.

FIRST VIEWPORT: 400 x 640 points. Product and settings at top; state and a compact
supported connection action share a row. Routing segments, active route and
aligned traffic precede a tighter node-group/search/list region. The selected
node has both a check and a bold name. Settings replaces the panel body with a
back action. Long labels truncate visually while action names stay complete.

FORM: Keep the released MyGo tray/flyout interaction. The shared list and row
system adapts to native host typography. Clearing a filter resets its scroll;
reopening creates new engine-owned list state. A missing tray falls back to a
reachable ordinary window. Closing a tray panel preserves operations and the
independent service.

FINISH: Record content render/event evidence for light/dark and long Unicode
names, real local API/supervisor workflow receipts, per-target build metadata and
accurate OS ownership. Native visual review remains recapture until system-window
captures and interactive OS acceptance exist; a cross-build never implies them.
