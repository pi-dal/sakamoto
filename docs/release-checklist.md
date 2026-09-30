# Public repository readiness checklist

**This document is preparation, not authorization to publish.** Ask the owner to approve the repository name, license, and scope before creating a public remote, uploading attachments, or committing.

## Licensing

- The CLI directly imports `github.com/sagernet/sing-box/daemon` and `common/srs`. The upstream license is GPL-3.0-or-later; confirm a compatible license (proposed: GPL-3.0-or-later) and retain upstream copyright/notices.
- Do not imply endorsement by SagerNet or Shadowrocket. "Shadowrocket import" describes interoperability only.
- The embedded About portraits adapt a CC BY 2.0 photo by Joi Ito (Wikimedia Commons crop by Solid State Survivor). Keep the source, modification notice, license link and non-affiliation wording in About, NOTICE.md and [portrait-license.md](portrait-license.md). GPL covers code; the image assets retain their separate CC BY 2.0 terms.

## Secret audit

Never stage `~/.sakamoto` or `~/.config/sakamoto`, including API secret, nodes.txt, subscription tokens, config.json, `.srs`, logs, cache, iCloud hash state, learned auto-proxy domains, pending API keys and proxy restore state. The source tree should contain only documentation addresses and synthetic UUIDs.

Before any future commit:

```bash
git status --short
git ls-files | grep -E '(^|/)(nodes\.txt|config\.json|sakamoto\.yaml|proxy-restore\.json|dns-restore\.json|auto-proxy\.json|api-rotation\.pending\.json|watch\.sock|watch\.lock|.*\.srs|.*\.log)$' && echo 'STOP: private file tracked'
git grep -n -E 'token=[[:xdigit:]]{12,}|/Users/[^/]+/|password=[^[:space:]]{8,}|uuid=[0-9a-f-]{36}' -- ':!go.sum'
```

Manually inspect diffs and screenshots too: credentials may be base64 encoded in share links and will evade a plain grep. Do not publish actual IPs, API keys or user data in issue examples.

## Functional gate

- `go test ./...`, `go test -race ./...`, `go vet ./...`, pinned golangci-lint v2.14.0, `python3 scripts/check-english.py` and `go build ./cmd/sakamoto` pass.
- Fresh macOS install from `sakamoto.example.yaml` and `nodes.example.txt` is documented, with explicit root consent.
- Legacy migration does not interrupt an active VPN. New plist templates contain placeholders, not a real person's home path.
- Config import follows relative includes, fails closed on missing rule sets, and restores the previous working config after failure.
- TUI is tested with SGR mouse input at both 110x30 and 72x20; About/Copyright fits 64x16, 80x24 and 120x36 and its portrait works without terminal graphics or network fetches.
- Fallback checks fresh URL-test results; manual selection remains manual. The private `sakamoto recover` socket is single-watcher, user-only, rate-limited, and cannot start or restart the TUN.
- Optional iCloud sync never uploads generated state and never overwrites simultaneous edits.
- Protected system DNS is opt-in: UDP/TCP health before takeover, snapshot/read-back, restoration before listener stop, port-conflict/root-capability checks and rollback. No live DNS activation is claimed without the necessary administrative authorization.
