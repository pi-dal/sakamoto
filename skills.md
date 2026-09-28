# Agent guide

This file is intentionally short and safe to load into an agent context.

## Product

`sakamoto` is a macOS TUI/controller for sing-box. It is inspired by Shadowrocket's Home, Config, Data, and Settings workflows, but runs as a Go CLI with native sing-box gRPC control.

## Important invariants

1. Never commit user runtime files or credentials. Fresh-install runtime lives under `~/.sakamoto`; a live legacy daemon may still use `~/.config/sakamoto` until explicit migration.
2. Do not reintroduce subscription URLs or node secrets into source, fixtures, README, screenshots, or tests.
3. A SOCKS exit is chained through `MainProxy` with sing-box `detour`; do not silently change chain direction.
4. `fallback_enabled` controls the ordered `RealityAuto -> OthersAuto` policy. Fresh URL-test results must be consumed before switching. Manual selector choices outside that chain are respected.
5. The macOS TUN stack defaults to `gvisor` on this machine. `system` was able to start but did not provide working browser TCP routing.
6. Browser support depends on `system_proxy.enabled: true` and `mixed_inbound.enabled: true`; the watcher restores prior macOS proxy settings on disconnect.
7. Shadowrocket and sing-box must never have active TUN/VPN sessions at the same time. The daemon checks this and stops sing-box if Shadowrocket reconnects.
8. Importing a Shadowrocket conf must follow relative `include` files and fail closed if an include or remote rule list cannot be loaded.
9. Test status vocabulary is meaningful: `待测`, `测试中`, `可达 Nms`, `失败/超时`; a selected dot is not a connectivity claim.
10. iCloud sync is opt-in and limited to explicitly named source files; never sync `sakamoto.yaml`, generated `config.json`, logs, sockets, or API credentials. Never overwrite simultaneous edits.
11. Use `pnpm`/PDM/mise preferences where applicable; Go dependencies use the existing `go.mod`.

## Verification

```bash
go test ./...
go vet ./...
sing-box check -c ~/.sakamoto/config.json
```

Use a PTY with a real terminal size to test mouse input. The TUI enables SGR mouse all-motion and supports left click, right-click node menus, wheel scrolling, bracketed paste, and Tab navigation. Validate at least 110x30 and 72x20.

## Safe publication checklist

- `git grep` for subscription tokens, UUIDs, passwords, Reality keys, SOCKS credentials, private server addresses;
- confirm `.gitignore` covers `~/.sakamoto` exports, generated JSON, `.srs`, logs, and binaries;
- replace real fixtures with RFC 5737/RFC 3849 documentation addresses;
- publish docs and examples, not the owner's configuration;
- ask the owner before creating or pushing a public GitHub repository.
