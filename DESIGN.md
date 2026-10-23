---
name: Sakamoto desktop
description: Shared macOS, Windows and Linux tray controls
colors:
  light-background: "#fcfcfd"
  light-surface: "#f0f0f3"
  light-surface-hover: "#e8e8ec"
  light-surface-pressed: "#e0e1e6"
  light-popup: "#f9f9fb"
  light-separator: "#d9d9e0"
  light-control-border: "#cdced6"
  light-control-border-hover: "#b9bbc6"
  light-text: "#1c2024"
  light-muted: "#60646c"
  light-accent: "#3e63dd"
  light-accent-hover-pressed: "#3358d4"
  light-selected-background: "#edf2fe"
  light-selected-text: "#3a5bc7"
  light-selection: "#d2deff"
  light-danger: "#ce2c31"
  light-warning: "#ab6400"
  light-success: "#218358"
  light-scrollbar: "#8b8d98"
  dark-background: "#111113"
  dark-surface: "#212225"
  dark-surface-hover: "#272a2d"
  dark-surface-pressed: "#2e3135"
  dark-popup: "#18191b"
  dark-separator: "#363a3f"
  dark-control-border: "#43484e"
  dark-control-border-hover: "#5a6169"
  dark-text: "#edeef0"
  dark-muted: "#b0b4ba"
  dark-accent: "#3e63dd"
  dark-accent-hover-pressed: "#3358d4"
  dark-selected-background: "#182449"
  dark-selected-text: "#9eb1ff"
  dark-selection: "#253974"
  dark-danger: "#ff9592"
  dark-warning: "#ffca16"
  dark-success: "#3dd68c"
  dark-scrollbar: "#696e77"
  solid-accent-text: "#ffffff"
typography:
  title:
    fontFamily: system-ui
    fontSize: 16px
    fontWeight: 700
  body:
    fontFamily: system-ui
    fontSize: 13px
  caption:
    fontFamily: system-ui
    fontSize: 12px
rounded:
  control: 6px
  segmented-track: 8px
  popup: 10px
spacing:
  compact: 8px
  section: 14px
  inset: 20px
components:
  connection-primary-light:
    backgroundColor: "{colors.light-accent}"
    textColor: "#ffffff"
    rounded: "{rounded.control}"
  connection-primary-dark:
    backgroundColor: "{colors.dark-accent}"
    textColor: "#ffffff"
    rounded: "{rounded.control}"
---

# Design System: Sakamoto desktop

The shared desktop client is a compact native connection utility for people
who already use sakamoto and want routine controls without keeping a terminal
open. System typography and Lucide icons span macOS, Windows and Linux. MyGo
owns input, keyboard navigation, accessibility and the native shell. Onevra
supplies icons; the panel styles MyGo bases to enforce the same color, border
and motion rules throughout. This system covers desktop content, not the TUI,
mobile clients or the owner's existing app artwork.

## Color

