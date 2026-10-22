import XCTest
import Libbox
import SakamotoKit
import Darwin
@testable import Sakamoto

/// Native Go core + local command transport + real DataModel consumers;
/// synthetic direct-only configuration, no TUN, accounts or private sources.
@MainActor
final class NativePowerIntegrationTests: XCTestCase {
    func testDetailedStreamsFollowVisibilityAndPreserveTheRunningCore() async throws {
        try AppServiceSetup.apply()
        let shared = try AppPaths.sharedDirectory()
        let setup = LibboxSetupOptions()
        setup.basePath = shared.path
        setup.workingPath = shared.appendingPathComponent("Library/Caches/Working").path
        setup.tempPath = shared.appendingPathComponent("Library/Caches").path
        // Simulator container paths exceed sockaddr_un's 104-byte limit.
        // Use the core's authenticated loopback transport in this fixture.
        setup.commandServerListenPort = try freePort()
        setup.commandServerSecret = UUID().uuidString
        var setupError: NSError?
        LibboxSetup(setup, &setupError)
        if let setupError { throw setupError }
        defer { try? AppServiceSetup.apply() }
        let platform = ObservationTestPlatform()
        var error: NSError?
        var server = try XCTUnwrap(LibboxNewCommandServer(platform, platform, &error))
        if let error { throw error }
        let commanding = LibboxCoreCommanding()
        let data = DataModel(commanding: commanding)
        defer { data.deactivate(); commanding.stop(); try? server.closeService(); server.close() }
        try server.start()
        let config = """
        {"log":{"level":"warn"},"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"MainProxy","outbounds":["direct"]}],"route":{"rules":[{"clash_mode":"global","action":"route","outbound":"MainProxy"},{"clash_mode":"direct","action":"route","outbound":"direct"}],"final":"MainProxy"},"experimental":{"clash_api":{"default_mode":"rule"}}}
        """
        try server.startOrReloadService(config, options: LibboxOverrideOptions())
        let instance = server.runningInstanceID()
        commanding.start()
        try await eventually { commanding.isAvailable }
        let actionClient = try XCTUnwrap(commanding.currentClient)
        var samples = 0
        let stream = commanding.traffic()
        let consumer = Task { for await _ in stream { samples += 1 } }
        defer { consumer.cancel() }
        try await Task.sleep(nanoseconds: 1_200_000_000)
        XCTAssertEqual(samples, 0)
        var evidence: [[String: Any]] = [["stage": "hidden", "samples": samples]]
        for _ in 0..<3 {
            commanding.setDataVisible(true); data.activate()
            let before = samples
            try await eventually { samples > before && data.traffic != nil }
            XCTAssertTrue(commanding.currentClient === actionClient)
            XCTAssertEqual(server.runningInstanceID(), instance)
            evidence.append(["stage": "data", "samples": samples, "same_core": true])
            commanding.setDataVisible(false); data.deactivate()
            let hidden = samples
            try await Task.sleep(nanoseconds: 1_200_000_000)
            XCTAssertEqual(samples, hidden)
            XCTAssertTrue(commanding.currentClient === actionClient)
        }
        for _ in 0..<10 { commanding.setDataVisible(true); commanding.setDataVisible(false) }
        commanding.setForeground(false)
        XCTAssertFalse(commanding.isAvailable)
        try await Task.sleep(nanoseconds: 1_200_000_000)
        let hidden = samples
        commanding.setForeground(true)
        try await eventually { commanding.isAvailable }
        try await Task.sleep(nanoseconds: 1_200_000_000)
        XCTAssertEqual(samples, hidden)
        XCTAssertEqual(server.runningInstanceID(), instance)
        try await commanding.setClashMode(.direct)
        try await eventually { commanding.currentClashMode() == .direct }
        evidence.append(["stage": "foreground", "same_core": true, "action": "direct"])
        let previousClient = commanding.currentClient
        try server.closeService(); server.close()
        try await eventually { !commanding.isAvailable }
        server = try XCTUnwrap(LibboxNewCommandServer(platform, platform, &error))
        if let error { throw error }
        try server.start()
        try server.startOrReloadService(config, options: LibboxOverrideOptions())
        try await eventually { commanding.isAvailable }
        let recovered = commanding.currentClient
        XCTAssertFalse(recovered === previousClient)
        try await Task.sleep(nanoseconds: 1_200_000_000)
        XCTAssertTrue(commanding.isAvailable && commanding.currentClient === recovered)
        evidence.append(["stage": "transport_recovery", "stable_attachment": true])
        let attachment = XCTAttachment(data: try JSONSerialization.data(withJSONObject: evidence, options: [.prettyPrinted, .sortedKeys]), uniformTypeIdentifier: "public.json")
        attachment.name = "power-observation"; attachment.lifetime = .keepAlways
        add(attachment)
    }

