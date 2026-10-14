# Virtual location setup

Available from **Settings → Virtual location**. Choose places and route stops directly on the map, search a place name/address, set accuracy, save favorites, and explicitly start/stop simulation. iOS uses native **MapKit**; Android uses **MapLibre Native** with OpenFreeMap tiles and Photon place search, without a map API key or Google Play dependency. Network access is needed to load new map regions or search places. Existing favorites and **Advanced coordinates · WGS84** remain available when map/search providers cannot be reached. Coordinates copied manually from GCJ-02 or BD-09 maps must first be converted to WGS84.

**Choose on map** opens an isolated draft. Pan/zoom and tap a location, or submit a place/address search and choose a result; **Choose map center** (Android: **Choose center**) also selects the crosshair. **Done** (Android: **Use selection**) returns the chosen point to settings. Then tap **Save/Apply location**, or enable simulation. Cancel/back discards map edits and never changes an active simulation. Map browsing and place search do not request location permission.

## In-app setup guide

Open **Settings → Virtual location → Continue guide** on iOS (**Continue setup** on Android). The guide opens as a separate screen with **one step per page**, a fixed **Previous / Next step** footer, and a **Jump to step** menu. It remembers both the current flow and page, including after Settings or a restart. Next only navigates; **Close guide** does not certify setup, activation, or restoration.

- **iOS:** certificate download → profile install → full-trust check → choose a place → **Begin activation steps**. Activation walks through Airplane Mode on → Wi-Fi fully off in Settings → Location Services off → Wi-Fi on and connected, then connect Sakamoto VPN and tap **Start simulation at selected place** → Airplane Mode off → Location Services on and verify. A separate **Verify current setup** action supports checking an already configured session. Successful stop opens the three-page **Restore real location** guide automatically; **Still old? Refresh real location** opens the full radio/location refresh sequence. Restart is optional while location is off. Legacy cache-recovery mode restarts at the complete activation sequence. Settings shortcuts keep manual paths visible because accepted URLs can still open the wrong page. Returning refreshes Location Services and runtime status; trust requires its own explicit check.
- **Android:** enable Developer options → choose Sakamoto as the mock location app → keep device Location on and allow app access → choose a place → explicitly start → check the reported position. Buttons open About phone, Developer options, device Location and app settings. The reported location and simulation errors appear inside the verification page. If a settings destination cannot open, the app tries main Settings and shows a manual recovery message. Authorization, location-service and permission states refresh on return. Android must **keep Location on** throughout simulation; it does not use the iOS reset step, certificate or VPN requirement.

The route shortcut closes the guide and opens the existing moving-route controls. Map selection edits only the draft; active simulation changes only on an explicit Apply/Start action. The final page includes an explicit Stop action and restoration instructions.

## iOS 26

First install the certificate profile and enable full trust in **Settings → General → About → Certificate Trust Settings**. Return and run **Check certificate trust**, then choose a place on the map. Profile download alone does not install or trust it. If Safari cannot download from localhost through the current proxy, export the profile to Files and open it to install.

Tap **Begin activation steps** and follow these pages in order:

1. Turn **Airplane Mode ON** in Control Center. Cellular connectivity pauses; Wi-Fi may remain enabled.
2. Open **Settings → Wi-Fi** and turn the switch **OFF**. The Control Center button usually disconnects a network while leaving Wi-Fi available for Location Services; it is not the full-off action required by this procedure.
3. Open **Settings → Privacy & Security → Location Services** and turn the top switch **OFF**. Keep it off through the next page. If a previous attempt stayed stale, an optional restart belongs here; after unlocking, resume the guide, recheck radio settings and continue.
4. Keep Airplane Mode on. Turn Wi-Fi **ON** in Settings, join a network with internet access, connect **Sakamoto VPN** from Home, return to **Virtual location → Continue guide**, and tap **Start simulation at selected place**. Confirm **Network-location proxy enabled** before continuing. If trust is missing, recheck it. Opening Home or tapping Next does not start simulation.
5. Turn **Airplane Mode OFF**. Wait for the VPN to reconnect if the network change interrupted it; return to the preceding page if simulation is no longer confirmed.
6. Turn **Location Services ON**, then request a fresh location in Maps or the target app. Tap **Check reported location** and compare against the selected place.

This sequence adapts the reference project's activation instructions to Sakamoto's PacketTunnel. It does not establish that a two-second delay clears caches, that restarting always works, or that GPS is overridden. A working Wi-Fi connection is needed for the Airplane Mode stage. An already configured/cellular session can use **Verify current setup**; it is not claimed to have completed this refresh procedure.

**Diagnose the result:**

- VPN disconnected or proxy unconfirmed: restore the connection and explicitly start simulation first.
- **Location requests** remains zero after requesting a new fix: the system may be returning GPS/cached data or bypassing the proxy. Repeating certificate installation does not prove the request path works.
- Requests arrive but no new rewrites: inspect **TLS failures**, **Passed through unchanged**, and the diagnostic detail; check trust and upstream reachability. A format or OS incompatibility may also prevent processing.
- A new rewrite appears but the reported location differs: interception worked for that response; GPS, caches or the app's own location behavior may still take precedence. The guide shows **new rewrites during this check**, rather than treating a historical total as current success.

**Stop and restore real location:**

Turning the simulation switch off or tapping **Stop and restore real location** requests a stop. Only a successful, confirmed stop opens restoration guidance; failures keep their error and do not claim restoration.

1. Restore Wi-Fi if you turned it off; rejoin your usual network.
2. Turn Airplane Mode **OFF** to restore cellular connectivity.
3. Turn Location Services **ON** when you want fresh positioning. Request a new fix in Maps/the target app and use **Check reported location** to compare coordinates with your real surroundings. Sakamoto cannot know the true physical coordinate, so it does not declare restoration based on proximity to the former simulated point.

If it remains stale, **Still old? Refresh real location** provides Airplane Mode on → Wi-Fi fully off → Location Services off → Wi-Fi restored with simulation remaining off → Airplane Mode off → Location Services on and verify. An optional restart belongs at the location-off page. Stop does not automatically re-enable system Location Services, and the app respects a user who chooses to leave it off.

Sakamoto restores its own previous NE proxy settings when simulation stops. It does **not** install the reference App Mode's manual Wi-Fi HTTP proxy, so its guide does not ask you to remove that proxy. The VPN can remain connected for normal proxy/Tailscale use. Coordinate updates do not reload the VPN core; enabling/disabling changes the system proxy settings. Apps may retain a last fix until they request another one.

The feature rewrites recognized Apple network-positioning responses; it does not directly write GPS data. iOS 27+ remains disabled based on the reference compatibility report. The device-specific CA private key stays in device-only Keychain and is absent from profiles, IPC and reports. After stopping, you may remove its public profile if no longer needed.

See [the settings and activation/restoration research](ios-location-settings-research.md) for exact source references and physical-device validation limits.

## Android

1. Enable Developer options and choose **Sakamoto** under **Select mock location app**. The page has a link to those settings.
2. Turn on system Location Services and **keep it on while simulating**. Grant location access when Sakamoto requests it for the foreground service and location measurement. Android does not require switching Location off to start or refresh simulation.
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
