import Foundation
import Libbox
import SakamotoKit

/// The slot serializes control and core openTun callbacks. Go owns all sockets
/// and their close semantics; the provider owns this slot for the VPN lifetime.
final class ProviderLocationRuntime: @unchecked Sendable {
    private let lock = NSLock()
    private var proxy: MobilelocationProxy?
    private var failure = ""
    private var fallback = ""

    var port: Int? { lock.lock(); defer { lock.unlock() }; return proxy.map { Int($0.port()) } }

    func configure(_ settings: VirtualLocationSettings, server: LibboxCommandServer) throws {
        try settings.validate()
        if settings.enabled {
            guard VirtualLocationStore.supportsNetworkSimulation else { throw TunnelProfile.InvalidProfile("Network-location interception is unsupported on iOS 27 and later") }
            guard let authority = try LocationAuthorityStore.load(), LocationAuthorityStore.wasTrusted(authority) else {
                throw TunnelProfile.InvalidProfile("Install, trust and check the location certificate in the app first")
            }
            lock.lock(); defer { lock.unlock() }
            if let proxy { try proxy.apply(settings.json()) }
            else {
                let next = try server.newLocationProxy(authority, settingsJSON: settings.json())
                do { try next.setFallback(fallback); proxy = next }
                catch { try? next.close(); throw error }
            }
            failure = ""
        } else {
            lock.lock(); defer { lock.unlock() }
            try proxy?.apply(settings.json()); failure = ""
        }
    }
    func restore(server: LibboxCommandServer) {
        let settings = VirtualLocationStore.load()
        guard settings.enabled else { return }
        do { try configure(settings, server: server) }
        catch { lock.lock(); failure = TunnelDiagnostics.sanitized(error.localizedDescription); lock.unlock() }
    }
    func setFallback(_ address: String) {
        lock.lock(); defer { lock.unlock() }
        fallback = address
        do { try proxy?.setFallback(address) }
        catch { failure = "Could not preserve the existing HTTP proxy" }
    }
    func snapshot() -> VirtualLocationSnapshot {
        lock.lock(); defer { lock.unlock() }
        var value = proxy.flatMap { try? JSONDecoder().decode(VirtualLocationSnapshot.self, from: Data($0.snapshotJSON().utf8)) } ?? VirtualLocationSnapshot()
        if !failure.isEmpty { value.error = failure }
        return value
    }
    func close() {
        lock.lock(); let previous = proxy; proxy = nil; lock.unlock()
        try? previous?.close()
    }
}
