# Optional macOS iCloud Drive sync

The service keeps **generated state local** in `~/.sakamoto`. To sync manual node links and any other small source conf files, opt into file sync under iCloud Drive:

```yaml
icloud:
  enabled: true
  directory: /absolute/path/to/iCloudDrive/sakamoto
  files: [nodes.txt]
```

You can toggle **Settings → Sync node sources to iCloud** in the TUI after setting the directory in your local `sakamoto.yaml`. The watcher checks approximately once a minute and uploads/downloads only the listed source filenames; it stores last synced hashes in the **local-only** `icloud-state.json`. Do not put generated `config.json`, `.srs`, logs, proxy restore files or `sakamoto.yaml`/API secret in that list. Add a local `macOS.conf` to `files:` if you own that file and want it synced; `include` files can be listed by filename too.

On first use:

- If a file exists on only one side, it is copied to the other.
- If both copies are identical, sync starts tracking it.
- If both copies differ without a common baseline, **nothing is overwritten**. Merge them manually and try again.
- If both changed since the last sync, it reports a conflict. Deletion is not propagated automatically.

**Privacy:** `nodes.txt` contains server addresses and may contain UUIDs/passwords. Enabling iCloud sync uploads these secrets to your iCloud Drive. Make this choice explicitly; consider Advanced Data Protection if required by your threat model. Keep `.sakamoto` itself out of git. Don't use an iCloud folder as the root daemon's live state directory: generated routes and a root-owned socket should remain local.

iCloud Drive may temporarily evict files; if a listed file is unavailable, sync reports an error and preserves both sides. Never edit the same source simultaneously on two Macs without first allowing them to sync.
