# Generic proxy chain exits

CLI/Desktop 0.2.26, iOS 0.1.26 (35) and Android 0.1.20 (22) allow a leaf proxy node to be selected as the chain exit, including VLESS, VMess, Trojan, Shadowsocks, SOCKS, HTTP, Hysteria2 and TUIC. The native core still validates protocol options and transport compatibility.

For a host-generated configuration, add both entry and exit nodes to the node sources and select the exit by its exact tag:

```yaml
chain_enabled: true
chain_exit: Landing
```

Traffic follows `device → MainProxy entry → Landing → destination`. The exit is excluded from automatic and manual entry groups. With chaining disabled, proxy traffic uses MainProxy directly. An empty `chain_exit` preserves the legacy first-SOCKS selection; `socks_exit` remains an explicit compatibility alias. `chain_exit` takes precedence. If an explicit tag disappears, generation fails instead of silently selecting a replacement.

Desktop exposes **Chain exit** in its configuration editor. Save the change, then use **Apply saved configuration**. CLI users regenerate through their normal workflow or use `sakamoto repair --regenerate --restart`. An installed supervisor without `generic-chain-exit-v1` must be updated before it can apply an explicit exit; the desktop offers **Update service and apply**.

iOS and Android expose **Proxy chain** in Config. Choose the upstream and exit, enable the chain and save, then apply through the existing configuration workflow. Their saved base profile retains group membership so switching or disabling the chain can restore it. Existing imported chained configurations are recognized; group membership already missing from an imported snapshot cannot be reconstructed.

## Failure cases and acceptance

- An exit selected as an entry creates a cycle. Exclude the chosen exit from entry groups and their defaults before validation.
- A missing exit, self reference, duplicate/reserved tag, missing dependency, or indirect cycle must fail preparation without replacing the current configuration.
- An explicitly selected exit with no remaining entry must fail. Never silently dial an explicitly chained exit directly.
- Keep existing automatic SOCKS selection order when `chain_exit` is empty; accept `socks_exit` as the legacy explicit alias.
- Changing or disabling a mobile chain must start from the saved unchained profile, retaining original memberships, defaults, credentials and advanced protocol settings.
- Regeneration must retain the selected exit intent. Mobile node renames update that intent; missing nodes fail validation instead of selecting another exit.
- An older supervisor that can regenerate but cannot honor `chain_exit` must require an upgrade before any reconnect or regeneration request.
- PROXY, Global, ordinary proxy DNS and learned proxy routes use the selected exit. DIRECT/REJECT/private rules retain their policy; nested route rules are included.
- A group latency test proves entry health only. It must not be presented as complete chain health. Entry failover does not repair a failed landing server.
- TCP-based exits such as VLESS, VMess and Trojan require TCP from their upstream. QUIC-based exits such as Hysteria2/TUIC require usable UDP through their upstream. Native validation and end-to-end checks remain necessary; a valid schema alone proves no network reachability.
- Saving in CLI/Desktop/mobile remains separate from applying or connecting.

## Verification

```sh
SAKAMOTO_CHAIN_ARTIFACT_DIR="$PWD/dist/chain-e2e" mise exec sing-box@1.14.2 -- go test ./internal/gen -run TestNativeGenericChainExit -count=1
mise exec sing-box@1.14.2 -- swift run --package-path ios ChainScenario "$PWD/dist/chain-e2e"
```

After building/installing the Android debug and instrumentation APKs on an isolated emulator, run `adb shell am instrument -w -e chain true com.pidal.sakamoto.test/com.pidal.sakamoto.NativeBindingSmokeTest`. Its private cache contains `android-chain.json`. The iOS Xcode UI test is `SakamotoEditorUITests/NodeGroupUITests/testVLESSChainExitSavesReopensAndDisables`; use a signed simulator build so App Group access is available, and set simulator appearance through `simctl ui` when checking both themes. Keep its xcresult bundle and screenshots.

Repeatable native scenarios use only loopback listeners and synthetic credentials, without a TUN or owner configuration. Retain JSON evidence under `dist/chain-e2e`. Cover VLESS-to-VLESS traffic, unavailable exits, entry-group exclusion, DNS/rule projections, legacy SOCKS selection, switching/disabling intent, and rejected cycles. Platform builds verify all app call sites. Release artifacts must describe the same clean source commit and pass signing/version/hash checks.
