# About-page portrait provenance

The four embedded Unicode-block portraits (`internal/tui/assets/portrait-{wide,compact,mini,tiny}.txt`) are **adaptations**, not original photography.

- **Source:** [RyuichiSakamoto2007.jpg](https://commons.wikimedia.org/wiki/File:RyuichiSakamoto2007.jpg), Wikimedia Commons.
- **Photograph:** Joi Ito (2007); Commons portrait cropped from his photograph of Ryuichi Sakamoto and Keigo Oyamada by **Solid State Survivor**.
- **Source license:** [Creative Commons Attribution 2.0 (CC BY 2.0)](https://creativecommons.org/licenses/by/2.0/). Attribution and indication of modifications are required.
- **Modifications for sakamoto:** A tighter crop of the face and shoulders, luminance downsampling and conversion to a six-shade Unicode block mosaic by the sakamoto project. No original JPEG is bundled in the release. These derived portrait assets remain subject to CC BY 2.0; the program code is separately GPL-3.0-or-later.
- **Verified source SHA-256:** `c51cedfd74096a9503636eea88e2275568f9a8fdde899a7d305007962cf01471` (484×532 JPEG). Only regenerate from a licensed source that you have verified.

Reproduce the text-only assets with the source JPEG stored locally (not committed):

```bash
go run ./tools/portraitgen -input /path/to/RyuichiSakamoto2007.jpg -output internal/tui/assets/portrait-wide.txt -width 38 -height 21
go run ./tools/portraitgen -input /path/to/RyuichiSakamoto2007.jpg -output internal/tui/assets/portrait-compact.txt -width 28 -height 16
go run ./tools/portraitgen -input /path/to/RyuichiSakamoto2007.jpg -output internal/tui/assets/portrait-mini.txt -width 18 -height 10
go run ./tools/portraitgen -input /path/to/RyuichiSakamoto2007.jpg -output internal/tui/assets/portrait-tiny.txt -width 14 -height 7
```

The About page repeats the attribution and license. This tribute does not imply endorsement by Ryuichi Sakamoto, his family, estate or representatives. The portrait is **not** a product logo or a claim of affiliation.

## Terminal technique reference

The static density-map approach follows the established terminal-image technique illustrated by [`qeesung/image2ascii`](https://github.com/qeesung/image2ascii) (MIT) and [`TheZoraiz/ascii-image-converter`](https://github.com/TheZoraiz/ascii-image-converter) (Apache-2.0). No source code or runtime dependency from either repository is copied or linked; the small converter here uses only Go's standard library. Unicode block glyphs need no Kitty, Sixel, image viewer or true-colour terminal support.
