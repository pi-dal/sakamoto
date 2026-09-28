# Public repository readiness checklist

**This document is preparation, not authorization to publish.** Ask the owner to approve the repository name, license, and scope before creating a public remote, uploading attachments, or committing.

## Licensing

- The CLI directly imports `github.com/sagernet/sing-box/daemon` and `common/srs`. The upstream license is GPL-3.0-or-later; confirm a compatible license (proposed: GPL-3.0-or-later) and retain upstream copyright/notices.
- Do not imply endorsement by SagerNet or Shadowrocket. "Shadowrocket import" describes interoperability only.

## Secret audit

Never stage `~/.sakamoto` or `~/.config/sakamoto`, including API secret, nodes.txt, subscription tokens, config.json, `.srs`, logs, cache, iCloud hash state and proxy restore state. The source tree should contain only documentation addresses and synthetic UUIDs.

Before any future commit:

```bash
git status --short
git ls-files | grep -E '(^|/)(nodes\.txt|config\.json|sakamoto\.yaml|proxy-restore\.json|.*\.srs|.*\.log)$' && echo 'STOP: private file tracked'
git grep -n -E 'token=[[:xdigit:]]{12,}|/Users/[^/]+/|password=[^[:space:]]{8,}|uuid=[0-9a-f-]{36}' -- ':!go.sum'
```

Manually inspect diffs and screenshots too: credentials may be base64 encoded in share links and will evade a plain grep. Do not publish actual IPs, API keys or user data in issue examples.

## Functional gate

- `go test ./...`, `go test -race ./...`, `go vet ./...`, pinned golangci-lint v2.14.0 and `go build ./cmd/sakamoto` pass.
- Fresh macOS install from `sakamoto.example.yaml` and `nodes.example.txt` is documented, with explicit root consent.
- Legacy migration does not interrupt an active VPN. New plist templates contain placeholders, not a real person's home path.
- Config import follows relative includes, fails closed on missing rule sets, and restores the previous working config after failure.
- TUI is tested with SGR mouse input at both 110x30 and 72x20.
- Fallback checks fresh URL-test results; manual selection remains manual.
- Optional iCloud sync never uploads generated state and never overwrites simultaneous edits.
