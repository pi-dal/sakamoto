# Stability and offline recovery

The desktop recovery mechanisms in this document ship in CLI/TUI **0.2.18**. Installing a CLI does not replace an already-running supervisor or watcher. Use `sakamoto doctor` from the new build to inspect actual running capabilities before claiming activation.

**0.2.21** adds explicit configuration fallback/regeneration and a controlled supervisor update. The [configuration repair guide](configuration-repair.md) covers commands, GUI confirmation, version boundaries and verification evidence.

The owner must be able to recover locally when the proxy, DNS, watcher, native API, or root supervisor is unavailable. Recovery must not require a working proxy, online package manager, rebuild, or online assistant.

## Quick core repair

Use one command after a core crash or update failure:

```sh
sakamoto repair
sakamoto repair --check
sakamoto repair --restart
```

`repair` probes the actual native API and configured protected DNS, retains a healthy process, and starts an exited core. The new supervisor resets a blocked crash episode for this explicit manual attempt and can restore one complete known-working bundle if the current generation cannot start. Prepared inputs that differ from the last applied runtime require the retained bundle rather than being silently activated. Missing retired core executable paths fall back to the installed stable location/PATH before startup validation.

`--check` performs no lifecycle or service changes. A live process with an unavailable API or DNS is reported as degraded; default repair retains it. `--restart` explicitly reconnects such a process, waits for its owned predecessor to exit, and can terminate that owned predecessor after a bounded graceful-stop window if it is hung. An older supervisor gets a compatible connect/disconnect sequence and real API/DNS verification; it cannot provide the new supervisor's atomic recovery or bundle rollback capabilities.

If the supervisor itself is unavailable, repair can start its identified installed launchd job and wait for the socket, then repair the core. Local administrator authorization is requested only for the required launchd operation. It never uses `kickstart -k`, replaces a plist, downloads software, clears a maintenance hold, or disables DNS protection as a repair shortcut. A healthy but unresponsive supervisor is not blindly killed. Recovery reports verified success separately from process startup, returns a nonzero exit on incomplete repair, and preserves failures for local inspection. Verification covers the core API and protected DNS, not every remote website.

`recover` remains the watcher-owned node-testing command. Use `repair` for the core process; use `recover` when fresh node health tests are needed.

## Independent rescue

The CLI package includes `scripts/sakamoto-rescue`, built separately with CGO disabled. It does not link libbox, the TUI, or the core API client. The privileged installer copies it outside Homebrew and preview directories:

```text
/Library/Application Support/Sakamoto/rescue/sakamoto-rescue
```

Install while the system is healthy:

```sh
sakamoto rescue-install --check
sakamoto rescue-install
```

This installs the rescue executable only. It does not reload a service or change VPN, DNS, or proxy state. Existing rescue copies are retained by default; `--replace` deliberately replaces one and keeps `sakamoto-rescue.previous`. Fresh `sakamoto setup` includes rescue installation before launchd services are bootstrapped. Source builds can supply a locally built, signed executable through `SAKAMOTO_RESCUE_BIN` to `scripts/install-rescue.sh`.

Keep these commands locally available; adjust the runtime path for a legacy or custom installation:

```sh
# Read-only; no administrator, DNS, or external network needed.
"/Library/Application Support/Sakamoto/rescue/sakamoto-rescue" diagnose --dir "$HOME/.sakamoto"

# Explicit emergency exit from VPN protection. Requires local administrator authorization.
sudo "/Library/Application Support/Sakamoto/rescue/sakamoto-rescue" exit --dir "$HOME/.sakamoto" --restore-network

# Open a user shell without inherited HTTP/HTTPS/ALL_PROXY variables or user startup scripts.
"/Library/Application Support/Sakamoto/rescue/sakamoto-rescue" shell
```

Emergency exit is an explicit choice to restore ordinary networking through the original settings. It is never invoked by an automatic health check. Physical uplink failure, managed network restrictions, missing original settings, or an unrelated VPN can still prevent connectivity; the tool reports the phases it actually completed.

The exit operation:

