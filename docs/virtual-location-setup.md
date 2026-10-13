# Virtual location setup

Available from **Settings → Virtual location**. Choose places and route stops directly on the map, search a place name/address, set accuracy, save favorites, and explicitly start/stop simulation. iOS uses native **MapKit**; Android uses **MapLibre Native** with OpenFreeMap tiles and Photon place search, without a map API key or Google Play dependency. Network access is needed to load new map regions or search places. Existing favorites and **Advanced coordinates · WGS84** remain available when map/search providers cannot be reached. Coordinates copied manually from GCJ-02 or BD-09 maps must first be converted to WGS84.

**Choose on map** opens an isolated draft. Pan/zoom and tap a location, or submit a place/address search and choose a result; **Choose map center** (Android: **Choose center**) also selects the crosshair. **Done** (Android: **Use selection**) returns the chosen point to settings. Then tap **Save/Apply location**, or enable simulation. Cancel/back discards map edits and never changes an active simulation. Map browsing and place search do not request location permission.

## In-app setup guide

Open **Settings → Virtual location → Setup guide**. The guide is expanded initially and can be collapsed or reopened at any time; its expanded state is remembered. Numbered steps use the real certificate, permission, map and simulation controls. A “Next” hint reflects checked prerequisites and runtime status, rather than marking a step complete just because a button was tapped. Browsing the guide or granting location access does not start simulation.

- **iOS:** download the device certificate → install the profile and enable full trust → check trust → choose a place → connect VPN and enable simulation → check the reported position. The Home link switches to the existing Home tab; return to Settings → Virtual location after connecting. Moving route and diagnostic links scroll to those controls. iOS exposes only an app-settings link, so the guide shows the exact manual paths for profile installation and Certificate Trust Settings. Downloading a profile alone does not install or trust it. Returning from Settings refreshes the VPN/runtime status; run **Check certificate trust** after changing trust.
- **Android:** enable Developer options → choose Sakamoto as the mock location app → turn on device Location and allow app access → choose a place → explicitly start → check the reported position. Buttons open About phone, Developer options, device Location, and Sakamoto app settings. If a device lacks a settings destination, the app tries the main Settings screen and displays manual recovery instructions. Authorization, location-service and permission states are checked again on return. Android needs neither the iOS certificate nor a VPN connection for this feature.

The guide also explains how to create a moving route and stop simulation. Map selection edits only the draft; active simulation changes only on an explicit Apply/Start action.

## iOS 26

1. Tap **Install location certificate**. Safari downloads the public configuration profile. Install it in Settings. If the download cannot reach localhost through your current proxy, use **Export certificate profile**, save it to Files and open the file to install.
2. Open **Settings → General → About → Certificate Trust Settings** and enable full trust for **Sakamoto Location CA**.
3. Return to Sakamoto and tap **Check certificate trust**. This uses default iOS TLS validation against a temporary local HTTPS server; it does not bypass certificate validation.
4. Tap **Choose on map**, select a place and tap **Done**. Set accuracy, enable **Simulate network location**, and connect Sakamoto VPN. Saved intent waits for the VPN if it is disconnected.
5. Request a new location in the app being tested. In Sakamoto, **Refresh proxy diagnostics** shows TLS connections, location requests, rewrites, pass-through counts and failures. **Check reported location** asks iOS for a separate location observation.

The feature runs in the existing VPN extension on Wi-Fi/cellular when the OS routes the location service through the configured proxy. It rewrites recognized Apple network-positioning responses; it does not directly write GPS data. A successful rewrite is evidence of interception, not proof that every app uses that point. GPS, caches, service pinning and proxy behavior can take precedence. iOS 27+ is disabled based on the reference project's reported compatibility block.

Turn off the switch to stop rewriting and restore the original system proxy settings. Stop works even when an unsaved coordinate field is invalid. Coordinate updates do not reload the VPN core; enabling/disabling requires a system proxy settings update. Apps may retain the last location until a fresh fix arrives.

