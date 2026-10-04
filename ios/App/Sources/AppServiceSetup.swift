import Foundation
import Libbox
import SakamotoKit

// App-process libbox setup: the CommandClient finds the tunnel's
// CommandServer at <app-group>/command.sock through the global base path,
// so the app process must run the same LibboxSetup as the provider
// (mirrors sing-box-for-apple ServiceSetup.apply).
//
// The app-group identifier comes from the App target's Info.plist
// ("SakamotoAppGroupIdentifier") — the repository ships a development
// placeholder; see ios/README.md for the Team/App Group checklist.

enum AppPaths {
    static func appGroupIdentifier() throws -> String {
        guard let identifier = Bundle.main.object(
            forInfoDictionaryKey: "SakamotoAppGroupIdentifier"
        ) as? String else {
            throw AppSetupError("missing Info.plist key SakamotoAppGroupIdentifier")
        }
        return identifier
    }

    static func sharedDirectory() throws -> URL {
        let container = FileManager.default.containerURL(
            forSecurityApplicationGroupIdentifier: try appGroupIdentifier()
        )
        guard let container else {
            throw AppSetupError("app group is not provisioned for this target")
        }
        return container
    }
}

struct AppSetupError: LocalizedError, CustomStringConvertible {
    let description: String
    init(_ description: String) { self.description = description }
    var errorDescription: String? { description }
}

enum AppServiceSetup {
    /// Idempotent libbox setup for the app process. Crash-report sources are
    /// distinct per process so reports never overwrite each other.
    static func apply() throws {
        let shared = try AppPaths.sharedDirectory()
        let cache = shared.appendingPathComponent("Library/Caches", isDirectory: true)
        let options = LibboxSetupOptions()
        options.basePath = shared.path
        options.workingPath = cache.appendingPathComponent("Working").path
        options.tempPath = cache.path
        options.crashReportSource = "SakamotoApp"
        options.appVersion = Bundle.main.object(
            forInfoDictionaryKey: "CFBundleShortVersionString"
        ) as? String ?? ""
        var error: NSError?
        LibboxSetup(options, &error)
        if let error {
            throw error
        }
    }
}