1. Inspects the new and known legacy launchd jobs, verifying that every loaded target identifies the requested runtime. An unidentified or different runtime is rejected before any job is unloaded.
2. Persists `maintenance.json`, then copies private restore/configuration evidence into a private `rescue-evidence-*` directory.
3. Validates restore state. Missing original state with a configured dead loopback resolver/proxy is an error, not an instruction to guess DHCP or public DNS.
4. Tries cooperative cancellation/restoration through a new supervisor, but does not depend on it responding.
5. Unloads this installation's identified watcher/supervisor jobs through launchd and waits for confirmation. It never kills processes by name or trusts a PID supplied through a user-owned socket.
6. Restores original DNS and HTTP/HTTPS proxy settings directly with macOS tools. DHCP versus explicit DNS is preserved. Read-back must confirm writes; explicit external overrides are retained.
7. Writes a completion receipt and leaves automatic takeover paused. A second exit is safe to repeat.

The cooperative path restores DNS before intentionally stopping its working resolver. The independent fallback can interrupt the already-failing VPN while unloading its job; it remains usable without that resolver. Corrupt evidence, failed OS commands, unconfirmed job termination, or partial restoration produce a nonzero exit with per-phase results. Snapshots are retained until their corresponding restoration is verified.

The clean shell affects its own environment only. It does not modify an existing shell or running agent, remove a TUN, or restore OS network settings. For an existing shell:

```sh
unset http_proxy https_proxy all_proxy HTTP_PROXY HTTPS_PROXY ALL_PROXY
```

## Resume deliberately

A successful emergency exit unloads the services and keeps maintenance held. Repair or upgrade the application while ordinary networking is available, then load the installed jobs explicitly:

```sh
sudo /bin/launchctl bootstrap system /Library/LaunchDaemons/dev.sakamoto.daemon.plist
/bin/launchctl bootstrap "gui/$(id -u)" "$HOME/Library/LaunchAgents/dev.sakamoto.watch.plist"
sakamoto doctor
sakamoto ctl resume
```

Bootstrap only jobs that are unloaded; an already-loaded job should be inspected instead of duplicated. Legacy jobs without a verifiable runtime require the existing migration procedure rather than blind unloading. Resuming does not start the VPN; connect explicitly afterward. The new supervisor can also cancel a failed rescue hold while retaining a still-running core through `ctl resume`.

## Bounded crash recovery

Unexpected core process exits permit at most three automatic retries, delayed by 2, 4, and 8 seconds. Failed starts consume the same budget. This is a consecutive-failure episode, not a rolling two-minute window that could allow slow failures forever. `crash-recovery.json` survives supervisor replacement.

When the budget is exhausted, a valid known-working runtime may be restored and started once. If that restored process fails, recovery becomes `blocked` immediately. A missing, unsafe, corrupt, or incompatible bundle leaves recovery blocked. An automatic rollback cannot disable protected DNS while a protected restore snapshot remains.

An unchanged runtime must remain alive for five minutes, present the same fresh native API epoch before/after that interval, and pass protected DNS readiness again when enabled before its budget resets and a new stable receipt is promoted. Changed or staged inputs are not silently promoted. A website failure or lost API stream alone never restarts an otherwise-running core.

Manual stop cancels startup/readiness/reload work before waiting on lifecycle serialization. Cancelable DNS probes and OS command deadlines prevent indefinite waits. A stale retry cannot undo a successful manual stop. If DNS restoration fails, the core and snapshot remain; monitoring still recovers a later unexpected core crash unless an explicit rescue maintenance hold is present.

Status remains available while startup or orphaned-DNS cleanup is waiting. Status distinguishes disconnected maintenance, backoff and blocked conditions. A connected process and `dns=protected` are process/lifecycle observations, not an end-to-end reachability claim.

## Complete known-working bundles

Private `recovery/bundles/runtime-*` directories include:

- The applied configuration and sidecar.
- Captured local rule sets and file-based TLS inputs, including relative paths rewritten to retained assets.
- Matching committed learned state and live learned rules.
- A copy of the executable actually used, retained through its open file descriptor even if Homebrew removes its original path.
- Process/native epoch evidence and SHA-256 checksums for all files.

The snapshot is captured before launch and promoted only after the stable interval with unchanged inputs. A newer file on disk is not evidence that the running core loaded it. If a prepared generation differs from the last applied inputs, an automatic retry refuses to activate it. Use explicit activation after diagnosis.

