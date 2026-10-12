import Foundation

public struct LocationWaypoint: Codable, Equatable, Sendable {
    public var latitude: Double
    public var longitude: Double
    public init(latitude: Double, longitude: Double) { self.latitude = latitude; self.longitude = longitude }
    public func distance(to other: Self) -> Double {
        let a = latitude * .pi / 180, b = other.latitude * .pi / 180
        let d = sin((b - a) / 2), e = sin((other.longitude - longitude) * .pi / 360)
        return 6_371_000 * 2 * asin(sqrt(min(1, max(0, d * d + cos(a) * cos(b) * e * e))))
    }
    public func interpolated(to other: Self, fraction: Double) -> Self {
        if fraction <= 0 { return self }; if fraction >= 1 { return other }
        let angle = distance(to: other) / 6_371_000
        let first = sin((1 - fraction) * angle) / sin(angle), second = sin(fraction * angle) / sin(angle)
        func vector(_ p: Self) -> (Double, Double, Double) {
            let lat = p.latitude * .pi / 180, lon = p.longitude * .pi / 180
            return (cos(lat) * cos(lon), cos(lat) * sin(lon), sin(lat))
        }
        let a = vector(self), b = vector(other)
        let x = first * a.0 + second * b.0, y = first * a.1 + second * b.1, z = first * a.2 + second * b.2
        return Self(latitude: atan2(z, hypot(x, y)) * 180 / .pi, longitude: atan2(y, x) * 180 / .pi)
    }
}

public struct VirtualLocationRoute: Codable, Equatable, Sendable {
    public var id: UUID
    public var points: [LocationWaypoint]
    public var durationSeconds: Int
    public var intervalSeconds: Int
    public var paused: Bool
    public init(points: [LocationWaypoint], durationSeconds: Int, intervalSeconds: Int = 1, paused: Bool = false) {
        id = UUID(); self.points = points; self.durationSeconds = durationSeconds; self.intervalSeconds = intervalSeconds; self.paused = paused
    }
    public func validate() throws {
        guard (2...32).contains(points.count), (10...86400).contains(durationSeconds), (1...10).contains(intervalSeconds) else {
            throw TunnelProfile.InvalidProfile("Use 2 to 32 route points, a duration of 10 to 86400 seconds and an interval of 1 to 10 seconds")
        }
        for point in points { try VirtualLocationSettings(latitude: point.latitude, longitude: point.longitude).validate() }
        for (a, b) in zip(points, points.dropFirst()) {
            let distance = a.distance(to: b)
            guard distance >= 0.01, distance < 6_371_000 * (.pi - 0.00001) else { throw TunnelProfile.InvalidProfile("Adjacent route points must differ; add an intermediate point for opposite sides of Earth") }
        }
    }
    public static func parse(_ text: String, duration: String, interval: String) throws -> Self {
        guard text.utf8.count <= 4096, let seconds = Int(duration), let tick = Int(interval) else { throw TunnelProfile.InvalidProfile("Enter whole-number route duration and interval") }
        let lines = text.split(whereSeparator: \.isNewline).filter { !$0.trimmingCharacters(in: .whitespaces).isEmpty }
        guard (2...32).contains(lines.count) else { throw TunnelProfile.InvalidProfile("Enter 2 to 32 lines, each containing latitude, longitude") }
        let points = try lines.map { line -> LocationWaypoint in
            let values = line.split(separator: ",", omittingEmptySubsequences: false)
            guard values.count == 2, let lat = Double(values[0].trimmingCharacters(in: .whitespaces)), let lon = Double(values[1].trimmingCharacters(in: .whitespaces)) else { throw TunnelProfile.InvalidProfile("Each route line must contain decimal latitude, longitude") }
            return LocationWaypoint(latitude: lat, longitude: lon)
        }
        let value = Self(points: points, durationSeconds: seconds, intervalSeconds: tick); try value.validate(); return value
    }
    public var distance: Double { zip(points, points.dropFirst()).reduce(0) { $0 + $1.0.distance(to: $1.1) } }
    public func position(elapsed: Double) -> LocationWaypoint {
        let progress = min(1, max(0, elapsed / Double(durationSeconds)))
        if progress >= 1 { return points.last! }; if progress <= 0 { return points[0] }
        var remaining = distance * progress
        for (a, b) in zip(points, points.dropFirst()) {
            let leg = a.distance(to: b)
            if remaining <= leg { return a.interpolated(to: b, fraction: remaining / leg) }; remaining -= leg
        }
        return points.last!
    }
}

@available(iOS 16.0, macOS 13.0, *)
public enum LocationRouteClock {
    private static let clock = ContinuousClock()
    private static let origin = clock.now
    public static func now() -> Double {
        let value = origin.duration(to: clock.now).components
        return Double(value.seconds) + Double(value.attoseconds) / 1e18
    }
}

/// A monotonic clock controls progress; wall-clock changes and delayed ticks do not alter speed.
public struct LocationRoutePlayback: Sendable {
    public private(set) var route: VirtualLocationRoute
    private var accumulated: Double = 0
    private var started: Double
    public init(route: VirtualLocationRoute, now: Double) throws { try route.validate(); self.route = route; started = now }
    public func elapsed(now: Double) -> Double { min(Double(route.durationSeconds), accumulated + (route.paused ? 0 : max(0, now - started))) }
    public func state(now: Double) -> String { elapsed(now: now) >= Double(route.durationSeconds) ? "Completed" : route.paused ? "Paused" : "Moving" }
    public func position(now: Double) -> LocationWaypoint { route.position(elapsed: elapsed(now: now)) }
    public mutating func setPaused(_ paused: Bool, now: Double) { accumulated = elapsed(now: now); started = now; route.paused = paused }
}
