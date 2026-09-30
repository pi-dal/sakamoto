# Optional macOS iCloud Drive sync

Generated state stays local. Once source sync is explicitly enabled, sakamoto can sync manual node links **and the actual local rule conf with its relative conf includes**:

```yaml
icloud:
  enabled: true           # still off by default; enabling needs confirmation
  include_conf: true      # local conf + recursive relative .conf includes
  directory: /absolute/path/to/iCloudDrive/sakamoto
  files: [nodes.txt]       # additional source paths; conf discovery is separate
```

## What is synchronized

**Settings → Sync sources to iCloud** enables the feature. **Include conf and rule includes** controls rule discovery. It defaults to true for fresh configurations but cannot upload anything while overall sync is disabled. Existing iCloud configs without this field retain nodes-only scope until you explicitly set `include_conf: true` or confirm the new control; an upgrade never expands old consent silently. The watcher reloads settings and checks about once a minute; no VPN restart is required for syncing sources.

If `conf:` points at `~/.sakamoto/sources/current/macOS.conf`, the cloud copies keep that relative layout:

```text
sakamoto/
├── nodes.txt
└── sources/current/
    ├── macOS.conf
    └── sr_top500_banlist_ad_5_25.conf
```

Relative includes are discovered recursively on **both sides**, so a cloud-only conf or a cloud edit that introduces a new nested include downloads its dependencies too. Include paths stay within the conf's source directory, with cycle/depth/size limits. For a local conf outside the runtime directory (e.g. Downloads), the cloud copy uses `conf/<filename>.conf` and keeps relative subdirectories. To share across Macs, use matching runtime-relative paths or the same external main filename; sync does not change `conf:` or select another rule profile for you.

`files:` remains the explicit list for nodes and other source paths. Safe relative subdirectories are supported; **Additional source paths** in Settings edits this list. The conf and its includes do not need to be manually listed. To keep the old nodes-only behavior, set `include_conf: false`.

URL-only main configs and remote HTTP(S) includes/`RULE-SET` lists remain importer-managed network sources; sync does not upload their URLs, download them itself, or pretend a cached generated snapshot is the complete source. Import a local `.conf` with local dependencies if you want a fully shared rule-source bundle.

## Conflicts and safety

- One-sided first-use files are copied; identical files establish a shared hash baseline.
- Different copies without a common baseline, or simultaneous edits on both sides, stop the pass without overwriting either copy. The whole source graph is preflighted before copying unrelated files.
- Deletions are not propagated or silently restored; resolve them manually. A missing required include stops the pass.
- Symlinks, traversal, hidden paths, generated JSON/SRS/database files, sockets/locks, logs and known API/credential filenames are rejected, including at nested paths.
- The local-only `icloud-state.json` tracks completed copies. A later filesystem failure may stop the pass; successful copies keep their baseline and the error is reported.
- Synced files are written atomically with mode `0600`; new source subdirectories use `0700`.

Never sync the entire runtime folder. `sakamoto.yaml`, `config.json`, API rotation state, learned domains, `.srs`, watch/daemon sockets and logs must remain local. Downloading a source change does **not** regenerate the active core config or reconnect the VPN: generate/check it separately and plan a reconnect to apply route changes.

**Privacy:** node files can contain UUIDs/passwords. Conf files can contain proxy credentials and private host mappings. Enabling sync uploads those selected sources to your iCloud Drive; a rule-only setup can use `files: []` with `include_conf: true`. Make that choice explicitly and consider Advanced Data Protection. Do not enable another Mac's previously disabled sync merely because the CLI was upgraded.

If iCloud temporarily evicts a file or a read fails, the pass reports an error rather than inventing a replacement. Avoid editing a source on two Macs before their shared baseline has synchronized.
