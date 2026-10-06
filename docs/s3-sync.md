# S3 source sync

Sakamoto supports optional bidirectional sync using AWS S3 and S3-compatible stores such as Cloudflare R2 and MinIO. macOS TUI, iOS and Android use the same Go sync engine and wire format.

## Connection

Configure the same endpoint, bucket and prefix on each device:

| Service | Endpoint | Region |
| --- | --- | --- |
| AWS S3 | `https://s3.us-east-1.amazonaws.com` | `us-east-1` (your bucket region) |
| Cloudflare R2 | `https://ACCOUNT_ID.r2.cloudflarestorage.com` | `auto` |
| MinIO | `https://s3.example.com` | configured region, often `us-east-1` |

The endpoint is a service endpoint, not a bucket URL. Requests use path-style addressing and AWS Signature V4. HTTPS is required; redirects are rejected. A prefix such as `sakamoto` separates this source bundle from other objects in the bucket. The sync object is `PREFIX/sources-v1.json`.

The credential needs only GetObject and PutObject for that object. Bucket listing and deletion are not used. The store must support ETag and conditional PutObject (`If-Match` and `If-None-Match`). Concurrent object updates stop with a conflict; stores that omit ETag cannot be safely synchronized.

## TUI

In **Settings → S3 source sync**, edit endpoint, region, bucket and prefix, then access key, secret key and optional session token. Enable **Sync sources to S3** after connection settings are valid; click **Sync S3 now** for an immediate pass. `Ctrl+U` clears a credential editor before pasting a replacement. Credentials are always masked.

Non-secret settings are stored in the sidecar:

```yaml
s3:
  enabled: false
  endpoint: https://s3.us-east-1.amazonaws.com
  region: us-east-1
  bucket: your-private-bucket
  prefix: sakamoto
```

Access/secret keys and session tokens live in private `s3-credentials.json` (0600), not in the sidecar or cloud source bundle. The watcher runs a sync pass every minute while enabled. It does not regenerate or restart a running tunnel after a download: use **Config → Regenerate** and reconnect when ready.

## iOS and Android

Open **Settings → S3 Sync**, fill connection settings and credentials, enable sync and save. Tap **Sync now** to upload or download. iOS stores credentials in Keychain; Android encrypts them with an Android Keystore AES-GCM key. Source data stays in app-private storage. Mobile sync is manual while the app is open; this does not promise background execution when iOS/Android suspends the app.

Downloads are staged in the Config page. They do not silently replace a running tunnel's generated config. The existing mobile generation boundary still applies: imported sources are validated and staged; host generation remains responsible for producing full sing-box configurations.

## Scope and conflicts

The versioned JSON source bundle contains only:

- `nodes.txt`: manual node share links;
- `policy.json`: an array of `{match, action}` rules;
- `subscriptions.json`: an array of `{name, url, format}` declarations;
- `conf/*.conf`: the selected local conf and its relative includes, with `main_conf` identifying the main file.

Generated `config.json`, `sakamoto.yaml`, API/S3/Tailscale credentials, logs, sockets, `.srs`, learning state and sync baselines never enter the bundle. Remote conf URLs and remote includes remain importer-managed. Local relative conf includes must be present; an incomplete graph fails before upload. Downloaded include files are retained so subsequent mobile passes preserve the complete bundle.

A device records hashes only after adopting a successful merge. If one side changed relative to that baseline, its edit is adopted. If both sides changed, or a source was deleted on one side, sync reports a conflict and keeps both copies. On a fresh device, absent sources download; different existing sources require manual reconciliation. File deletions are not propagated automatically. Empty node/policy/subscription content can be synchronized after the initial baseline exists.

Changing endpoint/bucket/prefix starts a new baseline. Copy the desired source data manually to resolve a conflict, then retry; removing sync state alone does not make divergent copies safe to overwrite. Edits made while a network request is running cause adoption to stop and require a retry.

Source bundles can contain proxy credentials and subscription access tokens. Use a private bucket and narrowly scoped credentials. TLS protects transport; this feature does not add application-layer encryption to the bucket object.

## Validation

Local automated tests cover source scope, include graphs, credentials permissions/masking, bidirectional merge, ETag conditional failures and SigV4 requests using a TLS test S3 server. Actual AWS/R2/MinIO interoperability requires a pass against the configured provider; no live bucket credentials are included in the repository.
