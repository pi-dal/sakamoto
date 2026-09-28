# TUI guide

Launch with `sakamoto`. The status bar distinguishes *TUN started* from *network verified*. A node's latency only means its test URL was reachable; it does not prove the whole system route works.

## Home

- **连接 / 断开** toggles sakamoto TUN. Disconnect Shadowrocket VPN first; Tailscale can remain connected.
- The chain summary shows the current entry selection and SOCKS exit.
- **全部测速** tests each distinct protocol node rather than treating groups as nodes; wait for the final success/failure count.
- Left-click a node for manual selection. Right-click for **使用 / 测速 / 详情**. Wheel scrolls the list. A filled dot means selected, not reachable.

## Config

- **导入配置:** paste a Shadowrocket `.conf` URL or local path. Relative `include` files are merged automatically. Invalid or unavailable imports do not replace the last working config.
- **节点与订阅:** click a source row, then **编辑** or **删除**. Deletion requires a second click. Add a manual node by pasting a share link; it is stored in the local `nodes.txt` file. Node links and subscription URLs are masked in the edit prompt until you press **Ctrl+R**. **Ctrl+U** clears the current input before pasting a replacement. Rows show node names/types and do not reveal passwords.
- **更新生成** regenerates the sing-box JSON and `.srs` files; disconnect and reconnect before expecting the core to use the new configuration. If your source is remote, this also refreshes the source.

## Data

Shows traffic, connections and logs. Click a connection to inspect the route and optionally close it. Logs may contain server addresses, so avoid pasting them publicly without review.

## Settings

Click boolean switches or values to edit; `Enter` saves and `Esc` cancels. Relevant options:

- **未命中策略 (experiment):** `off` directs unmatched traffic, `on` proxies unmatched traffic, `auto` learns hostnames only after repeated corroborated direct timeouts and a healthy proxy. **直连失败阈值** defaults to 3; auto may briefly reconnect the TUN when a hostname is learned. See [configuration and limitations](configuration.md#experimental-unmatched-domain-policy). Regenerate and reconnect after changing modes.
- **自动回落链:** comma-separated priority, e.g. `RealityAuto,OthersAuto`; manual choice remains untouched.
- **系统代理（浏览器）:** requires **开启 HTTP/SOCKS**. While sakamoto is connected, the watcher saves and sets macOS HTTP/HTTPS proxy; on disconnect it restores the previous values. A user-specific `proxy-restore.json` is local-only.
- **iCloud 同步节点源:** off by default. Enabling requires a second confirmation because node links may contain credentials. Configure its directory/files in Settings and see [iCloud notes](icloud.md).

All changes are saved to `sakamoto.yaml`. Most generation/network changes need **Config → 更新生成** followed by a reconnect; the fallback watcher reloads policy settings on its next health interval.

## Keyboard fallback

`Tab`/`1–4`: tabs; arrows/`j,k`: move; `Enter`: select/edit; `Esc`: back; `c`: connect/disconnect; `u`: test all; `a`: import config on Config; `n`: add node on Config; `g`: regenerate; `q`: quit. The terminal must support SGR mouse input for pointer interactions (tested with Ghostty).