Rollback verifies the entire bundle, saves the rejected candidate privately, restores the referenced files/settings, and pins the matching executable. It does not assume a newer installed engine is compatible with an older configuration. Learning commit, learning rollback and runtime restoration use the same short file-transaction lock; a superseded worker cannot overwrite restored rules or save stale metadata. Originally absent learned state is restored as absent. Keep two stable bundles plus bundles referenced by active/staged files or the held executable.

```sh
sakamoto ctl disconnect
sakamoto ctl rollback
sakamoto ctl connect
```

A second rollback in the same failed episode is rejected. `ctl resume` resets the episode while retaining an existing engine pin. After inspecting a new candidate, `ctl resume-installed` explicitly selects the installed engine again; it requires a disconnected core. Maintenance holds remain independent of rollback and must be released deliberately.

These files contain machine credentials and must remain local. The iCloud source allowlist rejects recovery state, bundles, and evidence directories. Mutable cache and Tailscale state directories remain in the runtime; they are not rolled back as immutable rule inputs.

## Running identities

```sh
sakamoto doctor
sakamoto ctl info
sakamoto ctl status
```

The new supervisor advertises protocol 2, build identity, running core path/PID, applied-input fingerprint, circuit state and stable-receipt presence. The watcher independently reports its version, maintenance state and actual API-stream connection. The CLI reports its own version separately. Old `unknown: info` responses become an upgrade requirement; no installed-version label is treated as proof of running capabilities.

Installed-version updates and privileged-service updates are separate operations. Stage and verify rescue/rollback first, then schedule supervisor replacement and its required controlled reconnect. A watcher-only update does not need to restart a healthy TUN.

## Existing macOS fallback

If the supervisor still responds but the CLI/TUI/watcher is unavailable, native netcat can request a normal disconnect:

```sh
printf 'status\n' | /usr/bin/nc -w 3 -U "$HOME/.sakamoto/svc.sock"
printf 'disconnect\n' | /usr/bin/nc -w 35 -U "$HOME/.sakamoto/svc.sock"
```

The second command intentionally interrupts the VPN. An empty reply, timeout or restoration error is not success. This path still depends on the supervisor. Preserve a failed DNS snapshot and inspect it locally when needed:

```sh
sudo /usr/bin/plutil -p "$HOME/.sakamoto/dns-restore.json"
```

System Settings > Network > the affected service > Details exposes DNS and proxy controls independently. Restore the recorded original values; do not assume that DHCP, a public resolver, or disabling an unrelated corporate proxy is correct.

## Verification and limits

The repeatable desktop E2E lane runs real child processes, actual Unix sockets, a real native core with loopback-only traffic, and separate subprocess fixtures for macOS network/launchd commands. It never kills the owner's live VPN or modifies their network settings.

```sh
SAKAMOTO_RECOVERY_ARTIFACT_DIR=/tmp/sakamoto-recovery-e2e \
  go test -race ./cmd/sakamoto-rescue ./internal/svc ./internal/rescue ./internal/watch \
  -run 'TestRecovery|TestOffline|TestMaintenance|TestRescueShell|TestRepair' -count=1 -timeout=90s
```

Scenarios cover persisted crash budgets, canceled DNS startup, responsive orphan cleanup, failed DNS restoration with retained crash monitoring, maintenance across server replacement, missing local rules, real pinned-engine rollback, continued TCP traffic during API failure, failed rollback blocking, all application processes absent, repeated rescue, external overrides, corrupt/missing snapshots, wrong-runtime refusal, stale learning after rollback, clean-shell startup isolation and unprivileged exit rejection. JSON artifacts record the actual outcomes. CI runs `mise run go:recovery` and uploads those receipts.

Actual privileged launchd bootout and OS network restoration still require an isolated macOS fault exercise. Fixture results are not a claim that this privileged exercise has happened on the owner's current network. Root helper installation needs local administrator authorization; privileged-service activation requires a controlled deployment.

Mobile clients share the recovery policy through the combined native Go bridge. See [Mobile stability and repair](mobile-stability-recovery.md) for mobile lifecycle fencing, retained configuration assets, repair entry points, native verification, and OS limits. Mobile recovery remains working-source functionality until new apps are released. Android system Always-on/lockdown policy must be changed through system settings when it intentionally blocks non-VPN traffic.
