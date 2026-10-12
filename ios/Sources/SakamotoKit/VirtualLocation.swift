import Foundation

public struct VirtualLocationSettings: Codable, Equatable, Sendable {
    public var enabled = false
    public var latitude: Double = 0
    public var longitude: Double = 0
    public var accuracy = 25
    public init(enabled: Bool = false, latitude: Double = 0, longitude: Double = 0, accuracy: Int = 25) {
        self.enabled = enabled; self.latitude = latitude; self.longitude = longitude; self.accuracy = accuracy
    }
    public func validate() throws {
        guard latitude.isFinite, longitude.isFinite, (-90...90).contains(latitude), (-180...180).contains(longitude), (1...1000).contains(accuracy) else {
            throw TunnelProfile.InvalidProfile("Use latitude -90 to 90, longitude -180 to 180, and accuracy 1 to 1000 meters")
        }
    }
    public func json() throws -> String { try validate(); return String(decoding: try JSONEncoder().encode(self), as: UTF8.self) }
}

public struct VirtualLocationSnapshot: Codable, Equatable, Sendable {
    public var enabled = false
    public var connections: Int64 = 0
    public var requests: Int64 = 0
    public var patched: Int64 = 0
    public var skipped: Int64 = 0
    public var tlsFailures: Int64 = 0
    public var lastRequest: Int64 = 0
    public var lastPatch: Int64 = 0
    public var error = ""
    public init() {}
}

public struct SavedLocation: Codable, Equatable, Identifiable, Sendable {
    public var id: UUID
    public var name: String
    public var latitude: Double
    public var longitude: Double
    public init(name: String, latitude: Double, longitude: Double) {
        self.id = UUID(); self.name = String(name.trimmingCharacters(in: .whitespacesAndNewlines).prefix(64)); self.latitude = latitude; self.longitude = longitude
    }
}

public enum VirtualLocationStore {
    public static let key = "sakamoto.location.settings"
    private static let favoritesKey = "sakamoto.location.favorites"
    public static var defaults: UserDefaults? { UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier) }
    public static func load(defaults: UserDefaults? = defaults) -> VirtualLocationSettings {
        guard let data = defaults?.data(forKey: key), let value = try? JSONDecoder().decode(VirtualLocationSettings.self, from: data), (try? value.validate()) != nil else { return VirtualLocationSettings() }
        return value
    }
    public static func save(_ value: VirtualLocationSettings, defaults: UserDefaults? = defaults) throws {
        try value.validate(); defaults?.set(try JSONEncoder().encode(value), forKey: key)
    }
    public static func favorites(defaults: UserDefaults? = defaults) -> [SavedLocation] {
        guard let data = defaults?.data(forKey: favoritesKey) else { return [] }
        return ((try? JSONDecoder().decode([SavedLocation].self, from: data)) ?? []).filter {
            !$0.name.isEmpty && $0.latitude.isFinite && $0.longitude.isFinite && (-90...90).contains($0.latitude) && (-180...180).contains($0.longitude)
        }.prefix(20).map { $0 }
    }
    public static func saveFavorites(_ values: [SavedLocation], defaults: UserDefaults? = defaults) throws {
        defaults?.set(try JSONEncoder().encode(Array(values.prefix(20))), forKey: favoritesKey)
    }
    public static var supportsNetworkSimulation: Bool { ProcessInfo.processInfo.operatingSystemVersion.majorVersion < 27 }
}
