# Desktop configuration editor

The menu bar GUI now exposes the persistent scalar settings previously available
in the TUI Settings page. Open Settings, then **Edit configuration**.

## Groups

- Network: chain SOCKS exit, strict routing and TUN stack
- DNS: protected system DNS, network service and private suffixes
- Traffic policy: Cloudflare region handling, unmatched policy and failure threshold
- Privacy: STUN and QUIC blocking
- Local proxy: HTTP/SOCKS listener, browser proxy, service, LAN access and port
- Advanced: log level, latency URL, interval, tolerance, uTLS fingerprint, health
  interval and recovery threshold
- Cloud sync: iCloud and S3 settings, source paths and destinations
- Fallback: automatic fallback and the MainProxy fallback chain

API URL and API secret are not shown in the GUI. The API identity remains preserved
when a configuration is saved. Nodes, subscription sources and policy rules remain
in the TUI because they are collection editors with secret-aware input and source
file semantics. The GUI provides an Open TUI entry for those workflows.

## Save and activation boundary

Save validates the complete candidate using the shared configuration validators,
including API endpoint, DNS, experiment, policy, automatic group, S3 and source
path checks. It writes a temporary file beside the active sidecar, loads it again,
then atomically replaces `sakamoto.yaml`. A failed validation leaves the active
file byte-for-byte unchanged.

Saving does not restart or reconfigure the running core. The page reports that the
change needs regeneration. To apply it, open Check & repair and explicitly choose
regenerate and reconnect. A running service is never interrupted just by editing
or saving a field. External-core clients can save the sidecar, but service restart
and regeneration remain owned by the external service manager.

The editor does not print credentials or raw validation errors containing private
paths. It preserves the existing API secret and uses the same file mode as the
TUI configuration writer.

## Repeatable verification

```sh
mise exec -- go test ./internal/desktop -run TestConfigurationEditingEndToEnd -count=1
mise exec -- go test -C desktop ./... -count=1
mise run gui:check
```

The controller integration workflow verifies valid edits, API identity preservation,
atomic writes, invalid port and DNS rejection, and preservation of the active file.
The shared view workflow verifies group labels, toggle editing, save acknowledgement,
return navigation, and light/dark configuration renders.
