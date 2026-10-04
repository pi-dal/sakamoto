# Attribution

`sakamoto` uses the Go module `github.com/sagernet/sing-box`, including its daemon gRPC client and SRS rule-set support.

- Upstream: https://github.com/SagerNet/sing-box
- Copyright © 2022–present nekohasekai and contributors
- License: GPL-3.0-or-later (consult the upstream `LICENSE` and source for its notices)

This independent project does not use the name "sing-box" as its own product name and does not imply upstream endorsement. Shadowrocket is referenced solely to describe import compatibility; this project is not affiliated with Shadowrocket.

## sing-box-for-apple (iOS extension sources)

The iOS PacketTunnel integration ports the iOS-required subset of the official Apple client's extension sources:

- Upstream: https://github.com/SagerNet/sing-box-for-apple (dev branch, commit `d1224bb5081b3df5d0ecc55b1bd3d72ea6c60628`)
- Copyright © 2022 by nekohasekai <contact-sagernet@sekai.icu>
- License: GPL-3.0-or-later
- Ported files (each carries the upstream license header at the top):
  - `ios/Extension/ExtensionPlatformInterface.swift` ← `Library/Network/ExtensionPlatformInterface.swift` (iOS subset: OpenTun over packetFlow, interface monitor, WIFI state, notifications, TailscaleHostname; macOS XPC/system-extension/shell/bridge branches dropped as explicit unsupported throws)
  - `ios/Extension/ExtensionSupport.swift` ← `Library/Network/Extension+RunBlocking.swift`, `Extension+Iterator.swift`, `ExtensionErrors.swift`
  - `ios/Extension/SakamotoPacketTunnelProvider.swift` ← lifecycle of `Library/Network/ExtensionProvider.swift` (Setup → CommandServer → startOrReloadService), restructured around the sakamoto provider IPC
- Signatures are adapted to the Libbox **v1.14.2** gomobile bindings, which differ from the dev branch the upstream file targets (e.g. `send`/`cancel` vs `sendNotification`/`cancelNotification`, `usePlatformAutoDetectControl` vs `usePlatformAutoDetectInterfaceControl`, protocol names suffixed `Protocol` by the Swift importer due to the class/protocol name collision in this gomobile generation).

The upstream repository is **not** vendored; only the files above are derived, and the framework binaries (`Libbox.xcframework`) are built from the upstream Go module's own builder (`cmd/internal/build_libbox`, default tag set including `with_tailscale`) via `ios/scripts/build-libbox.sh`.

## About-page portrait

- Source photograph: [RyuichiSakamoto2007.jpg](https://commons.wikimedia.org/wiki/File:RyuichiSakamoto2007.jpg) by **Joi Ito**; the Wikimedia Commons portrait was cropped by **Solid State Survivor**.
- License for the source and its terminal pixel-art adaptations: [CC BY 2.0](https://creativecommons.org/licenses/by/2.0/).
- Changes made here: face-focused crop, grayscale resampling, truecolor/ANSI-256 half-block portrait sizes and monochrome fallback. See [docs/portrait-license.md](docs/portrait-license.md) for attribution, source digest and reproduction details.

The name sakamoto is a tribute to the musician **Ryuichi Sakamoto**, not a claim of endorsement or association with him, his family, estate or representatives.
