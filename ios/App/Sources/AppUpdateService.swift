import Foundation

struct AppUpdateVersion: Comparable, Equatable {
    let components: [Int]
    init?(_ text: String) {
        let pieces = text.split(separator: ".", omittingEmptySubsequences: false)
        guard (1...3).contains(pieces.count), pieces.allSatisfy({ !$0.isEmpty && $0.allSatisfy { $0.isASCII && $0.isNumber } }) else { return nil }
        let values = pieces.compactMap { Int($0) }
        guard values.count == pieces.count else { return nil }
        components = values + Array(repeating: 0, count: 3 - values.count)
    }
    static func < (lhs: Self, rhs: Self) -> Bool { lhs.components.lexicographicallyPrecedes(rhs.components) }
}

enum AppUpdateChannel: String, CaseIterable, Identifiable {
    case testFlight, appStore
    var id: String { rawValue }
    var title: String { self == .testFlight ? "TestFlight" : "App Store" }
}

struct IOSUpdateRelease: Decodable {
    let tag_name: String
    let draft: Bool
    let prerelease: Bool
    let body: String?
    var version: String { String(tag_name.dropFirst("ios-v".count)) }
    var pageURL: URL { URL(string: "https://github.com/pi-dal/sakamoto/releases/tag/\(tag_name)")! }
    static func latest(in data: Data) throws -> IOSUpdateRelease? {
        try JSONDecoder().decode([IOSUpdateRelease].self, from: data).filter {
            !$0.draft && $0.tag_name.hasPrefix("ios-v") && $0.version.split(separator: ".").count == 3 && AppUpdateVersion($0.version) != nil
        }.max { AppUpdateVersion($0.version)! < AppUpdateVersion($1.version)! }
    }
}

struct IOSUpdateResult: Equatable {
    let version: String?
    let notes: String
    let releaseURL: URL?
    let minimumOS: String?
    var isPublished: Bool { version != nil }
    func isNewer(than installed: String) -> Bool {
        guard let version, let latest = AppUpdateVersion(version), let current = AppUpdateVersion(installed) else { return false }
        return latest > current
    }
}

protocol IOSUpdateChecking {
    func check(_ channel: AppUpdateChannel) async throws -> IOSUpdateResult
}

struct IOSUpdateService: IOSUpdateChecking {
    static let appID = 6820372642
    static let appStoreURL = URL(string: "https://apps.apple.com/app/id\(appID)")!
    static let testFlightStoreURL = URL(string: "https://apps.apple.com/app/testflight/id899247664")!
    let session: URLSession
    let country: String
    init(session: URLSession? = nil, country: String = Locale.current.region?.identifier ?? "US") {
        if let session { self.session = session }
        else {
            let configuration = URLSessionConfiguration.ephemeral
            configuration.timeoutIntervalForRequest = 15
            configuration.timeoutIntervalForResource = 45
            configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
            self.session = URLSession(configuration: configuration)
        }
        self.country = country
    }
    func check(_ channel: AppUpdateChannel) async throws -> IOSUpdateResult {
        if channel == .appStore {
            var components = URLComponents(string: "https://itunes.apple.com/lookup")!
            components.queryItems = [URLQueryItem(name: "id", value: String(Self.appID)), URLQueryItem(name: "country", value: country)]
            return try Self.appStoreResult(from: await document(components.url!))
        }
        var latest: IOSUpdateRelease?
        for page in 1...3 {
            try Task.checkCancellation()
            let data = try await document(URL(string: "https://api.github.com/repos/pi-dal/sakamoto/releases?per_page=30&page=\(page)")!)
            if let found = try IOSUpdateRelease.latest(in: data), latest == nil || AppUpdateVersion(found.version)! > AppUpdateVersion(latest!.version)! { latest = found }
            if (try JSONSerialization.jsonObject(with: data) as? [Any])?.count ?? 0 < 30 { break }
        }
        return IOSUpdateResult(version: latest?.version, notes: String((latest?.body ?? "").prefix(12_000)), releaseURL: latest?.pageURL, minimumOS: nil)
    }
    static func appStoreResult(from data: Data) throws -> IOSUpdateResult {
        struct Listing: Decodable {
            struct App: Decodable {
                let trackId: Int
                let bundleId: String
                let version: String
                let releaseNotes: String?
                let minimumOsVersion: String?
            }
            let resultCount: Int
            let results: [App]
        }
        let listing = try JSONDecoder().decode(Listing.self, from: data)
        guard listing.resultCount != 0 else { return IOSUpdateResult(version: nil, notes: "", releaseURL: nil, minimumOS: nil) }
        guard let app = listing.results.first(where: { $0.trackId == appID && $0.bundleId == "com.pidal.sakamoto" }), AppUpdateVersion(app.version) != nil else {
            throw UpdateError.invalidResponse
        }
        return IOSUpdateResult(version: app.version, notes: String((app.releaseNotes ?? "").prefix(12_000)), releaseURL: appStoreURL, minimumOS: app.minimumOsVersion)
    }
    private func document(_ url: URL) async throws -> Data {
        var request = URLRequest(url: url)
        request.setValue("Sakamoto-iOS-Updater", forHTTPHeaderField: "User-Agent")
        // Streaming bounds decompressed responses before decoding; no account credentials are sent.
        let (bytes, response) = try await session.bytes(for: request)
        guard let http = response as? HTTPURLResponse else { throw UpdateError.invalidResponse }
        guard (200...299).contains(http.statusCode) else { throw UpdateError.http(http.statusCode) }
        guard let final = http.url, final.scheme == "https", ["api.github.com", "itunes.apple.com"].contains(final.host ?? "") else { throw UpdateError.invalidResponse }
        var data = Data()
        for try await byte in bytes {
            guard data.count < 2_097_152 else { throw UpdateError.invalidResponse }
            data.append(byte)
        }
        return data
    }
    enum UpdateError: LocalizedError {
        case invalidResponse, http(Int)
        var errorDescription: String? {
            switch self {
            case .invalidResponse: return "The update source returned an invalid response. Try again later."
            case .http(let code) where code == 403 || code == 429: return "The update source limited this request. Try again later or open the store directly."
            case .http(let code): return "The update source returned HTTP \(code). Try again or open the store directly."
            }
        }
    }
}
