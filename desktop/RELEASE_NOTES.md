# Desktop 0.2.24

MyGo now brings learning history and source management into the desktop panel.

- Learning shows the saved mode, learned route count and twenty recent checks.
- Select failed domains and add exact proxy rules, with duplicate prevention and existing direct/reject overrides retained.
- Add, edit and remove nodes, subscriptions and routing policies in the panel. Node remark, server and port edits retain credentials and advanced link parameters.
- Search lists, mask sensitive links by default and reject stale edits before replacing saved sources.
- Saving updates configuration sources. Use Check & repair to regenerate and apply through the existing explicit reconnect flow; external-core users apply through their service.

Input-driven desktop workflows cover editing, validation, stale saves, selection, search and light/dark renders. Both macOS architectures are signed, notarized and stapled; Windows/Linux portable clients use an existing external core. Physical platform accessibility/IME acceptance remains separate from these automated fixtures.

```sh
brew update
brew upgrade --cask pi-dal/tap/sakamoto-desktop
```

See [desktop management](https://github.com/pi-dal/sakamoto/blob/main/docs/desktop-management.md). The CLI Homebrew formula remains independent.

---

# Desktop 0.2.23

The menu bar GUI now includes a validated editor for persistent settings that were
previously available only in the TUI.

- Edit Network, DNS, Traffic policy, Privacy, Local proxy, Advanced, Cloud sync
  and Fallback settings from Settings, then Edit configuration.
- Save validates the complete candidate and atomically replaces the sidecar only
  after it can be loaded again. Invalid ports, DNS services, durations, paths,
  modes and source lists leave the active configuration unchanged.
- API URL and secret remain hidden and the existing API identity is preserved.
- Saving does not restart the core. Check & repair provides the explicit
  regenerate and reconnect boundary for applying saved changes.
- Nodes, subscription sources and policy rule collections continue to use the TUI
  list editors. The GUI exposes Open TUI for those workflows.
- Light and dark configuration renders, save feedback and configuration failure
  states are covered by the shared desktop E2E workflow.

The feature does not replace the running supervisor, change the active service or
connect a tunnel merely by opening or saving the editor.

```sh
brew update
brew upgrade --cask pi-dal/tap/sakamoto-desktop
```

The separate CLI formula remains independent. See [desktop configuration](https://github.com/pi-dal/sakamoto/blob/main/docs/desktop-configuration.md)
for the field groups, validation boundary and repeatable verification.

---

# Desktop 0.2.22

The home header now opens **Connections**, a searchable live list of active
connections across macOS, Windows and Linux.

- See the destination, application (when supplied by the core), outbound, network
  type and uploaded/downloaded totals. Search by any of these names or the
  destination IP. Newest connections appear first.
- Closed history is excluded and finished connections disappear. Delta-only
  traffic events update totals while preserving connection metadata.
- Connection tracking runs only while its page is visible. Leaving the page or
  hiding clears live rows and stops the subscription; reopening fetches the
  current snapshot. Inspection preserves active connections.
- Interrupted streams reconnect. Cores without connection tracking show that
  limitation while keeping node, mode and traffic controls available.
- Large lists virtualize their rows and coalesce lifecycle bursts. The panel
  retains Radix colors, concise copy and 0.5-point borders in both appearances.

The actual pinned core and loopback proxy workflow verify live connections,
traffic totals, closed-history filtering and that the same data stream survives
leaving/hiding the page. API/controller and input-driven view workflows cover
reconnect, idle subscriptions, search, empty/error states and 2,000-row lists.
These isolated checks do not modify the user's running service or network.

macOS arm64/Intel releases are signed, notarized and stapled. Windows/Linux
arm64/amd64 portable clients continue to use an existing external core service.
Installing this app does not replace the running supervisor or connect a tunnel.

```sh
brew update
brew upgrade --cask pi-dal/tap/sakamoto-desktop
```

The separate CLI formula remains independent; this display feature requires only
the GUI update. See [live connections](https://github.com/pi-dal/sakamoto/blob/main/docs/desktop-connections.md)
for protocol behavior and verification limits.
