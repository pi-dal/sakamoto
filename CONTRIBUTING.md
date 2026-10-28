# Contributing to sakamoto

Use the checked-in mise tasks for local development and CI. Keep real servers, subscription URLs, credentials and generated runtime state outside this repository.

## Toolchain

Runtime versions and shared tasks live in [mise.toml](mise.toml). Android SDK/JDK versions are opt-in through [mise.android.toml](mise.android.toml). Xcode and its command-line tools remain macOS prerequisites; Gradle uses the checked-in wrapper.

```sh
mise trust
mise install
```

Project tasks do not install global Homebrew tools or change global mise runtime choices. `mise run mobile:tools` exposes mise-managed gomobile binaries at `GOPATH/bin`, as required by the upstream sing-box builder. The [Homebrew tap](https://github.com/pi-dal/homebrew-tap) is an end-user distribution option.

## Build the CLI from source

From a clone of this repository on macOS:

```sh
mise exec -- go build -o sakamoto ./cmd/sakamoto
install -d -m 700 ~/.sakamoto
cp sakamoto.example.yaml ~/.sakamoto/sakamoto.yaml
cp nodes.example.txt ~/.sakamoto/nodes.txt
chmod 600 ~/.sakamoto/sakamoto.yaml ~/.sakamoto/nodes.txt
```

Follow the [macOS configuration and connection steps](README.md#install-and-connect-on-macos), using the repository-root templates copied above. Replace the API secret and example source/node paths before generation. Run `./sakamoto` and `./sakamoto setup` from the repository root so setup can locate its resources. The CLI also requires a separately installed compatible sing-box executable.

For a local macOS build copied to another location, apply an ad-hoc signature:

```sh
codesign -s - -f ./sakamoto
```

For desktop work, use a labeled demo with isolated fixtures:

```sh
mise run gui:demo
mise run gui:check
mise run gui:build
```

To run the built client against your own configuration:

```sh
mise exec -- python scripts/run-desktop.py
```

See the [desktop guide](desktop/README.md) for platform dependencies, external-core setup, artifact locations and native verification limits.

## Verification

Run the checks appropriate to your change:

```sh
mise run ci:check       # workflows, shell scripts, build-policy checks, English-only text, diff whitespace
mise run go:check       # shared Go tests, vet, pinned golangci-lint and executable build
mise run go:recovery    # isolated recovery faults with JSON receipts
mise run gui:check      # isolated desktop workflows, screenshots and JSON receipts
```

These are the tasks used by [Go and desktop CI](.github/workflows/ci.yml). `go:check` pins golangci-lint v2.14.0. Recovery artifacts live under `dist/recovery-e2e/`; desktop artifacts live under `dist/menubar-verification/`. Use isolated workflows for verification rather than changing a live VPN.

The [code quality review](docs/code-quality.md) records a historical analysis and refactoring plan. Its `fuck-u-code` scores are advisory: the shell parser may fall back to regex, and a score is not evidence of correctness. Current test and linter output determine the state of a new change.

## Packaging and mobile development

```sh
mise run cli:build                 # optimized macOS arm64/Intel CLI + resources
mise run gui:build                 # native tray client for the current OS
mise run icons                    # regenerate launcher icons from owner-provided artwork
mise run ios:build                # macOS + Xcode
mise run ios:package              # unsigned simulator .app zip
mise -E android install
mise -E android run android:build  # SDK, AARs and debug APK
mise -E android run android:install # install on an attached, USB-debugging-enabled device
```

`cli:build` writes binaries, setup scripts, launchd templates, example configurations, checksums and build metadata to `dist/cli/`. It requires macOS and Xcode command-line tools, keeps CGO enabled for upstream native TLS/certificate-store helpers, and verifies an ad-hoc signature. Release CLI binaries use `-trimpath`, stripped debug information and the shared [Go size policy](scripts/go-size-profile.json). Standard-library networking/crypto and TUI rendering retain their default inlining. Ordinary source builds remain available for debugging. Build tasks do not install services or change the live VPN.

Platform and release details have their own guides:

- [Android build, signing, device installation and limitations](android/README.md#build-and-release)
- [iOS build, signing, provisioning and packaging](ios/README.md#build-from-clean-verified-sequence)
- [Desktop release, signing, notarization and Homebrew cask](docs/desktop-release.md)
- [Public release checklist](docs/release-checklist.md)
- [Agent development notes](skills.md)

The macOS desktop release workflow requires Developer ID signing, Apple notarization and a stapled ticket before updating the separate `sakamoto-desktop` cask. The CLI formula remains independent.

## Private data and live-runtime changes

Do not commit generated binaries, real node links, subscription URLs, API secrets, generated `config.json`, `.srs` files, logs or learned-domain state such as `auto-proxy.json`. Keep private configuration out of screenshots, fixtures and issue reports. See the [configuration security reference](docs/configuration.md#security).

To stage an API credential without changing the current connection, run `sakamoto rotate-api`; the next normal TUI connect applies it. `sakamoto rotate-api --apply-now` explicitly accepts an immediate brief reconnect. Neither command prints the key.

Review [DNS activation](docs/dns-protection.md), [configuration repair](docs/configuration-repair.md) and [offline recovery](docs/stability-recovery.md) before modifying lifecycle behavior. The platform guides distinguish isolated verification from signed-device and privileged-service acceptance.