The CA is unique to the device. Its private key stays in the existing App Group's device-only Keychain and is never included in profiles, IPC or reports. The public profile may be removed from Settings after disabling this feature if it is no longer needed.

## Android

1. Enable Developer options and choose **Sakamoto** under **Select mock location app**. The page has a link to those settings.
2. Turn on system Location Services. Grant location access when Sakamoto requests it for the foreground service and location measurement.
3. Tap **Choose on map**, select a place and tap **Use selection**. Set accuracy and enable **Simulate location**. The independent location service shows an ongoing notification with a Stop action; it can run alongside the VPN.
4. Use **Check reported location** and the injection counters to verify the fix. Google-certified devices use fused mock mode as well as GPS/network providers. Devices without usable Google Play services use GPS/network providers.
5. Stop from the page or notification. Sakamoto removes owned test providers and requests Google mock-mode exit. If cleanup cannot be confirmed, the page requires **Stop simulation** recovery before starting again; reselect Sakamoto in Developer options if authorization was withdrawn.

Android marks injected fixes as mock, and apps can reject them. Fixed-point updates run once per second while the service is scheduled; routes use their configured interval. battery saving can delay them. Simulation never automatically resumes at boot. A pending cleanup is retained across restarts, so a previous unconfirmed session can require one Stop action before a new start. Apps may retain cached fixes after stopping.

## Moving routes

In **Moving route**, tap **Create/Edit route on map**. Tap or search for 2 to 32 stops in travel order. Numbered markers and a connecting path show the planned order; **Undo last stop**, **Clear route**, and **Show all** help correct the draft. The simulated position is a separate marker when simulation is active. Confirm with **Done/Use selection**, set duration and update interval, then **Start route**. Confirming a draft preserves any route already running until you explicitly start the new one.

**Advanced route coordinates · WGS84** still accepts one decimal `latitude, longitude` pair per line. For example, this illustrative path contains three points:

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

## Map providers

MapKit renders/searches through Apple's map services. Android uses the public OpenFreeMap Liberty vector style (OpenMapTiles/OpenStreetMap data) and the public Photon geocoder. Map attribution remains visible in the editor, and Android **About → Map licenses** includes the SDK notice. Photon searches are submit-only, cancellable, bounded to 12 seconds and 256 KiB, spaced by at least one second, and cached for the last 50 queries; no autocomplete or location tracking request is sent to Photon. These public endpoints offer no availability guarantee, so map load failures expose **Retry map**, and place-search failures keep map selection and advanced inputs usable.

## System VPN indicator

The VPN icon shown in the device status bar is controlled by the operating system and cannot be hidden by Sakamoto while the tunnel is active.

- **iOS:** the Packet Tunnel Network Extension has no public API to suppress or replace the iOS VPN status indicator. Disconnecting the VPN is the supported way to remove it. The explanation appears under Settings → Widgets & Shortcuts → VPN status indicator.
- **Android:** Android documents a VPN key icon in the status bar for an active `VpnService`, and requires a foreground service notification while the VPN runs. Settings → Widget & notifications → System VPN indicator explains this and opens Android's Sakamoto notification settings. Disabling Sakamoto's own notification may remove its notification entry, but it does not remove Android's system VPN icon; disconnecting the VPN does.

This is separate from Sakamoto's own widgets, notification content and notification permission. Those controls can change app surfaces but cannot suppress the operating system's privacy indicator.

Reports include the app/OS version, selected coordinates, timestamps, runtime evidence and the observed position. They exclude VPN configuration, authentication keys and CA private keys. Review the coordinates before sharing.

Local integration tests cover HTTPS response rewriting/disable, preservation of the original local proxy, gateway failure on a stalled upstream, malformed/oversized payloads and certificate trust behavior. Physical-device checks remain necessary for location-source precedence, Wi-Fi/cellular, screen lock, permissions, force-stop and restoration.

For source references and implementation constraints, see [the feasibility assessment](virtual-location-feasibility.md).
