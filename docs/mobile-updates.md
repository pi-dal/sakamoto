# Mobile app updates

Open **Settings → App updates** on either platform. Checking is manual; opening the app does not start a download or installation.

## Android: official GitHub APKs

1. Tap **Check for updates**. The updater selects the highest stable `android-vX.Y.Z` release among the most recent 90 public releases, independently of iOS and CLI tags. Drafts and prereleases are excluded.
2. Review the version, build, architecture, size and release notes. Tap **Download update** to download over the current connection. Keep the page open until it finishes; leaving the page cancels unfinished work. Rotation preserves the operation. Interrupted downloads restart from the beginning.
3. Tap **Install update** once the installer has been verified. On Android 8 or newer, enable **Allow from this source** for Sakamoto if requested, return, and tap **Install update** again. On Android 7, the system installer manages the global unknown-sources setting.
4. Confirm installation in Android’s installer. The signed update replaces the app and keeps its settings. Cancelling installation leaves the verified download available to retry. A completed download can be restored when this page is reopened, provided Android has not evicted the cache.

The updater consumes the existing public `release-metadata.json`. It checks application ID, clean/non-debug release metadata, version name and code, preferred `Build.SUPPORTED_ABIS` architecture (with universal fallback), exact asset filename and GitHub download URL, byte length, SHA-256, and the GitHub asset digest when supplied. Before installation, it rechecks the entire cached file and uses Android’s APK parser to verify the package identity, build advancement, non-debug manifest, and signing certificate against the currently installed app. A different signer, including a debug build, cannot use this channel to overwrite its installation. Certificate rotation requires an explicit future migration.

Files stay in the private update cache. An unexported FileProvider grants access to the APK URI for the system installation request; the app requests neither shared-storage access nor silent-install privileges. GitHub metadata and asset requests use HTTPS, bounded responses and redirects restricted to GitHub’s API/download hosts. No GitHub login or token is embedded. HTTP 403/429, network failures, invalid metadata, storage errors and corrupted downloads remain retryable; **Open GitHub releases** provides a manual fallback.

## iOS: App Store and TestFlight

Choose **TestFlight** or **App Store**, then tap **Check for updates**. TestFlight/development receipts default to TestFlight; App Store receipts default to App Store.

- **TestFlight** compares the installed marketing version with the highest public `ios-vX.Y.Z` announcement among the latest 90 GitHub releases, including prereleases. **Open TestFlight** opens the TestFlight app; if it is unavailable, it opens Apple’s TestFlight download page. Existing testers select Sakamoto; new testers need an invitation. There is currently no public invitation URL configured.
- **App Store** queries Apple’s public lookup endpoint for Sakamoto’s app ID (`6820372642`) and the device locale’s region. It validates the returned bundle ID and app ID. If published there, it shows the store version, minimum iOS version and release notes, and **Open App Store** opens the official listing. A zero-result lookup is displayed as no release in that region, rather than as an up-to-date result. At implementation time this app is distributed through internal TestFlight and has no public App Store listing.

The public TestFlight announcement check cannot enumerate a tester’s private builds or detect a higher build number with an unchanged marketing version. The page explains this and keeps **Open TestFlight** available so Apple can show eligible builds. Do not embed App Store Connect credentials to work around that boundary. iOS performs no APK/IPA download or installation inside Sakamoto.

## Verification

- Android unit tests cover mixed-platform feeds, stable-channel filtering, numerical version comparison, ABI selection/fallback, metadata and asset tampering, cache record round trips, identity/build/signature rejection and redirect restrictions.
- Android native smoke checks (`-e updates true`) use a staged officially signed APK and explicitly synthetic installed-version facts to exercise download verification, Android’s signature parser, restored cache verification, signer mismatch, corrupt checksum/size rejection, partial-file cleanup and native page states. Stage the fixture at the test app’s private `cache/update-test/official.apk` first. Native captures cover phone/tablet and dark mode with enlarged text.
- iOS tests cover platform/version filtering, App Store identity and absence, cancellation/retry, and stale-result rejection on channel changes. Optional native captures use `TEST_RUNNER_SAKAMOTO_UPDATE_REVIEW_DIR` and cover phone/tablet in light and enlarged dark appearances.

Neither native lane performs an unattended app replacement. Final installation confirmation belongs to the platform installer or Apple’s store.
