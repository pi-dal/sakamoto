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

Homebrew normally links the formula automatically. `sakamoto link --check` compares the stable command against the installed Homebrew executable without changing files. `sakamoto link` repairs missing commands, stale symlinks and retained old regular executables with `brew unlink sakamoto` followed by `brew link sakamoto`. Existing executables are renamed to a unique `.sakamoto-link-backup-*` sibling before repair; failure restores them where possible, and a successful repair reports the retained backup. Directories and special files are refused. Homebrew-installed `setup` performs the same repair after service/config preflight; `setup --check` stays read-only and source builds retain their own executable. Neither operation restarts a running service. If the stale executable itself predates this command, invoke the new keg explicitly: `"$(brew --prefix sakamoto)/bin/sakamoto" link`.

## Issue checklist

| # | Issue | Implementation / remaining verification |
|---|---|---|
| 1 | Whole sidecar crosses machines | Source bundle uses an explicit whitelist. Shared nodes, policy, subscriptions, conf and automatic-group exclusions travel; machine paths, API/endpoint identities and cloud credentials stay local. |
| 2 | Proxy endpoint enters TUN | Generation collects outbound IPs and bounded-resolves domains; mobile IP edits also repair exclusions. DNS snapshots need regeneration after address changes. |
| 3 | URL-test timing | Native RPC is asynchronous, and seven members share one ten-worker batch. Watcher now waits for the native measurement budget and retains selection on acknowledgement failure. Actual JP Lite failures still require live evidence. |
| 4 | Old Homebrew command | Read-only `link --check`, backed-up `link` repair and automatic repair in Homebrew setup. Host link was already current; sandbox repairs cover stale/missing/file cases. |
| 5 | Android S3 not configured | Client support exists. Provider endpoint, region, private bucket/prefix and scoped credentials are still needed for live activation. |
| 6 | iCloud dataless preflight | Request download and defer unavailable files; pending conf defers its rule graph while independent nodes can sync. |
| 7 | Snapshot latency appears current | Node age/stale labels and detailed last-test timestamp; request failure is distinct from absent current history. |
| 8 | DMIT transport loss | Watcher already defaults to a forced check every 30 seconds. Native URLTest offers one configured target per automatic group; multiple-target probing is not implemented. No live cause or successful mitigation is claimed. |
| 9 | Limited supervisor diagnostics | `ctl state`, `urltest`, `select`, and validated `reload` are implemented. |
| 10 | Watcher Homebrew PATH | Template contains both Homebrew prefixes and the installer-resolved core path; existing services need their environment reloaded. |
| 11 | Absolute SRS paths | Generated paths are runtime-relative; check/run with the same `-D` directory and copy the rules folder with the config. |

Android supports S3 source sync, but a private provider endpoint, region, bucket/prefix and scoped credentials must be configured before live sync can be verified. No bucket was created by this change. Intermittent DMIT transport loss requires actual connection evidence; longer result windows improve classification but do not repair the transport path, and no outage cause is inferred from URL-test history alone.
