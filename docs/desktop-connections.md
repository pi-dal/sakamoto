# Desktop live connections

The home header's **Connections** action opens active connection inspection on
macOS, Windows and Linux. The native core API supplies targets, application names
when available, outbounds, network types and uploaded/downloaded byte totals.
Search matches those fields and destination IPs. Results sort newest first.
This page does not close connections or change routing.

## Observation and protocol

The controller subscribes to `SubscribeConnections` only while the connection
page and panel are visible. Other pages retain their existing lighter observers.
A separate subscription keeps missing connection tracking from disabling node,
mode or traffic controls. `Unimplemented` shows tracking unavailable and parks
until navigation/session change; interrupted streams clear rows and reconnect.

The native initial reset contains active connections and closed history. Filter
both closed events and records with `closedAt`, and replace all cached rows on a
reset. New events project metadata; delta-only updates accumulate totals for
known IDs. Unknown updates cannot create phantom rows. Repeated full records
replace rather than duplicate rows. Closed events immediately remove the entry
from the model. Lifecycle bursts publish within 100 ms, limiting repeated sorting
and view snapshots. Traffic updates use the existing two-second interval.

Rows virtualize, so large live sets do not build thousands of rendered controls.
Snapshots copy the public value slice. View generations and runtime epochs reject
late results from cancelled pages/sessions. Hiding cancels observation and drops
connection rows, search text and engine-owned list bindings; reopening refreshes.
No destination history is written to disk. Process paths are reduced to their
executable name; process users and source addresses are excluded from the public
projection. Raw RPC errors remain internal. Test captures contain labeled demo
data only.

## Repeatable verification

```sh
mise run gui:check
SAKAMOTO_MENUBAR_ARTIFACT_DIR="$PWD/dist/menubar-verification" \
  mise exec sing-box@1.14.2 -- go test -race ./internal/desktop \
  -run TestLiveConnections -count=1 -timeout=30s
mise run gui:verify
```

- `connections-e2e.json`: real gRPC/controller workflow for reset/history,
  lifecycle/deltas, IPv6, copied snapshots, navigation cancellation, unsupported
  tracking, stream reconnect and idle visibility.
- `connections-native-e2e.json`: the actual pinned core proxies a loopback echo
  stream. Inspection sees its traffic; leaving the page or hiding preserves the
  same stream; a closed connection stays excluded after reopening. This test
  skips when no native executable is available; the commands above provide it.
- `connections-light.png` and `connections-dark.png`: input-driven shared view
  renders, including application filtering, empty states, 2,000 rows and Unicode.
- `macos-connections-light.png` and `macos-connections-dark.png`: packaged demo
  captures with capture provenance in `native-app.json`. A content snapshot
  cannot establish system popover positioning or platform input/reader behavior.

Fixtures use loopback services and temporary configuration. They do not create a
TUN, change system DNS, contact private endpoints or control the user's service.
Windows/Linux CI repeats the API and shared view workflows; native shell/IME/
screen-reader acceptance still requires those operating systems.
