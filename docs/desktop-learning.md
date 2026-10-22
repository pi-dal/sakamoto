# Desktop learning verification

## Failure cases to cover before implementation

- Automatic learning must never reconnect the TUN, including verification setup, success and rollback.
- A missing live rule slot remains pending until an explicit configuration apply; starting a watcher must not migrate a connected core.
- Long origin checks must leave groups and the recovery socket responsive; recovery reports `busy` while a learning update is in flight.
- A stopped/replaced core, edited configuration or uncommitted live file must prevent stale learning commit.
- A learned file change and rollback must retain the same native process and an existing TCP stream.
- An old millisecond value reaches the native API as nanoseconds and spins a core.
- Connection traffic deltas wake a learning consumer that only needs new/closed events.
- Historical connection/log replay becomes fresh failure evidence after reconnect.
- A target is unavailable through both direct and proxy paths; no rule should be saved.
- A direct target has recovered; no rule should be saved.
- A proxy returns a denial, challenge, rate limit or server error; this must not count as recovery.
- A useful proxy route is saved but the restarted core cannot actually reach the target.
- Probe configuration is missing, credentials are absent, or the destination is not HTTP(S).
- Repeated failures cause duplicate probes, reconnect storms or unbounded state.
- A setting is disabled while verification runs, or the process is canceled.
- A failed verification or restart must restore the previous learned set and generated config.
- Explicit DIRECT/PROXY/REJECT and private-network rules retain their authority for ordinary traffic.
- Passive failure evidence still requires an observed unmatched direct TCP connection.

## Verification contract

Desktop learning compares the observed HTTP(S) destination through private authenticated loopback listeners routed to the configured direct and proxy exits, then verifies the installed ordinary route. It requests only the root path, uses HEAD, does not follow redirects or send application credentials, and bounds time and response headers. A response proves only the tested origin's reachability, not an authenticated application's health.

Generation prepares a dedicated native local source rule set, `auto-proxy-rules.json`, immediately before the explicit proxy rule. The core watches this file and replaces matching for new connections. Learning writes this file, verifies the ordinary route, then commits `auto-proxy.json`; failure restores the prior file. It never writes `config.json`, reconnects the TUN, restarts a core, or replaces established connections. Watcher startup restores the committed file after an interrupted transaction, but never migrates a connected configuration. Missing listeners or a missing slot produce `deferred` until an explicit generation/apply. The API's start timestamp must be newer than the generated configuration, and must remain stable through commit.

Only one background verification runs at a time, with a 30-second global start limit and five-minute domain cooldown. The watch event loop continues reading groups and requests; recovery returns `busy` while verification runs. Normal API reconnect backoff resets after a stable session. No learning failure can trigger a restart/retry loop.

The integration fixture uses a real native core, isolated loopback SOCKS exits and an isolated runtime directory. It covers improvement, no improvement, direct recovery, rejection responses, rollback, a missing slot, historical replay and native subscription intervals. It asserts an unchanged API start timestamp and generated configuration, and echoes bytes over the same TCP connection before and after each learning outcome. The recovery fixture uses the real watcher Unix socket and slow origin exits to verify a prompt `busy` response. Set `SAKAMOTO_TEST_ARTIFACT_DIR` to retain JSON evidence. No owner configuration, TUN, system proxy or external website is used by the fixtures.
