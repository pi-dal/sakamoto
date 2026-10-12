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

## Location HTTPS processor

The optional iOS location HTTPS adapter uses [elazarl/goproxy](https://github.com/elazarl/goproxy), version v1.8.5. Its BSD-3-Clause notice follows. The WLOC codec in `pkg/mobilelocation` is independently implemented using protocol facts researched from [xweiba/location-spoofer](https://github.com/xweiba/location-spoofer); that project's AGPL implementation and C archive are not included.

> Copyright (c) 2012 Elazar Leibovich. All rights reserved.
>
> Redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met:
>
> - Redistributions of source code must retain the above copyright notice, this list of conditions and the following disclaimer.
> - Redistributions in binary form must reproduce the above copyright notice, this list of conditions and the following disclaimer in the documentation and/or other materials provided with the distribution.
> - Neither the name of Elazar Leibovich. nor the names of its contributors may be used to endorse or promote products derived from this software without specific prior written permission.
>
> THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

## About-page portrait

- Source photograph: [RyuichiSakamoto2007.jpg](https://commons.wikimedia.org/wiki/File:RyuichiSakamoto2007.jpg) by **Joi Ito**; the Wikimedia Commons portrait was cropped by **Solid State Survivor**.
- License for the source and terminal pixel-art adaptations: [CC BY 2.0](https://creativecommons.org/licenses/by/2.0/).
- Changes made here: face-focused crop, grayscale resampling, truecolor/ANSI-256 half-block portrait sizes and monochrome fallback. See [docs/portrait-license.md](docs/portrait-license.md) for attribution, source digest and reproduction details.

The mobile app icon (`assets/app-icon.png`) is owner-provided artwork selected for this project. The launcher exports resize that image; the Android themed variant is a luminance adaptation. It is separate from the CC BY 2.0 terminal portrait.

The name sakamoto is a tribute to the musician **Ryuichi Sakamoto**, not a claim of endorsement or association with him, his family, estate or representatives.
