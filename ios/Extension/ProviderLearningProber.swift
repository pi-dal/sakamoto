import Foundation
import Libbox

/// Binds every check to the same running core, including its DNS and protected sockets.
final class ProviderLearningProber: NSObject, MobileexperimentLearningProberProtocol, @unchecked Sendable {
    private let server: LibboxCommandServer
    private let instanceID: Int64
    init(server: LibboxCommandServer, instanceID: Int64) { self.server = server; self.instanceID = instanceID }
    func probeOrigin(_ domain: String?, port: Int32, outbound: String?, timeoutMillis: Int32, ret0_: UnsafeMutablePointer<Int32>?) throws {
        try server.probeAutomaticOrigin(domain, port: port, outboundTag: outbound, expectedInstanceID: instanceID, timeoutMillis: timeoutMillis, ret0_: ret0_)
    }
}
