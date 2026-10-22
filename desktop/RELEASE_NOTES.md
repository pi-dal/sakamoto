# Desktop release

The native menu bar/tray client shares node selection, routing modes, latency,
traffic and appearance across macOS, Windows and Linux.

- macOS: arm64 and Intel apps signed with Developer ID Application, notarized
  by Apple and stapled before ZIP packaging. Includes the CLI and offline rescue helper.
- Windows/Linux: arm64 and amd64 portable previews. These packages are unsigned.
  They control an existing compatible core; privileged tunnel/DNS service adapters
  are not included.
- Opening the client never connects a tunnel. Closing the panel keeps it resident;
  quitting preserves the independent service.

Install the macOS GUI after the release workflow updates the tap:

```sh
brew install --cask pi-dal/tap/sakamoto-desktop
```

The CLI formula remains `brew install pi-dal/tap/sakamoto`. GUI installation does
not create privileged services or change user configuration. Use the bundled TUI
or CLI setup workflow to configure services separately.

Each download has a SHA-256 entry in `SHA256SUMS`. `release-metadata.json` records
platform capabilities and macOS signing/notarization evidence. macOS
`verification.json` records checks of the actual final ZIP contents. See
[desktop documentation](https://github.com/pi-dal/sakamoto/blob/main/desktop/README.md)
for setup and platform verification limits.
