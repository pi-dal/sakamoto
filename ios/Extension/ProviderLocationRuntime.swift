import Foundation
import Libbox
import SakamotoKit

/// Control, timer ticks and core openTun callbacks share a single serialized slot.
final class ProviderLocationRuntime: @unchecked Sendable {
    private let lock = NSLock()
    private var proxy: MobilelocationProxy?
    private var failure = ""
    private var fallback = ""
    private var settings: VirtualLocationSettings?
    private var playback: LocationRoutePlayback?
    private var timer: DispatchSourceTimer?
    private var generation: UInt64 = 0

    var port: Int? { lock.lock(); defer { lock.unlock() }; return proxy.map { Int($0.port()) } }

    func configure(_ next: VirtualLocationSettings, server: LibboxCommandServer) throws {
        try next.validate()
        if next.enabled {
            guard VirtualLocationStore.supportsNetworkSimulation else { throw TunnelProfile.InvalidProfile("Network-location interception is unsupported on iOS 27 and later") }
            guard let authority = try LocationAuthorityStore.load(), LocationAuthorityStore.wasTrusted(authority) else { throw TunnelProfile.InvalidProfile("Install, trust and check the location certificate in the app first") }
            lock.lock(); defer { lock.unlock() }
            let now = LocationRouteClock.now()
            var candidate: LocationRoutePlayback?
            if let route = next.route {
                if var current = playback, current.route.id == route.id, current.route.points == route.points,
                   current.route.durationSeconds == route.durationSeconds, current.route.intervalSeconds == route.intervalSeconds {
                    current.setPaused(route.paused, now: now); candidate = current
                } else { candidate = try LocationRoutePlayback(route: route, now: now) }
            }
            var value = next; value.route = nil
            if let point = candidate?.position(now: now) { value.latitude = point.latitude; value.longitude = point.longitude }
            if let proxy { try proxy.apply(value.json()) }
            else {
                let proxy = try server.newLocationProxy(authority, settingsJSON: value.json())
                do { try proxy.setFallback(fallback); self.proxy = proxy }
                catch { try? proxy.close(); throw error }
            }
            cancelTimerLocked(); playback = candidate; settings = next; settings?.latitude = value.latitude; settings?.longitude = value.longitude; failure = ""
            if candidate?.state(now: now) == "Moving" { startTimerLocked(interval: next.route!.intervalSeconds) }
        } else {
            lock.lock(); defer { lock.unlock() }
            var fixed = next; fixed.route = nil
            try proxy?.apply(fixed.json())
            cancelTimerLocked(); playback = nil; settings = fixed; failure = ""
        }
    }
    private func cancelTimerLocked() { generation &+= 1; timer?.cancel(); timer = nil }
    private func startTimerLocked(interval: Int) {
        let ticket = generation
        let timer = DispatchSource.makeTimerSource(queue: DispatchQueue(label: "sakamoto.location.route", qos: .utility))
        timer.schedule(deadline: .now() + .seconds(interval), repeating: .seconds(interval), leeway: .milliseconds(100))
        timer.setEventHandler { [weak self] in self?.tick(ticket: ticket) }
        self.timer = timer; timer.resume()
    }
    private func tick(ticket: UInt64) {
        lock.lock(); defer { lock.unlock() }
        guard ticket == generation, let proxy, let playback, var value = settings else { return }
        let now = LocationRouteClock.now(), point = playback.position(now: now)
        value.latitude = point.latitude; value.longitude = point.longitude; value.route = nil
        do {
            try proxy.apply(value.json())
            settings?.latitude = value.latitude; settings?.longitude = value.longitude
            if playback.state(now: now) == "Completed" {
                cancelTimerLocked(); try VirtualLocationStore.save(value)
            } else {
                let remaining = Double(playback.route.durationSeconds) - playback.elapsed(now: now)
                if remaining < Double(playback.route.intervalSeconds) { timer?.schedule(deadline: .now() + remaining, repeating: .never) }
            }
        } catch {
            cancelTimerLocked(); self.failure = "Route update failed; stop simulation and retry"
            value.enabled = false; try? proxy.apply(value.json())
        }
    }
    func restore(server: LibboxCommandServer) {
        var settings = VirtualLocationStore.load(); settings.route = nil
        guard settings.enabled else { return }
        do { try configure(settings, server: server) }
        catch { lock.lock(); failure = TunnelDiagnostics.sanitized(error.localizedDescription); lock.unlock() }
    }
    func setFallback(_ address: String) {
        lock.lock(); defer { lock.unlock() }; fallback = address
        do { try proxy?.setFallback(address) } catch { failure = "Could not preserve the existing HTTP proxy" }
    }
    func snapshot() -> VirtualLocationSnapshot {
        lock.lock(); defer { lock.unlock() }
        var value = proxy.flatMap { try? JSONDecoder().decode(VirtualLocationSnapshot.self, from: Data($0.snapshotJSON().utf8)) } ?? VirtualLocationSnapshot()
        if let settings { value.latitude = settings.latitude; value.longitude = settings.longitude }
        if let playback {
            let now = LocationRouteClock.now()
            value.route = playback.route; value.routeState = playback.state(now: now); value.routeElapsed = playback.elapsed(now: now)
            if value.routeState == "Completed", timer != nil { value.routeState = "Moving" }
        }
        if !failure.isEmpty { value.error = failure; if playback != nil { value.routeState = "Error" } }
        return value
    }
    func close() {
        lock.lock(); cancelTimerLocked()
        if let playback, var value = settings {
            let point = playback.position(now: LocationRouteClock.now())
            value.latitude = point.latitude; value.longitude = point.longitude; value.route = nil; try? VirtualLocationStore.save(value)
        }
        playback = nil; settings = nil; let previous = proxy; proxy = nil; lock.unlock()
        try? previous?.close()
    }
}
