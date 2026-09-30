# TUI guide

Launch with `sakamoto`. The status bar distinguishes *TUN started* from *network probe passed*. The probe checks two independent small HTTPS endpoints through macOS routing and, when configured, the local browser proxy entry; it retries with backoff after a failure. A single probe failure is **Unverified**, not a verdict that the network is down. A successful probe confirms only the displayed path, not every domain, TUN capture of excluded routes, DNS privacy, or the proxy's public exit IP. A node's latency likewise does not prove the whole system route works.

## Home

- **Connect / Disconnect** toggles sakamoto TUN. Disconnect Shadowrocket VPN first; Tailscale can remain connected. A pending `sakamoto rotate-api` key is applied before the next normal TUI connection (not while the TUN is already connected).
- The chain summary shows the current entry selection and SOCKS exit.
- Click **Mode** or press `m` to cycle **Rule → Global → Direct**. The TUI checks that the native core exposes the requested mode and verifies the result; an old generated config is reported as needing regeneration/reconnection rather than showing a false success. Global sends ordinary public traffic through the selected chain, including domains otherwise marked DIRECT. Direct sends ordinary public traffic directly. Both retain private/Tailscale exclusions, ad rejects and enabled STUN/QUIC blocks. Changing modes affects new connections; existing streams are not forcibly closed.
- **Test all** tests each distinct protocol node rather than treating groups as nodes; wait for the final success/failure count.
- Left-click a node for manual selection. Right-click for **Use / Test / Details**. Wheel scrolls the list. A filled dot means selected, not reachable.

## Config

- **Import config:** paste a Shadowrocket `.conf` URL or local path. Relative `include` files are merged automatically. Invalid or unavailable imports do not replace the last working config.
- **Nodes & sources:** click a source row, then **Edit** or **Remove**. Deletion requires a second click. Add a manual node by pasting a share link; it is stored in the local `nodes.txt` file. Node links and subscription URLs are masked in the edit prompt until you press **Ctrl+R**. **Ctrl+U** clears the current input before pasting a replacement. Rows show node names/types and do not reveal passwords.
- **Regenerate** regenerates the sing-box JSON and `.srs` files; disconnect and reconnect before expecting the core to use the new configuration. If your source is remote, this also refreshes the source.

## Data

Shows traffic, connections and logs. Click a connection to inspect the route and optionally close it. Logs may contain server addresses, so avoid pasting them publicly without review.

## Settings

Click boolean switches or values to edit; `Enter` saves and `Esc` cancels. Relevant options:

- **Unmatched policy (experiment):** a clickable three-state switch, not a text field. Click/Enter cycles `off → on → auto`; the highlighted option is selected. `off` directs unmatched traffic, `on` proxies unmatched traffic, `auto` learns hostnames only after repeated corroborated **unmatched direct dial timeouts** and a healthy proxy. It does not retry HTTP challenges or reroute a hostname already matched by a proxy rule. **Direct failure threshold** defaults to 3; auto may briefly reconnect the TUN when a hostname is learned. See [configuration and limitations](configuration.md#experimental-unmatched-domain-policy). Regenerate and reconnect after changing modes.
- **Fallback chain:** comma-separated priority, e.g. `RealityAuto,OthersAuto`; manual choice remains untouched.
- **System proxy (browser):** requires **Enable HTTP/SOCKS**. While sakamoto is connected, the watcher saves and sets macOS HTTP/HTTPS proxy; on disconnect it restores the previous values. A user-specific `proxy-restore.json` is local-only.
- **Sync node sources to iCloud:** off by default. Enabling requires a second confirmation because node links may contain credentials. Configure its directory/files in Settings and see [iCloud notes](icloud.md).

All changes are saved to `sakamoto.yaml`. Most generation/network changes need **Config → Regenerate** followed by a reconnect; the fallback watcher reloads policy settings on its next health interval.

## About → Copyright

The About tab includes a face-focused, high-resolution grayscale half-block portrait and explains the name's tribute to Ryuichi Sakamoto. Select **Copyright & attribution** (click or press Enter) for software copyright, separate image license, source credits and the non-affiliation statement. Scroll with `j/k`, arrows or the mouse wheel; `Esc` returns to About. The artwork is embedded text, works offline without Kitty/Sixel/iTerm2 image protocols, uses truecolor or ANSI-256 when available, and has a monochrome/ASCII fallback (`SAKAMOTO_ASCII_ART=1`). See the [portrait provenance and CC BY 2.0 license](portrait-license.md).

## Keyboard fallback

`Tab`/`1–5`: tabs; arrows/`j,k`: move; `Enter`: select/edit; `Esc`: back; `c`: connect/disconnect; `u`: test all; `a`: import config on Config; `n`: add node on Config; `g`: regenerate; `m`: cycle Rule/Global/Direct; `q`: quit. The terminal must support SGR mouse input for pointer interactions (tested with Ghostty).