All authored content colors come from [Radix UI Colors](https://www.radix-ui.com/colors).
`desktop/internal/menubar/style.go` pins values from
[dbdb85470547c7d34b9001f48fddb08ded335979](https://github.com/radix-ui/colors/tree/dbdb85470547c7d34b9001f48fddb08ded335979).
`desktop/LICENSE.radix` preserves its MIT attribution and ships in every package.

Follow [Radix's scale roles](https://www.radix-ui.com/colors/docs/palette-composition/understanding-the-scale):

| Role | Scale and step |
| --- | --- |
| Panel background | Slate 1 |
| Popup background | Slate 2 |
| Control default, hover, press | Slate 3, 4, 5 |
| Subtle separators | Slate 6 |
| Control border, hover border | Slate 7, 8 |
| Primary action | Indigo 9 |
| Primary hover and press | Light Indigo 10 in both appearances |
| Selected option/segment | Indigo 3 background and Indigo 11 text |
| Secondary text | Slate 11 |
| Primary text | Slate 12 |
| Success, warning, failure | Green 11, Amber 11, Red 11 |
| Input selection | Indigo 5 |
| Keyboard focus | Indigo 9 |
| Scrollbar | Slate 9 |
| Text on a solid primary action, switch thumb | Radix Indigo contrast token (white) |

Dark Indigo 10 with white small text falls below 4.5:1, so primary hover uses
light Indigo 10 in both appearances. Primary text contrast is at least 5.20:1
in default, hover and pressed states. Secondary text on the neutral control
states stays above 4.5:1. Disabled controls reduce opacity and remain disabled
semantically. State colors always accompany words or a recognizable status icon.
Accent is reserved for primary actions, selection and focus. White is the
[Radix Indigo contrast token](https://github.com/radix-ui/themes/blob/main/packages/radix-ui-themes/src/styles/tokens/colors/indigo.css),
used for solid actions and the switch thumb.

The entire content background is opaque Slate 1, including on a material host.
System appearance still determines light/dark. The OS owns its popover frame,
tray tint, menus outside the panel and window shadow; content renders do not
verify those surfaces. Windows/Linux's authored tray glyph uses Indigo 9.

## Typography and copy

Use exactly three sizes in logical pixels (DIPs): 16 for panel titles, 13 for
body, controls and section headings, 12 for measurements, secondary status,
errors, popup hints and legal text. Use weight and spacing for other hierarchy.
Never use an 11px caption or add a fourth size. All text uses system sans.
Preserve full user-owned node and group names in accessible action labels when
visual names truncate.

Use concise, professional operational English. Do not use middle dots as copy
separators, decorative eyebrows, overlines, casual filler or introductory
paragraphs that repeat a control. Preserve user-owned labels verbatim. Successful
selection, mode and test changes are already visible in their controls and do
not add footer prose. Footer errors remain actionable and readable.

Show extra text when it changes the next decision: setup/offline recovery,
external service ownership, actual health/test results, maintenance instructions
and the interruption warning beside reconnect confirmation. Queued/running route
tests remain pending; never call them successful recovery. Demo identity remains
visible. Legal attribution remains in Settings and package resources.

## Layout, alignment and shape

The shared panel is 400 × 640 DIPs with 20-point content insets and 14-point gaps.
Distinct sections in settings/repair use 20-point gaps; related controls use 8.
The hierarchy is title/settings, observed state and supported connection action,
routing mode, active route, aligned traffic, then node group/search/list. Only
the node list scrolls on home. Settings and repair scroll their own body and
retain navigation/footer positions.

Node rows are 44 points high. A fixed 16-point check slot and 8-point gap keep
selected/unselected names aligned; the latency action remains a separate 32-point
target. Row padding and footer text align to the content inset. Traffic values
share their labels' leading edge. A group with a single available choice is
plain text rather than an unnecessary dropdown.

All authored control borders, separators and keyboard focus outlines are 0.5
logical pixels. Focus uses Indigo 9 with a 2-point gap around its control; input
focus changes its border color. Content has no shadows, cards,
colored edge stripes or ornamental rounded containers. A control's radius is
6; the routing track is 8 with an inset of 2; popup radius is 10 with an inset
of 4. Insets include the border, making inner/outer corners concentric. A login
switch uses a functional 20-point capsule and 16-point circular thumb inset 2.
Lucide glyph strokes belong to the icon design, not the container-border token.

## Interaction and motion

All content state changes are immediate. No section entrances, fades, sliding
selection, animated glass, looping activity glyphs or application shadows.
MyGo bases retain pointer/keyboard handling, semantic disabled states, native
text editing, input selection, focus, popup dismissal and accessible roles.
Icon hints use a plain palette-colored tooltip at 12px without glass/shadow.

Routine actions have no extra confirmation. Reconnect alone presents its
interruption warning and Cancel/Reconnect now at the action's location. Closing
the panel clears a pending confirmation. Opening a page or dropdown does not
start work or persist a preference. Appearance persists only after selection;
login remains opt-in and disabled in demo mode.

External-core mode shows Core online and Managed externally, with API controls
and configuration access only. API availability does not imply TUN ownership.
Missing supervisor recovery opens a bundled terminal only through its named
button. Closing/quitting the app keeps the independently running service alive;
opening never connects. Hidden panels stop observations and release view bindings.

## Evidence and limits

Source: `desktop/internal/menubar/style.go`, `panel.go`, `recovery.go`, `app.go`,
the platform adapters and `internal/desktop` public projections. Input-driven
view workflows produce light/dark home, settings, popup and repair renders under
`dist/menubar-verification/`, plus machine-readable receipts. Service/API fixture
workflows verify ownership and lifecycle semantics. Package verification covers
architecture, checksums and license resources.

Shared content captures are evidence of the view on the test host. A packaged
macOS run checks lifecycle and captures either a real system window or an
explicitly recorded content fallback. Native acceptance remains **recapture**
for platform placement, OS frame, screen readers and IME until interactive OS
evidence exists. Cross-compilation does not imply Windows/Linux desktop acceptance.
