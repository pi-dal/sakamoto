# Mobile learning verification

## Failure cases before implementation

- Historical or unmatched log lines must not create candidates; log/connection streams may arrive in either order.
- Direct recovery, failure through both exits, HTTP 403/429/5xx, invalid TLS and unsupported destination ports must not save routes.
- Probe the selected native outbound, including its configured resolver and socket protection; never use an unprotected app URLSession/HTTP client.
- A successful forced proxy check does not prove ordinary routing: earlier user rules may take precedence after the live slot changes.
- Cancellation, stop/replacement, network/selector/mode changes and concurrent edits must prevent commit and restore the previous live domain set.
- An interrupted Android transaction must retain its recovery journal until restoration is durable.
- Repeated evidence must not create probe storms, permanent new listeners, status subscriptions or unbounded history.
- Learning and rollback must retain the core instance and an established TCP stream.
- Old iOS persisted runtime records must decode after verification history is added.

## Contract and repeatable evidence

Only correlated, unmatched direct TCP failures for observed domains on ports 80/443 are checked. The native core sends credential-free HEAD requests to the origin root, validates TLS with its own trust pool, never follows redirects, limits headers and uses a per-request deadline within a 25-second comparison budget. Success means HTTP 2xx/3xx, 401, 404 or 405; it proves origin reachability, not authenticated application health.

Shared Go policy compares both exits and actively confirms the configured failure threshold. Apps update only their dedicated live rule slot, verify the ordinary router before durable commit, restore that slot on failure, and keep a bounded local explanation history. Each domain has a five-minute cooldown; only one candidate is processed at a time. No permanent probe listeners or polling are added.

Run `python3 scripts/test-libbox-network.py` with `SAKAMOTO_TEST_ARTIFACT_DIR` to preserve the real-core verification report. Native iOS and Android scenarios also exercise their platform bridges and runtime transactions on isolated simulators/emulators, preserving JSON evidence under their test output/cache directories.
