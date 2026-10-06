# sakamoto Android

A native Material 3 VPN client for sing-box **v1.14.2**, Android **7.0 / API 24+**.
The Kotlin app binds libbox, mobilecore and mobileexperiment in one AAR and one
JNI runtime. Native frameworks and private configuration are never committed.

## Install the official release

Download `sakamoto-android-v0.1.0.apk` and `SHA256SUMS` from the
[Android v0.1.0 release](https://github.com/pi-dal/sakamoto/releases/tag/android-v0.1.0).
Verify the checksum before installation. The APK is a non-debuggable release
signed with the project's dedicated Android certificate:

```text
SHA-256: a6fb9d4b3070023b52c0efcf254b2cad251fa3fa79cab6eeec2c378f6daa66bc
```

The package is `com.pidal.sakamoto`, versionName `0.1.0`, versionCode `2`.
Future official versions retain the signing certificate for upgrades.
A development APK uses a different debug certificate and cannot be replaced
in place by the official APK. Preserve/export configuration and credentials
before a manual transition; uninstalling deletes app-private data and Keystore
credentials. Release tooling never uninstalls or clears a connected phone.

## First connection and configuration

The app does not contain servers, credentials or a sample live connection.
Import a validated tunnel package through **Config → Import tunnel package**:

```sh
# On your computer, from the source checkout:
mise -E android exec -- python3 android/scripts/sync-host.py --export /private/path/android-tunnel.zip
```

Transfer this private ZIP to your phone, disconnect the VPN and select it in
Config. The ZIP contains your server credentials; never attach it to a public
release. Import validates the complete generated configuration and rule files
before replacing app-private state, rewrites rule paths for this phone and
preserves the previous configuration on failure. Source conf, includes, nodes
and compiled rule sets are separate from the generated tunnel JSON. Plain
source URL import stages a conf; it does not generate a runnable tunnel.

- **Home:** a connection switch, network-check summary, routing mode and group
  entries. Tap the status row for VPN diagnostics and Check.
- **Config:** nodes/groups, route rules, exit proxy, source management and
  explicit Apply. Tap a node to select, use the trailing edit icon to change
  its server settings. Editors protect unsaved changes. Rules support ordering
  and deletion with Undo.
- **Data:** actual traffic and connection streams, connection details and
  confirmed close actions.
- **Settings:** tunnel settings, Tailscale, S3 sync, experiments, widgets,
  notifications and About.

Generated-config edits (rule/action/final, selector default, server/detour,
log level/TUN settings) are validated before saving and take effect on a
successful native reload or next connection. Source conf, node share links
and subscription declarations still require host generation. The app blocks
Apply of an unchanged generated snapshot after source edits rather than
claiming those edits are running. Local source-editor size limits keep large
rule sources on the computer.

Android VPN consent and, on Android 13+, notification permission are requested
through system dialogs. Denying notifications does not prevent the VPN, but
removes status/control visibility from the notification shade. Diagnostics
separate authorization, system TUN, physical internet, command channel and a
real DNS/TLS/HTTPS 204 check. Node latency is not an end-to-end VPN health claim.

## Widgets and notification

Settings can pin Toggle 1×1, Compact 2×1 or Status 3×2 widgets. Cell sizes depend
on the launcher's grid. Android 12+ receives layouts for the launcher's actual
SizeF options; older releases receive separate orientation layouts.

Small/compact widgets toggle directly after prior VPN consent. If consent is
missing, the app opens the authorization flow. All sizes preserve 48dp touch
controls; busy states disable repeated actions. Compact layouts center their
fixed status/latency slots, show node/protocol/mode/group only with sufficient
height, and keep on/off geometry stable. Measured latency, stale results and
untested state are distinct. The service refreshes widget/notification data
while running; a terminated process cannot promise continuous launcher updates.

The foreground notification displays mode/node/check/experiments and offers
Disconnect, Check and Recover. Credentials and source URLs are never placed
in widget or notification payloads.

## Experiments

Unmatched policy supports off (direct), on (proxy exit) and auto. The shared Go
Tracker learns only correlated unmatched-direct TCP dial timeouts spaced at
least ten seconds apart within thirty minutes, with a freshly healthy proxy
URL test. Threshold is 1–20. Candidate route updates validate before reload;
a private recovery journal restores an incomplete update at next connection.
Explicit direct/reject rules remain authoritative. Learned domains stay local.

Cloudflare learning requires an explicit regional restriction in core logs.
It does not inspect encrypted browser error pages or infer meaning from
ordinary HTTP 403s. Fallback uses fresh group tests and preserves manual
selection outside the priority chain; manual recovery has a cooldown.
A broken fixed exit cannot be repaired by changing entry nodes. Automatic
learning may interrupt active connections while the TUN reloads. These are
experimental features; evidence/parser tests and device smoke do not prove
all destination behavior or long-duration roaming reliability.

## Sync

S3 source sync uses the same Go engine as the CLI. Credentials are encrypted
with Android Keystore and never included in source bundles. Conflicts stop
without silently replacing simultaneous edits. See [S3 sync](../docs/s3-sync.md).

For a USB-connected **debug** install, the maintainer helper can transfer a
snapshot from `~/.config/sakamoto` directly into private app storage:

```sh
mise -E android exec -- python3 android/scripts/sync-host.py DEVICE_SERIAL
```

It validates the adapted sing-box config and verifies every file SHA-256,
retains a private rollback copy, and refuses to replace divergent device
sources/config. The local-only helper uses existing PyYAML and `sing-box`;
USB `run-as` does not apply to a non-debuggable release. Its `--export` mode
creates the private ZIP for the official client's system file picker; neither
path provides automatic cloud sync of generated tunnel state. No host API secret is copied.

## Build and release

```sh
mise trust
mise -E android install
mise -E android run android:build
mise -E android run android:checksum

# AAR already built:
mise -E android run android:verify

# Official signature required; testReleaseUnitTest + lintVital + assembleRelease:
mise -E android run android:release
```

Gradle 8.9, AGP 8.7.3, Kotlin 2.0.21, JDK 17, Android API 35, Build Tools 35.0.0
and NDK 28.2.13676358 are pinned. The upstream builder produces API-24 and
legacy AARs; this app ships the main API-24 variant only. The combined AAR
binds `experimental/libbox`, `pkg/mobilecore` and `pkg/mobileexperiment` so
there is one `go.Seq` and one native library. Do not mix independent AARs.

`release.py` requires `SAKAMOTO_ANDROID_KEYSTORE`, `SAKAMOTO_ANDROID_STORE_PASSWORD`,
`SAKAMOTO_ANDROID_KEY_ALIAS` and `SAKAMOTO_ANDROID_KEY_PASSWORD`, or the owner's
private signing directory outside the repository. Public APKs never use a
debug certificate. Preserve a secure backup of the release key: losing it
prevents future package upgrades.

CI builds debug artifacts on relevant commits. `android-v*` tags build officially
signed release APKs, verify the tag/version, and attach APK, SHA256SUMS and
commit metadata to a prepared GitHub release. Private signing material exists
only in repository secrets and a temporary runner file.

## Verification and upstream

Pixel 8 Pro smoke tests exercised native binding, VPN system registration,
DNS/TLS/HTTPS 204 through the exact application Check path, native reload,
notification Check/Disconnect and widget direct connect/disconnect/reconnect.
Native RemoteViews tests cover compact sizing, large font, fixed on/off
geometry and provider registration. JVM tests cover pure edit/routing/fallback/
widget projections and secret masking. Tests that use a real VPN require
explicit arguments and existing consent/config, and disconnect at completion.

Built-in Tailscale supports status, auth-URL login, exit-node choice, ping and
logout through libbox. Auth keys are encrypted at rest and injected at start.
Taildrop/SSH/serve are not shipped surfaces. No root, system tailscaled or
macOS DNS takeover is emulated on Android.

Reference implementations: [sing-box](https://github.com/SagerNet/sing-box),
[sing-box-for-android](https://github.com/SagerNet/sing-box-for-android),
[NekoBox](https://github.com/MatsuriDayo/NekoBoxForAndroid) and
[Telegram Android](https://github.com/DrKLO/Telegram). Interaction patterns were
studied; no association or endorsement is implied. Source code is GPL-3.0-or-later;
separate artwork attribution remains in [NOTICE](../NOTICE.md).