    private func freePort() throws -> Int32 {
        let descriptor = socket(AF_INET, SOCK_STREAM, 0)
        guard descriptor >= 0 else { throw NSError(domain: NSPOSIXErrorDomain, code: Int(errno)) }
        defer { Darwin.close(descriptor) }
        var address = sockaddr_in()
        address.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        address.sin_family = sa_family_t(AF_INET)
        address.sin_addr.s_addr = inet_addr("127.0.0.1")
        var size = socklen_t(MemoryLayout<sockaddr_in>.size)
        let result = withUnsafeMutablePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { socketAddress in
                guard Darwin.bind(descriptor, socketAddress, size) == 0 else { return Int32(-1) }
                return getsockname(descriptor, socketAddress, &size)
            }
        }
        guard result == 0 else { throw NSError(domain: NSPOSIXErrorDomain, code: Int(errno)) }
        return Int32(UInt16(bigEndian: address.sin_port))
    }

    private func eventually(_ condition: () -> Bool) async throws {
        let end = Date().addingTimeInterval(5)
        while !condition() && Date() < end { try await Task.sleep(nanoseconds: 25_000_000) }
        XCTAssertTrue(condition(), "Observation transition did not complete")
    }
}

final class ObservationTestPlatform: NSObject, LibboxPlatformInterfaceProtocol, LibboxCommandServerHandlerProtocol {
    private func unsupported() -> NSError { NSError(domain: "SyntheticObservationServer", code: 1) }
    func openTun(_ options: LibboxTunOptionsProtocol?, ret0_: UnsafeMutablePointer<Int32>?) throws { throw unsupported() }
    func usePlatformAutoDetectControl() -> Bool { false }
    func autoDetectControl(_ fd: Int32) throws {}
    func startDefaultInterfaceMonitor(_ listener: LibboxInterfaceUpdateListenerProtocol?) throws {
        listener?.updateDefaultInterface("", interfaceIndex: -1, isExpensive: false, isConstrained: false)
    }
    func closeDefaultInterfaceMonitor(_ listener: LibboxInterfaceUpdateListenerProtocol?) throws {}
    func getInterfaces() throws -> any LibboxNetworkInterfaceIteratorProtocol { ObservationInterfaces() }
    func underNetworkExtension() -> Bool { false }
    func includeAllNetworks() -> Bool { false }
    func clearDNSCache() {}
    func readWIFIState() -> LibboxWIFIState? { nil }
    func tailscaleHostname() -> String { "observation-test" }
    func useProcFS() -> Bool { false }
    func findConnectionOwner(_ ipProtocol: Int32, sourceAddress: String?, sourcePort: Int32, destinationAddress: String?, destinationPort: Int32) throws -> LibboxConnectionOwner { throw unsupported() }
    func startNeighborMonitor(_ listener: LibboxNeighborUpdateListenerProtocol?) throws {}
    func closeNeighborMonitor(_ listener: LibboxNeighborUpdateListenerProtocol?) throws {}
    func registerMyInterface(_ name: String?) {}
    func localDNSTransport() -> LibboxLocalDNSTransportProtocol? { nil }
    func connectSSHAgent(_ ret0_: UnsafeMutablePointer<Int32>?) throws { throw unsupported() }
    func usePlatformShell() -> Bool { false }
    func checkPlatformShell() throws { throw unsupported() }
    func openShellSession(_ user: LibboxPlatformUser?, command: String?, environ: LibboxStringIteratorProtocol?, term: String?, rows: Int32, cols: Int32) throws -> any LibboxShellSessionProtocol { throw unsupported() }
    func readSystemSSHHostKey(_ error: NSErrorPointer) -> String { "" }
    func lookupSFTPServer(_ error: NSErrorPointer) -> String { "" }
    func lookupUser(_ username: String?) throws -> LibboxPlatformUser { throw unsupported() }
    func usePlatformBridge() -> Bool { false }
    func createBridge(_ options: LibboxBridgeOptions?) throws -> any LibboxBridgeSessionProtocol { throw unsupported() }
    func serviceStop() throws {}
    func serviceReload() throws {}
    func getSystemProxyStatus() throws -> LibboxSystemProxyStatus { LibboxSystemProxyStatus() }
    func setSystemProxyEnabled(_ enabled: Bool) throws {}
    func triggerNativeCrash() throws { throw unsupported() }
    func writeDebugMessage(_ message: String?) {}
    func send(_ notification: LibboxNotification?) throws {}
    func cancelNotification(_ identifier: String?, typeID: Int32) throws {}
}

private final class ObservationInterfaces: NSObject, LibboxNetworkInterfaceIteratorProtocol {
    func hasNext() -> Bool { false }
    func next() -> LibboxNetworkInterface? { nil }
}
