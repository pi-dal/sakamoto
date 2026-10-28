# sakamoto

A [sing-box](https://sing-box.sagernet.org/) connection client with a macOS terminal UI, a shared desktop tray app, and native Android and iOS clients. Import Shadowrocket configurations, select proxy nodes, switch routing modes, and inspect traffic.

**License: [GPL-3.0-or-later](LICENSE).** Servers and credentials are supplied by you. This project is independent of SagerNet and Shadowrocket.

| Client | What you can use | Install and details |
| --- | --- | --- |
| macOS TUI | Mouse and keyboard control of the tunnel, configuration, nodes, traffic and fallback | [Quick start below](#install-and-connect-on-macos) |
| macOS menu bar | Native panel with tunnel control, nodes, live connections, settings and repair through the existing supervisor | `brew install --cask pi-dal/tap/sakamoto-desktop` · [Desktop guide](desktop/README.md) |
| Windows / Linux tray | Preview for an externally managed core: node/mode control, latency, traffic and live connections; tunnel lifecycle and DNS stay with your service manager | [Desktop setup and limits](desktop/README.md) |
| Android 7.0+ | Native VPN app with configuration editing, diagnostics, widgets and notification controls | [Signed APK installation and first connection](android/README.md#install-the-official-release) |
| iOS | SwiftUI app and PacketTunnel integration; device installation requires signing and provisioning | [Build, signing and device verification](ios/README.md) |

## Usage

On a configured Mac, open the TUI:

```sh
sakamoto
```

1. In **Home**, click **Connect** and check the displayed network verification result.
2. Click a node to select it; right-click for a latency test or details. Click **Mode** or press `m` to cycle **Rule → Global → Direct** for new connections.
3. In **Config**, import a `.conf`, manage nodes and subscriptions, edit settings or add routing policies. **Regenerate**, then disconnect/reconnect to apply generated changes.
4. In **Data**, inspect traffic, active connections and logs.

`Tab` switches pages; arrows or `j`/`k` move; `Enter` selects; `Esc` goes back; `q` quits the TUI. See the [TUI guide](docs/tui.md) for all controls and the [desktop guide](desktop/README.md) for tray workflows. Opening either client does not connect automatically.

Read-only diagnosis and watcher recovery are also available from the terminal:

```sh
sakamoto repair --check  # inspect core/API and configured protected DNS
sakamoto recover         # request fresh group tests from the running watcher
# Example recovery acknowledgement:
# queued
```

Recovery applies the watcher's existing fallback policy and respects manual node selection. An acknowledgement is not proof that connectivity has recovered; see [recovery statuses](docs/configuration.md#on-demand-recovery-trigger). Run `sakamoto help` for the complete command list.

## Install and connect on macOS

The TUI requires **sing-box 1.14.2+** and a terminal with SGR mouse support for pointer control, such as Ghostty or iTerm2. Homebrew installs the core dependency. Installation and service setup leave the VPN disconnected.

### 1. Install and prepare private settings

```sh
brew install pi-dal/tap/sakamoto
install -d -m 700 ~/.sakamoto
cp "$(brew --prefix)/share/sakamoto/sakamoto.example.yaml" ~/.sakamoto/sakamoto.yaml
cp "$(brew --prefix)/share/sakamoto/nodes.example.txt" ~/.sakamoto/nodes.txt
chmod 600 ~/.sakamoto/sakamoto.yaml ~/.sakamoto/nodes.txt
openssl rand -hex 32
```

Edit `~/.sakamoto/sakamoto.yaml`: set `api.secret` to the generated random value, `conf:` to your own Shadowrocket `.conf` file or URL, and `nodes_file:` to the absolute path of your `nodes.txt`. Replace example node links with your own. Keep these files and credentials private.

Fresh installs use `~/.sakamoto`. Existing installations with a live legacy daemon retain `~/.config/sakamoto`; follow the [migration guide](docs/migration.md#existing-installation-at-configsakamoto) before moving state.

### 2. Import and validate

Open `sakamoto`, choose **Config → Import config** to import a `.conf` URL/file, and use **Nodes & sources → Add node** for manual share links. Relative `include=ad.conf` files are fetched from the source URL's directory. Choose **Regenerate**, then validate:

```sh
sing-box check -D ~/.sakamoto -c ~/.sakamoto/config.json
```

See [import support and differences](docs/import.md) for supported rules and sources.

### 3. Install background services and connect

```sh
sakamoto setup --check  # read-only preflight
sakamoto setup         # interactive installation with administrator authorization
sakamoto
```

Setup installs the root supervisor for TUN control and the user watcher for automatic group fallback. It refuses to replace partial or unavailable installations. Disconnect Shadowrocket's VPN before clicking **Connect** in sakamoto.

**Reconnect manually after every reboot.** The supervisor and watcher restart, but the TUN and protected DNS require an explicit connection. The supervisor attempts to restore prior DNS settings when its protected listener is gone; see [startup and reboot behavior](docs/dns-protection.md#startup-and-reboot-behavior).

For source builds, use the [contributing guide](CONTRIBUTING.md#build-the-cli-from-source). For an existing installation, see [setup, command-link repair and migration](docs/migration.md).

## Configuration and guides

The [example settings](sakamoto.example.yaml) define the sidecar YAML; the [configuration reference](docs/configuration.md) explains routing modes, policy overrides, fallback and privacy.

| Task | Guide |
| --- | --- |
| Import Shadowrocket rules and subscriptions | [Supported imports and unsupported features](docs/import.md) |
| Configure routing, automatic fallback or experimental learning | [Configuration](docs/configuration.md) · [Desktop learning](docs/desktop-learning.md) · [Mobile learning](docs/mobile-learning.md) |
| Enable protected system DNS | [Activation, restoration and scope](docs/dns-protection.md) |
| Edit desktop settings or inspect live connections | [Configuration editor](docs/desktop-configuration.md) · [Connections](docs/desktop-connections.md) |
| Sync sources across devices | [S3](docs/s3-sync.md) · [iCloud Drive](docs/icloud.md) |
| Diagnose a failed core or restore a working configuration | [Configuration repair](docs/configuration-repair.md) · [Offline desktop recovery](docs/stability-recovery.md) · [Mobile recovery](docs/mobile-stability-recovery.md) |
| Install or migrate macOS services | [Installation, migration and troubleshooting](docs/migration.md) |

## Limits and recovery

- **Shadowrocket compatibility:** HTTP URL/Header/Body rewrites, MITM rules and JavaScript response scripts are not implemented by sing-box. Compiled `.db.rule` files cannot substitute for the original `.conf`. Review import diagnostics before relying on the result.
- **Connection verification:** a running TUN, a selected node and a successful latency test are separate observations. A successful network check confirms its tested path; it does not establish reachability of every destination or DNS privacy.
- **Browser routing on macOS:** the example enables both `system_proxy` and `mixed_inbound`. While connected, the watcher saves and sets the HTTP/HTTPS system proxy and restores it on disconnect. Review the network service name in your settings; see [configuration](docs/configuration.md#minimal-settings) and [troubleshooting](docs/migration.md#network-troubleshooting).
- **Core repair:** `sakamoto repair` retains a healthy process and can recover a stopped or crashed core. A live but unhealthy core requires explicit `sakamoto repair --restart`. Configuration restoration, regeneration and old-supervisor updates have separate prerequisites; see [configuration repair](docs/configuration-repair.md).
- **Mobile releases:** capabilities described in source may require a newer installed app. Android debug and official APKs use different signing certificates; preserve data before a manual transition. Unsigned iOS simulator builds cannot run a real VPN tunnel.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the mise toolchain, source builds, verification commands, packaging and release references. [Agent development notes](skills.md) and the [code quality review](docs/code-quality.md) provide additional maintenance context.

## License and attribution

Copyright © 2026 pi-dal. Licensed under **GPL-3.0-or-later**; see [LICENSE](LICENSE). Sakamoto links to [SagerNet/sing-box](https://github.com/SagerNet/sing-box), also GPL-3.0-or-later. The About-page portrait assets use a separate [CC BY 2.0 license](docs/portrait-license.md). See [NOTICE.md](NOTICE.md) for upstream and artwork attribution.
