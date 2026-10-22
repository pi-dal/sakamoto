---
name: Sakamoto desktop
description: Shared macOS, Windows and Linux tray controls
colors:
  light-background: "#ffffff"
  light-surface: "#f4f4f5"
  light-text: "#18181b"
  light-muted: "#71717a"
  light-accent: "#2563eb"
  dark-background: "#18181b"
  dark-surface: "#27272a"
  dark-text: "#f4f4f5"
  dark-muted: "#a1a1aa"
  dark-accent: "#3b82f6"
typography:
  title:
    fontFamily: system-ui
    fontSize: 17pt
    fontWeight: 700
  status:
    fontFamily: system-ui
    fontSize: 14pt
    fontWeight: 700
  body:
    fontFamily: system-ui
  caption:
    fontFamily: system-ui
    fontSize: 12pt
rounded:
  control: 6pt
spacing:
  compact: 6pt
  section: 14pt
  inset: 20pt
components:
  connection-primary-light:
    backgroundColor: "{colors.light-accent}"
    textColor: "{colors.light-background}"
    rounded: "{rounded.control}"
  connection-primary-dark:
    backgroundColor: "{colors.dark-accent}"
    textColor: "{colors.light-background}"
    rounded: "{rounded.control}"
---

# Design System: Sakamoto desktop

## Overview

The desktop client is a compact native connection utility. A shared MyGo view,
system typography, Onevra controls and Lucide icons span macOS, Windows and Linux.
macOS uses AppKit's NSPopover, Windows a notification-area flyout, and Linux an
AppIndicator menu with a compositor-managed utility panel. Platform behavior is
adapted without introducing separate visual systems. This document describes
the desktop surface and does not prescribe changes to the TUI or mobile clients.

Observed behavior and readable actions lead. Demo data carries a visible label.
Status text distinguishes a running tunnel from a reachable network; latency
measurements are separate from connection status. This is an operational
hierarchy, with no display typography or decorative imagery.

## Colors

Light and dark appearances use MyGo's released theme tokens. The frontmatter
records its fallback palette, also used by the view tester; the native host can
resolve system colors and accent when rendering the real popover. The blue
accent identifies the main connection action and selected controls. Neutral text and
surfaces carry the rest of the panel. Success, warning and failure icons use
the library's semantic colors, accompanied by ordinary readable status text.
Small demo labels use muted text to retain contrast in both appearances.

On macOS the popover supplies its material through AppKit. The other platforms
use their native window shell around the same opaque panel content. Content
renders use the theme background for inspection; they do not imitate system
material or prove native placement. System appearance follows the host.

## Typography

Use system sans for the utility's short titles, status, labels and data. The
panel title uses the title token; the observed connection state uses the status
token. Controls follow the host's default body size (13 on macOS, 14 elsewhere),
while node labels use 13 and captions use 11–12. Preserve full node labels in accessible names when visual
labels truncate. No separate display face or decorative monospace is used.

## Layout

The shared desktop panel is 400 × 640 points. Its body uses the inset token and
section gaps. The order is title/settings, observed connection/API status with
its supported action, routing mode, active route, compact labeled traffic and a
searchable scrolling node list. Settings replaces the body with a back action,
appearance, login launch and configuration/service details. Only the list
scrolls; connection controls remain visible. The list groups its header, search
and rows with an 8-point gap; node rows use 44-point targets. Clearing a filter
or changing group resets scroll so an old anchor cannot leave an orphaned line.

Keep related icon/text pairs compact. A node row has a selection action, a
separate latency action and a secondary measurement line. Group selection and
node selection are different operations. List UI state belongs to one popover
content engine and is recreated when the panel reopens.

## Elevation & Depth

Each platform shell owns placement and shadow: AppKit popover on macOS, the
native flyout on Windows and a utility window on Linux. Controls use the
library's subtle surface fills and borders. No decorative glass or extra
application-level shadow is introduced. Content snapshots cannot verify the
system frame; retain that limitation in review evidence.

## Shapes

Controls follow the library's gently rounded control radius. Lucide icons share
one stroke vocabulary. The tray icon is an original power glyph: monochrome/template on macOS and colored
on Windows/Linux for visibility. The app bundle icon resizes the existing
owner-provided artwork; it is separate from the terminal portrait.

## Components

The connection action sits beside observed service status. Connect uses the
accent; Disconnect is a secondary action so a healthy connection does not read
as a large call to disconnect. The label follows observed state and current
operation. External-core mode shows Core online and Managed externally, with no
unsupported lifecycle button. Errors use readable semantic text rather than a
muted success-like footnote. Rule/Global/Direct use a labeled segmented control
and only show supported native API modes.

Search uses a labeled native text input, a clear action and a visible empty
result. Nodes show a readable name and latency status, with accessible labels
for icon actions. Appearance and node group choices use labeled select controls.
Settings and the home panel share the same title, spacing and footer vocabulary.

## Do's and Don'ts

- Do preserve system light/dark appearance and the native popover frame.
- Do pair semantic status colors with readable words.
- Do label fixtures as demo data and keep latency distinct from reachability.
- Do keep advanced editing in the TUI rather than expanding this small panel.
- Don't claim a successful mode change before the native API confirms it.
- Don't imply that closing or quitting the client stops the independent tunnel.
- Don't infer system material, VoiceOver or multi-monitor correctness from content renders.

Evidence: `desktop/internal/menubar/panel.go`, `app.go`, the platform adapters,
MyGo v0.6.2 `ui/theme.go`, and `dist/menubar-verification/`. The current review
covers shared content and tested behaviors. Impeccable's reviewer/documenter
roles run inline because this harness has no available subagent interface.
Native visual approval remains **recapture** for each platform until interactive
system-window evidence is available. No native runtime acceptance is inferred
from a cross-build.
