# sakamoto

A mouse-enabled macOS TUI for [sing-box](https://sing-box.sagernet.org/). Inspired by Shadowrocket's Home / Config / Data / Settings workflow; **not affiliated with Shadowrocket or SagerNet**.

> **No real server credentials belong in this repository or tap.** The default local state directory is `~/.sakamoto` on a fresh install. Existing installs with a running legacy daemon keep using `~/.config/sakamoto` until explicitly migrated.

## Quick start (macOS)

Requirements: macOS, sing-box **1.14.2+** (native API mode support), and a terminal with SGR mouse events (e.g. Ghostty/iTerm2). The [Homebrew tap](https://github.com/pi-dal/homebrew-tap) installs the sing-box and Go build dependencies; **installation never starts a VPN or installs privileged services**.

```bash
brew install pi-dal/tap/sakamoto
install -d -m 700 ~/.sakamoto
cp "$(brew --prefix)/share/sakamoto/sakamoto.example.yaml" ~/.sakamoto/sakamoto.yaml
cp "$(brew --prefix)/share/sakamoto/nodes.example.txt" ~/.sakamoto/nodes.txt
```

For a source build instead, clone this repository, run `go build -o sakamoto ./cmd/sakamoto`, and use the templates at the repository root. Replace the example API secret with a unique random value (`openssl rand -hex 32`) before generating a config.

Edit `~/.sakamoto/sakamoto.yaml` to point `conf:` at your own Shadowrocket `.conf`, or open `sakamoto` → **Config → Import config** and paste its URL. If `include=ad.conf` is relative, the importer fetches it from the same URL directory. Add manual node share links to `nodes.txt` through **Config → Nodes & sources → Add node**, then **Regenerate**. Check the result:

```bash
sing-box check -c ~/.sakamoto/config.json
```

For TUN and always-on fallback, install the macOS services *once*: `bash "$(brew --prefix)/share/sakamoto/scripts/install-macos.sh"` (source builds: `SAKAMOTO_BIN="$PWD/sakamoto" bash scripts/install-macos.sh`). It asks for confirmation before installing a root LaunchDaemon and never automatically connects the VPN. Do **not** connect Shadowrocket and sakamoto TUN simultaneously. Then run `sakamoto` and click **Connect**. Daily use requires only that one command; `daemon` and `watch` are launchd internals. `sakamoto help` lists keyboard shortcuts.

## Interface

- **Home:** connected/verified state, live proxy chain and SOCKS exit, node groups with latency; left-click any node to select it, right-click for test/details, mouse wheel to scroll. Click **Mode** or press `m` to switch Rule/Global/Direct through the native API; [mode rules and exclusions](docs/configuration.md#rule-global-and-direct-routing-modes) are explicit.
- **Config:** import a `.conf` URL/file, browse General/rules/DNS, manage source subscriptions and manual node links; edited or deleted sources are saved locally. Generate changes, then disconnect/reconnect to apply.
- **Data:** traffic, active connections, logs and close-connection action.
- **Settings:** clickable toggles and editable values; optional iCloud source sync includes the local conf and recursive relative includes, not just nodes. Reality fallback order can be set as `RealityAuto,OthersAuto`. Experimental unmatched policy supports `off` (direct unmatched), `on` (proxy unmatched) and `auto` (learn from corroborated repeated direct timeouts). See [privacy limits and ruleset reference](docs/configuration.md#experimental-unmatched-domain-policy). A manual node selection is respected by the fallback watcher.
- **About → Copyright:** a terminal-safe block portrait and a tribute explaining the name, plus software copyright, portrait attribution, license and non-affiliation statement.

**Fallback semantics:** `RealityAuto` gets fresh URL tests, then `OthersAuto` is selected when Reality is unavailable; switching back needs `recover_after` consecutive healthy checks. The watcher runs without an open TUI. Explicit node selection is not overridden. `sakamoto recover` requests an immediate watcher-owned test pass for a local integration such as [pi-proxy-guard](https://github.com/pi-dal/pi-proxy-guard); it does not restart the TUN or force a node. [Request statuses and limitations](docs/configuration.md#on-demand-recovery-trigger) are documented.

**Browser routing on macOS:** some system DNS answers may be poisoned or cached. With `system_proxy.enabled: true` and `mixed_inbound.enabled: true`, the watcher enables the local HTTP/HTTPS system proxy while sakamoto is connected and restores prior settings when disconnected. TUN remains enabled for non-browser traffic and UDP. This behavior is opt-in in the example config. The initial user device has it enabled and was tested with Google, GitHub and Bing.

## Configuration and migration

- [TUI: mouse and keyboard workflows](docs/tui.md)
- [Configuration, fallback and privacy](docs/configuration.md)
- [Shadowrocket import and unsupported features](docs/import.md)
- [macOS installation and legacy-directory migration](docs/migration.md)
- [iCloud Drive sync (optional)](docs/icloud.md)
- [Agent-readable development notes](skills.md)
- [Code quality review and remaining refactoring plan](docs/code-quality.md)
- [Public release readiness](docs/release-checklist.md)
- [Portrait attribution and CC BY 2.0 asset license](docs/portrait-license.md)

**Limitations:** sing-box does not implement Shadowrocket's HTTP URL/Header/Body rewrite, MITM, or JavaScript response scripts. Import reports these differences; do not claim feature parity. DNS/domain and GeoIP rules require checking against your *current* Shadowrocket export; compiled `.db.rule` data alone is not the original `.conf`.

## Development

```bash
go test ./...
go vet ./...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
go build ./cmd/sakamoto
```

Code-quality baseline: `fuck-u-code analyze . -f json` improved from **65.9** to **about 78** after splitting oversized modules and handling unchecked I/O; the Go linter reports **0 issues**. The installed `fuck-u-code` shell parser may fall back to regex; treat its score as a trend, not a correctness proof. CI enforces tests, vet and the pinned Go linter.

On macOS sign your local build ad hoc when copying to a different location: `codesign -s - -f ./sakamoto`. Stage an API credential with `sakamoto rotate-api` (no change to the live connection; applied at the next normal TUI connect). Use `sakamoto rotate-api --apply-now` only when accepting an immediate brief reconnect. The key is never printed. Do not commit generated binary, actual node links, subscription URLs, API secret, `auto-proxy.json` (visited domains), `.srs`, logs or generated `config.json`.

## License and attribution

Copyright © 2026 pi-dal. `sakamoto` is licensed under **GPL-3.0-or-later**; see [LICENSE](LICENSE). It links to [SagerNet/sing-box](https://github.com/SagerNet/sing-box), also GPL-3.0-or-later. The About-page portrait assets are separately [CC BY 2.0](docs/portrait-license.md). See [NOTICE.md](NOTICE.md) for upstream and image attribution. This is an independent tool, not endorsed by SagerNet or Shadowrocket.
