# About-page portrait provenance

The four embedded grayscale sample matrices (`internal/tui/assets/portrait-{wide,compact,mini,tiny}.gray`) and their monochrome `.txt` fallbacks are **adaptations**, not original photography.

- **Source:** [RyuichiSakamoto2007.jpg](https://commons.wikimedia.org/wiki/File:RyuichiSakamoto2007.jpg), Wikimedia Commons.
- **Photograph:** Joi Ito (2007); Commons portrait cropped from his photograph of Ryuichi Sakamoto and Keigo Oyamada by **Solid State Survivor**.
- **Source license:** [Creative Commons Attribution 2.0 (CC BY 2.0)](https://creativecommons.org/licenses/by/2.0/). Attribution and indication of modifications are required.
- **Modifications for sakamoto:** A face-focused crop, grayscale resampling, conversion to two vertically stacked image pixels per terminal cell (truecolor/ANSI-256 `▀`), plus a monochrome block fallback by the sakamoto project. No original JPEG is bundled in the release. These derived portrait assets remain subject to CC BY 2.0; the program code is separately GPL-3.0-or-later.
- **Verified source SHA-256:** `c51cedfd74096a9503636eea88e2275568f9a8fdde899a7d305007962cf01471` (484×532 JPEG). Only regenerate from a licensed source that you have verified.

Reproduce both text-only asset variants from the verified JPEG stored locally (not committed):

```bash
for spec in wide:56:21 compact:46:16 mini:28:10 tiny:18:7; do
  IFS=: read -r size width height <<< "$spec"
  go run ./tools/portraitgen -input /path/to/RyuichiSakamoto2007.jpg \
    -output "internal/tui/assets/portrait-$size.gray" -width "$width" -height "$height" -format grayhex
  go run ./tools/portraitgen -input /path/to/RyuichiSakamoto2007.jpg \
    -output "internal/tui/assets/portrait-$size.txt" -width "$width" -height "$height" -format blocks
done
```

The About page repeats the attribution and license. This tribute does not imply endorsement by Ryuichi Sakamoto, his family, estate or representatives. The mobile app icons use separate owner-provided artwork and do not use the portrait.

## Mobile app icons

The launcher artwork is owner-provided in `assets/app-icon.png`, selected
from the supplied ChatGPT image on 2026-10-05. `mise run icons` resizes that
PNG into opaque iOS icons and Android adaptive/legacy icons; the themed
variant uses the artwork's luminance. No portrait samples are used.

## Terminal technique reference

The higher-resolution half-block design follows the approach demonstrated by [`viu`](https://github.com/atanunq/viu) / [`viuer`](https://github.com/atanunq/viuer) (MIT) and Charm's [`mosaic`](https://github.com/charmbracelet/x/tree/main/mosaic) (MIT). Bubble Tea's [image support discussion](https://github.com/charmbracelet/bubbletea/issues/163) documents redraw and alternate-screen limitations of Kitty/Sixel images, so normal text cells are safer here. No renderer code is copied or linked; our small converter uses Go's standard library. Ghostty and other modern terminals use truecolor, xterm-256color uses ANSI-256, and NO_COLOR/TERM=dumb keeps a text fallback without graphics protocols.
