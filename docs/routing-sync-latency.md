# Routing, source sync and latency checks

Proxy server IPs are automatically excluded from desktop and mobile TUN routes. Generation collects every outbound `server`, including subscriptions and SOCKS exits. Literal IPv4/IPv6 addresses become `/32` or `/128` routes and are deduplicated against existing exclusions. Generation also resolves hostnames in both address families with at most four simultaneous lookups and a three-second total budget. Failed lookups are reported and literal routes remain. Resolved routes are a DNS snapshot: regenerate after endpoint addresses change. Mobile node IP edits update exclusions before saving or applying, without requiring regeneration.

Generated local rule-set paths use `rules/<name>.srs`. Move `config.json` together with its `rules` folder, and run the core with `-D /absolute/runtime/directory`. Setup, import, migration and validation use the same directory contract.

## Why the RPC deadline is not the group test deadline

The pinned native core v1.14.2 `daemon/started_service.go` starts URL tests in goroutines and immediately acknowledges the gRPC request. Group tests run under the core's service context; cancelling the RPC acknowledgement does not cancel those tests. `protocol/group/urltest.go` uses ten concurrent workers and a 15-second `constant.TCPTimeout` per group member. The explicit automatic-group entry point forces a refresh. Seven members therefore occupy one concurrent batch; they do not require seven sequential 1–3 second requests.

The previous watcher waited at least ten seconds before evaluating fresh history. That could evaluate while a native 15-second probe was still running. The result window now covers `ceil(unique leaf members / 10) × 15 seconds + 5 seconds` for the largest tested group. The RPC acknowledgement has its own ten-second limit. A failed request invalidates the round and retains the current selection rather than interpreting an RPC failure as node unhealthiness. Native histories have whole-second timestamps, so a new round starts after the next second boundary.

TUI `t` uses a bounded acknowledgement. `u` waits until all leaf requests have been dispatched before starting its result-expiry window. Individual leaf requests lack the group's surrounding timeout and can include dial, HTTP and multiplex warm-up phases, so the leaf-result window is 65 seconds. Repeated identical latency with a new timestamp counts as fresh; unchanged cached history does not. No current result and request failures are displayed separately. Node rows show age or stale status; details show the last test's local timestamp.

## Supervisor diagnostics

The existing owner-restricted socket accepts the following controls through the CLI, including tags containing spaces:

```sh
sakamoto ctl state
sakamoto ctl status
sakamoto ctl logs
sakamoto ctl urltest "JP Lite"
sakamoto ctl select "MainProxy" "RealityAuto"
sakamoto ctl reload
```

`urltest` queues background work; inspect later state to see results. `reload` validates the configuration before requesting native SIGHUP reload and reports only that the request was sent. Changing protected DNS requires a disconnect/reconnect so its saved DNS lifecycle stays consistent. State exposes groups and latency history, not API credentials. `--config /absolute/path/sakamoto.yaml` selects that directory's supervisor socket.

The watcher launchd template includes both Homebrew prefixes in PATH and the installer-resolved `SAKAMOTO_SING_BOX` path. This affects newly rendered templates; existing services require an explicit service upgrade to load changed environment variables.

## Remaining operational setup

Cross-device synchronization shares source files plus `groups.json`, not the whole machine's `sakamoto.yaml`. Each device retains its own directories, API secrets, Tailscale identity, local overrides and cloud credentials. See [iCloud](icloud.md) and [S3](s3-sync.md) for conflict behavior and configuration. Dataless iCloud files are requested for download and deferred without blocking independent sources or advancing their baseline.

Homebrew normally links the formula automatically. Check `command -v sakamoto`, `ls -l "$(brew --prefix)/bin/sakamoto"`, and `"$(brew --prefix sakamoto)/bin/sakamoto" version` if the active version differs. A link conflict should be inspected with `brew link --overwrite --dry-run pi-dal/tap/sakamoto` before deliberately replacing the conflicting file; setup does not overwrite an unrelated executable or change a running service.

Android supports S3 source sync, but a private provider endpoint, region, bucket/prefix and scoped credentials must be configured before live sync can be verified. No bucket was created by this change. Intermittent DMIT transport loss requires actual connection evidence; longer result windows improve classification but do not repair the transport path, and no outage cause is inferred from URL-test history alone.
