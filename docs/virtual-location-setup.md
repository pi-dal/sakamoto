# Virtual location setup

Available from **Settings → Virtual location**. This release supports fixed WGS84 coordinates and moving routes, accuracy, saved favorites, explicit start/stop and diagnostic export. Enter decimal degrees; coordinates copied from GCJ-02 or BD-09 maps must first be converted to WGS84.

## iOS 26

1. Tap **Install location certificate**. Safari downloads the public configuration profile. Install it in Settings. If the download cannot reach localhost through your current proxy, use **Export certificate profile**, save it to Files and open the file to install.
2. Open **Settings → General → About → Certificate Trust Settings** and enable full trust for **Sakamoto Location CA**.
3. Return to Sakamoto and tap **Check certificate trust**. This uses default iOS TLS validation against a temporary local HTTPS server; it does not bypass certificate validation.
4. Enter latitude, longitude and accuracy. Enable **Simulate network location**, and connect Sakamoto VPN. Saved intent waits for the VPN if it is disconnected.
5. Request a new location in the app being tested. In Sakamoto, **Refresh proxy diagnostics** shows TLS connections, location requests, rewrites, pass-through counts and failures. **Check reported location** asks iOS for a separate location observation.

The feature runs in the existing VPN extension on Wi-Fi/cellular when the OS routes the location service through the configured proxy. It rewrites recognized Apple network-positioning responses; it does not directly write GPS data. A successful rewrite is evidence of interception, not proof that every app uses that point. GPS, caches, service pinning and proxy behavior can take precedence. iOS 27+ is disabled based on the reference project's reported compatibility block.

Turn off the switch to stop rewriting and restore the original system proxy settings. Stop works even when an unsaved coordinate field is invalid. Coordinate updates do not reload the VPN core; enabling/disabling requires a system proxy settings update. Apps may retain the last location until a fresh fix arrives.

The CA is unique to the device. Its private key stays in the existing App Group's device-only Keychain and is never included in profiles, IPC or reports. The public profile may be removed from Settings after disabling this feature if it is no longer needed.

## Android

1. Enable Developer options and choose **Sakamoto** under **Select mock location app**. The page has a link to those settings.
2. Turn on system Location Services. Grant location access when Sakamoto requests it for the foreground service and location measurement.
3. Enter latitude, longitude and accuracy, then enable **Simulate location**. The independent location service shows an ongoing notification with a Stop action; it can run alongside the VPN.
4. Use **Check reported location** and the injection counters to verify the fix. Google-certified devices use fused mock mode as well as GPS/network providers. Devices without usable Google Play services use GPS/network providers.
5. Stop from the page or notification. Sakamoto removes owned test providers and requests Google mock-mode exit. If cleanup cannot be confirmed, the page requires **Stop simulation** recovery before starting again; reselect Sakamoto in Developer options if authorization was withdrawn.

Android marks injected fixes as mock, and apps can reject them. Fixed-point updates run once per second while the service is scheduled; routes use their configured interval. battery saving can delay them. Simulation never automatically resumes at boot. A pending cleanup is retained across restarts, so a previous unconfirmed session can require one Stop action before a new start. Apps may retain cached fixes after stopping.

## Moving routes

In **Moving route**, enter 2 to 32 WGS84 points in travel order, one decimal `latitude, longitude` pair per line. For example, this illustrative path contains three points:

```text
22.500000, 114.000000
22.505000, 114.005000
22.510000, 114.015000
```

Set a **Duration** of 10 to 86400 seconds for the whole route and an **Update interval** of 1 to 10 seconds. **Start route** begins a new run at the first point. Points are connected along their shortest spherical arcs and distance, rather than point count, determines time spent on each leg. This follows the entered path; it does not calculate driving/walking roads or import GPX.

- **Pause route** holds the current point and freezes elapsed route time. **Resume route** continues from that progress without restarting.
- **Stop route and hold position** keeps simulation enabled at the current point. Turn the main simulation switch off, or use Android's notification Stop, to restore real positioning.
- At completion the simulated point stays at the exact endpoint. A new Start restarts the route; completion does not loop.
- Route drafts persist for editing. Routes require an explicit start and never automatically replay after a VPN reconnect or location-service restart.

On iOS, connect the VPN and verify CA trust before starting. The VPN extension advances the route when the app page closes; only new intercepted WLOC responses receive the then-current target. The route progress alone cannot prove that iOS reports continuous movement. Measure the reported location separately.

On Android, the foreground service injects route positions with fresh timestamps, speed and bearing, including fused mock mode where available. It continues after leaving the page. Wall-clock changes do not affect route timing on either platform; delayed ticks catch up to elapsed monotonic time. Device scheduling can still delay actual injection.

## Diagnostic reports

Reports include the app/OS version, selected coordinates, timestamps, runtime evidence and the observed position. They exclude VPN configuration, authentication keys and CA private keys. Review the coordinates before sharing.

Local integration tests cover HTTPS response rewriting/disable, preservation of the original local proxy, gateway failure on a stalled upstream, malformed/oversized payloads and certificate trust behavior. Physical-device checks remain necessary for location-source precedence, Wi-Fi/cellular, screen lock, permissions, force-stop and restoration.

For source references and implementation constraints, see [the feasibility assessment](virtual-location-feasibility.md).
