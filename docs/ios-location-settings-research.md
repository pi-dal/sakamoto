# iOS Location Services settings and WLOC recovery

Research date: 2026-10-14. Trigger: the released iOS guide opened the wrong Settings page and prescribed switching Location Services off, then immediately on.

## Findings

### The global switch does not select network positioning instead of GPS

[Apple's Location Services explanation](https://support.apple.com/en-us/102515), published May 20, 2026, states that Location Services combines GPS, Bluetooth, Wi-Fi and cellular information. When the global switch is off, apps cannot use Location Services in the foreground or background. Turning it off is therefore a pause, not a GPS-only exclusion or a way to force apps to accept WLOC coordinates.

Sakamoto's proxy can remain configured while the switch is off. That does not establish that iOS will issue new positioning requests or publish a new location to apps. The app must respect the user's choice to leave location off; it can explain why a requested measurement cannot proceed, without presenting the off state as a simulation error.

### The reference has intervening steps before location is enabled again

Inspected [xweiba/location-spoofer](https://github.com/xweiba/location-spoofer) revision `4aa4c6aa6cf0ae0d134c317c4c36eb90f21b21b7`:

- [ActivationTipContent](https://github.com/xweiba/location-spoofer/blob/4aa4c6aa6cf0ae0d134c317c4c36eb90f21b21b7/App/TipViews.swift) recommends airplane mode, Wi-Fi off, global Location Services off, restoring Wi-Fi and starting/confirming the proxy, leaving airplane mode, and finally enabling Location Services and verifying in a map.
- Its fallback says to restart at the location-off step and resume at the network/proxy restoration step.
- The [README](https://github.com/xweiba/location-spoofer/blob/4aa4c6aa6cf0ae0d134c317c4c36eb90f21b21b7/README.md) says caches may take time to refresh and suggests restarting if the system or target app retains an old location.

These are reference-maintainer troubleshooting recommendations, not Apple documentation or an independently reproduced guarantee. Search results claiming that iOS 26 always needs a restart or that only a restart can clear its cache are insufficient to establish those universal claims. Sakamoto should not invent a two-second wait or promise a complete cache reset.

The normal guide now goes directly from explicit VPN/simulation start to verification. An optional persisted recovery guide explains **off → restart → reconnect VPN and explicitly resume simulation while off → on and verify**. Keeping the proxy ready before enabling location explains why the off/on actions exist and what happens between them. Restart is manual and optional; the app cannot change the global switch or restart the phone.

### Settings URL acceptance is not navigation confirmation

The released URL was `App-Prefs:Privacy&path=LOCATION`. Community catalogs list the root/path form [`prefs:root=Privacy&path=LOCATION`](https://wesleydegroot.nl/blog/ios-settings-urls). The app now uses `App-Prefs:root=Privacy&path=LOCATION`, and the same root form for its profile and certificate-trust destinations.

Apple's [openSettingsURLString](https://developer.apple.com/documentation/uikit/uiapplication/opensettingsurlstring) documents the app's own settings. It does not document a public global-Location-Services destination. `App-Prefs` paths are OS-dependent. The open callback reports whether the URL was accepted, not which Settings screen is displayed. A second URL cannot be tried based on silent misnavigation that the app cannot detect.

All setup destinations therefore display their manual navigation path, use “Try opening…” labels, and keep the path in the app's status message even on accepted opens. Rejected opens still fall back to app settings with a manual recovery message.

## Validation limits

The available runtime is iOS 27.0 Simulator; no iOS 26 runtime or physical-device location test is available here. Simulator accepted `App-Prefs:root=Privacy&path=LOCATION` and a bundle-name variant, but both displayed the Settings root. It rejected `prefs:root=Privacy&path=LOCATION` and `settings-navigation://com.apple.Settings.PrivacySecurity/LOCATION` with error 115. Thus simulator acceptance is explicitly not claimed as a verified iOS 26 location-settings shortcut.

Unit and native render checks cover respecting Location Services off, explicit simulation control while off, guide persistence, and phone/tablet copy. Actual iOS 26 Settings navigation, WLOC requests after restart, GPS precedence, and target-app behavior remain physical-device checks.
