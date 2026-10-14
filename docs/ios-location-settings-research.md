# iOS Location Services settings, activation, and restoration

Research date: 2026-10-14. Rechecked after the owner reported incorrect positioning, vague “Starting is explicit” copy, and missing Location Services restoration guidance after stop.

## Complete reference procedure

The latest `xweiba/location-spoofer` main revision remains `4aa4c6aa6cf0ae0d134c317c4c36eb90f21b21b7` (confirmed through GitHub API on this research date). Its [ActivationTipContent and DeactivationTipContent](https://github.com/xweiba/location-spoofer/blob/4aa4c6aa6cf0ae0d134c317c4c36eb90f21b21b7/App/TipViews.swift) describe:

1. Airplane Mode on.
2. Wi-Fi off.
3. Global Location Services off.
4. Wi-Fi on while Airplane Mode remains on; start/confirm the location proxy for activation, or stop/clear/remove the relevant proxy for restoration.
5. Airplane Mode off.
6. Location Services on, then verify in a map.

Restart is suggested at step 3 only if an attempt fails, resuming at step 4. The source recommends two-second waits and asserts cache clearing. Those are reference-maintainer recommendations, not an Apple-documented cache-clearing contract or a guarantee reproduced on a physical iPhone here. Sakamoto keeps the sequence but waits for actual network/proxy state rather than promising that a fixed delay clears caches.

The earlier Sakamoto four-page recovery guide omitted both radio steps and the Airplane Mode restoration stage. Treating those as disposable was unjustified; the guide now includes them and offers optional restart at the right point. Initial setup leads to **Begin activation steps**, with **Verify current setup** separately available for an already configured session.

## Apple documentation corrects the Wi-Fi action

Apple's [Use Bluetooth and Wi-Fi in Control Center](https://support.apple.com/en-us/102412) states that Control Center disconnects Wi-Fi but keeps it available for features including Location Services. To completely disable Wi-Fi, it explicitly instructs **Settings → Wi-Fi → off**. It also notes that Airplane Mode may preserve radios enabled during a previous Airplane Mode session. [Airplane Mode instructions](https://support.apple.com/en-us/108785) confirm that Wi-Fi can be used while Airplane Mode is on.

Sakamoto therefore uses the actual Settings Wi-Fi switch for the off page, rather than copying the reference's ambiguous Control Center instruction or assuming Airplane Mode already disabled it.

Apple's [Location Services explanation](https://support.apple.com/en-us/102515) describes GPS, Bluetooth, Wi-Fi and cellular inputs. Global Location Services off pauses app access; it does not force a network-only fix. Both activation and restoration end with **turn Location Services on when you want a fresh position**. The app neither changes the switch nor demands that a user who wants location off enable it automatically.

## Adaptation to Sakamoto's VPN

Sakamoto uses the existing PacketTunnel's scoped NE proxy settings. The reference's App Mode instead installs a manual current-Wi-Fi HTTP proxy. Consequently Sakamoto's restoration does not tell the user to remove a manual Wi-Fi proxy it never created or require disconnecting the entire VPN.

Starting now names the actual action: after Wi-Fi/internet and VPN restoration, tap **Start simulation at selected place**, confirm **Network-location proxy enabled**, then restore radios and system location. Next only navigates. “Proxy enabled” is not a claim that the target app's position changed.

Successful stop opens persisted restoration guidance automatically:

- Restore Wi-Fi if it was disabled.
- Exit Airplane Mode.
- Re-enable Location Services and request a fresh real fix.

If the position stays stale, **Still old? Refresh real location** opens the complete six-page radio/location sequence, keeping simulation off and offering restart at the location-off page. Stop errors and unexpectedly enabled provider replies cannot open a success flow. Invalid unapplied coordinates do not block stop. Reopening/resuming preserves the restoration flow and page.

## Verification must distinguish current evidence

Verification captures the rewrite counter when that page opens and reports only additional rewrites during the current check. Historical counters cannot prove current operation.

- No requests after asking for a fresh fix: possible GPS/cache selection or proxy bypass, not established certificate failure.
- Requests but no current rewrite: inspect TLS failures, unchanged responses and runtime detail; trust, connectivity, format or OS incompatibility may matter.
- New rewrite plus a differing measurement: the response was processed, but location-source selection or target-app caching remains unresolved.

Restoration measurements display fresh coordinates and ask for comparison against real surroundings. Sakamoto has no independent truth coordinate and cannot declare restoration from proximity to the former simulated point. Reference [issue #10](https://github.com/xweiba/location-spoofer/issues/10) reports brief iOS 26.1 success followed by failure despite patch logs, reinforcing this boundary.

## Settings shortcuts

Sakamoto uses `App-Prefs:root=Privacy&path=LOCATION`, `App-Prefs:root=WIFI`, and corresponding profile/trust destinations. Apple's public [openSettingsURLString](https://developer.apple.com/documentation/uikit/uiapplication/opensettingsurlstring) documents the app's own settings only. Private Settings paths remain OS-dependent. The acceptance callback cannot establish which page opened, so each page always shows its exact manual path and labels shortcuts as attempts.

Prior iOS 27 Simulator experiments accepted the Location Services URL but displayed Settings root; they are not evidence of iOS 26 physical-device direct navigation. No new URL is described as a verified fix solely because it was accepted.

## Validation limits

Local environment has iOS 27 Simulator, with support overridden only in render/control tests. Physical iOS 26 behavior, Settings destinations, fresh WLOC requests after the radio sequence, GPS precedence and target-app restoration cannot be verified in this environment. The [reference README](https://github.com/xweiba/location-spoofer/blob/4aa4c6aa6cf0ae0d134c317c4c36eb90f21b21b7/README.md) still reports a MITM block from iOS 27 beta 6; Sakamoto keeps its iOS 27+ support block.

Automated checks verify confirmed stop, failure handling, persisted restoration, off-switch respect, lack of VPN reload, and current versus historical rewrite evidence. Native phone/tablet captures verify the revised activation and restoration pages. These establish the guide and controls, not successful physical-device position replacement.
