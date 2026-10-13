import UIKit

/// Settings routes vary by iOS release. Keep a visible manual path and a supported
/// app-settings fallback; opening a route never counts as completing setup.
enum LocationSetupSettingsDestination {
    case profiles, certificateTrust, locationServices

    var url: URL {
        let path: String
        switch self {
        case .profiles: path = "General&path=ManagedConfigurationList"
        case .certificateTrust: path = "General&path=About/CERT_TRUST_SETTINGS"
        case .locationServices: path = "Privacy&path=LOCATION"
        }
        return URL(string: "App-Prefs:\(path)")!
    }
    var manualPath: String {
        switch self {
        case .profiles: return "Settings → General → VPN & Device Management"
        case .certificateTrust: return "Settings → General → About → Certificate Trust Settings"
        case .locationServices: return "Settings → Privacy & Security → Location Services"
        }
    }
}
