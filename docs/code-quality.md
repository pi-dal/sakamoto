# Code Quality Review

## Summary
The main maintainability risk was two oversized, mixed-responsibility Go files; unchecked I/O errors also hid real configuration-generation failures.

## Overall Assessment

| Metric | Before | After |
|---|---:|---:|
| `fuck-u-code` project score | 65.9/100 | 77.6/100 |
| Source files analyzed | 23 | 31 |
| Files below 60/100 | 3 | 0 |
| `golangci-lint` (v2.14.0 default linters) | 36 findings | 0 findings |

The Go parser was repaired locally for the quality pass. The tool's shell parser still falls back to regex (`resolved is not a function`), so the Shell scores are advisory; tests and staticcheck determine correctness.

## Key Issues (sorted by severity)

- **`Run` (`internal/gen/gen.go:31`)**: Generation still has a maximum cyclomatic complexity of 62. Extract DNS assembly and config serialization into small typed functions next.
- **`buildRules` (`internal/gen/rules.go:19`)**: Complexity 37 from bucket compilation, GeoIP download, and direct overrides. Split compilation and data acquisition when adding another rule source.
- **`View` (`internal/tui/view.go:149`)**: The rendering branch remains dense. Keep hitbox calculations co-located with their corresponding component renderer rather than adding more cases to `View`.

## Refactoring Plan

1. Extract DNS server/routing construction from `internal/gen/gen.go:31` into a pure function with fixture tests.
2. Split GeoIP download from SRS compilation in `internal/gen/rules.go:19` and add bounded HTTP mocks.
3. Move TUI renderers in `internal/tui/view.go` into per-tab components when a new tab or modal is introduced.

## Security Concerns

- No real nodes, subscription tokens, SOCKS credentials, or generated configs are tracked in git. User state stays in `~/.sakamoto` (legacy state stays in `~/.config/sakamoto` until migration).
- Imports **and refreshes** now snapshot last-working `config.json`/rules, validate with `sing-box check`, and roll back after failure. Configured subscriptions fail closed instead of silently disappearing when an endpoint is down.
- New installs get a random API secret; the installer rejects example secrets. Existing users should rotate any legacy placeholder secret during a planned restart.
- Protected system DNS uses sing-box-native UDP/TCP interception, certificate-verified proxy DoH and separate encrypted bootstrap. Root DNS snapshot/read-back/restore and legacy-daemon capability are tested; administrative activation is explicit, never claimed from a CLI update alone.
- iCloud sync is opt-in and scoped to explicit source paths and the configured local conf/include graph; runtime state and API credentials are excluded.
