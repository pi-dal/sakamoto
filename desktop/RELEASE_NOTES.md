# Desktop 0.2.21

Check & repair now separates health diagnosis, core reconnect and configuration
recovery. Failed actions display a useful public reason without exposing private
paths, source URLs, credentials or raw engine diagnostics.

- **Restore working configuration…** restores the complete verified runtime,
  including its matching engine and checked local assets. Corrupt current JSON
  or YAML does not prevent fallback. A saved working copy is required.
- **Regenerate configuration…** reads saved settings and configured sources,
  requires supported proxy nodes, preserves API identity/DNS policy, and checks
  a private candidate before replacing anything. A failed preparation retains
  the live process and active files.
- Configuration replacement requires a separate confirmation. Manual stop,
  maintenance and newer edits supersede stale work. Failed activation can use
  one verified working fallback; repeated failure blocks automatic attempts.
- Older supervisors show **Update background service…**. The bundled CLI
  requests local confirmation/administrator authorization, stages independent
  rescue before disconnecting, retains service executables outside Homebrew,
  and verifies the approved reconnect. Installing the GUI alone does not
  replace the running service.
- API settings that differ between the GUI sidecar and core configuration now
  produce a configuration-mismatch result. TUI generation also validates an
  independent candidate before changing active rules.

A working fallback is promoted only after the existing unchanged-runtime
stability/readiness checks. An app update or successful configuration check
alone does not create one. Health verification covers the native API and
configured protected DNS, not every remote destination.

The shared tray client retains Radix colors, 16/13/12 typography, 0.5-point
borders, explicit reconnect confirmation and idle observation while hidden.
Opening the app never connects a tunnel; quitting preserves its service.

- macOS: arm64/Intel apps signed with Developer ID Application, notarized and
  stapled. The app includes the matching CLI and independent offline rescue.
- Windows/Linux: unsigned arm64/amd64 portable previews for an existing core.
  API diagnosis is available; configuration/lifecycle recovery stays with the
  external service manager.

```sh
brew update
brew upgrade --cask pi-dal/tap/sakamoto-desktop
```

The separate CLI formula is `pi-dal/tap/sakamoto`. Check its version before using
new terminal commands. See [configuration repair](https://github.com/pi-dal/sakamoto/blob/main/docs/configuration-repair.md)
for service activation, offline behavior and verification limits.
