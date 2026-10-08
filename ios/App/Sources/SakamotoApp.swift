import SwiftUI
import NetworkExtension
import SakamotoKit
import SakamotoNE

// sakamoto iOS app entry. Placeholder bundle identifiers live in
// ios/project.yml (com.pidal.sakamoto / com.pidal.sakamoto.PacketTunnel);
// the provider bundle ID must match the PacketTunnel target exactly or
// NEVPNManager refuses to start the tunnel.
//
// Page structure mirrors the macOS TUI: Home / Config / Data / Settings /
// About. The built-in Tailscale endpoint is a tool entry under Settings.
//
// Command-channel lifecycle lives in LibboxCoreCommanding (desired state +
// retries); NOTHING connects at app start. HomeModel drives start/stop from
// the tunnel observations, so first launch (no tunnel yet) boots with the
// channel honestly "Unavailable" and picks it up when the tunnel connects.

@main
struct SakamotoApp: App {
    @StateObject private var store: ConfigStore
    @StateObject private var homeModel: HomeModel
    @StateObject private var configModel: ConfigModel
    @StateObject private var settingsModel: SettingsModel
    @StateObject private var iCloudSyncModel: ICloudSyncModel
    @StateObject private var s3SyncModel: S3SyncModel

    @State private var selectedTab = 0

    private let tunnel: NETunnelController
    private let commanding: LibboxCoreCommanding

    init() {
        try? AppServiceSetup.apply()
        let tunnel = NETunnelController(
            providerBundleIdentifier: Bundle.main.object(
                forInfoDictionaryKey: "SakamotoProviderBundleIdentifier"
            ) as? String ?? "com.pidal.sakamoto.PacketTunnel"
        )
        let commanding = LibboxCoreCommanding()
        let store = ConfigStore(keyStore: TailscaleKeychainStore())
        self.tunnel = tunnel
        self.commanding = commanding
        _store = StateObject(wrappedValue: store)
        _homeModel = StateObject(wrappedValue: HomeModel(
            tunnel: tunnel,
            commanding: commanding,
            store: store
        ))
        _configModel = StateObject(wrappedValue: ConfigModel(
            tunnel: tunnel,
            store: store
        ))
        _settingsModel = StateObject(wrappedValue: SettingsModel(
            store: store,
            tunnel: tunnel,
            commanding: commanding
        ))
        _iCloudSyncModel = StateObject(wrappedValue: ICloudSyncModel(store: store))
        _s3SyncModel = StateObject(wrappedValue: S3SyncModel(store: store))
    }

    var body: some Scene {
        WindowGroup {
            TabView(selection: $selectedTab) {
                NavigationStack {
                    HomeView(model: homeModel)
                        .sakamotoInspectTag("HomeView")
                }
                .tabItem { Label("Home", systemImage: "house") }
                .tag(0)
                NavigationStack {
                    ConfigView(model: configModel, sync: iCloudSyncModel, s3: s3SyncModel, settings: settingsModel)
                        .sakamotoInspectTag("ConfigView")
                }
                .tabItem { Label("Config", systemImage: "slider.horizontal.3") }
                .tag(1)
                NavigationStack {
                    DataView(commanding: commanding)
                        .sakamotoInspectTag("DataView")
                }
                .tabItem { Label("Data", systemImage: "chart.bar") }
                .tag(2)
                NavigationStack {
                    SettingsView(model: settingsModel, sync: iCloudSyncModel, s3: s3SyncModel, commanding: commanding)
                        .sakamotoInspectTag("SettingsView")
                }
                .tabItem { Label("Settings", systemImage: "gearshape") }
                .tag(3)
            }
            .sakamotoInspector()
            .tint(.primary)
            .toggleStyle(SakamotoSwitchStyle())
            .onOpenURL { url in
                if url.scheme == "sakamoto", url.host == "home" { selectedTab = 0 }
            }
        }
    }
}
