import XCTest
import SakamotoKit
@testable import Sakamoto

@MainActor
final class HomeLifecycleTests: XCTestCase {
    final class Tunnel: TunnelControlling, @unchecked Sendable {
        var state: ServiceState = .running
        var failStatus = false
        var pings = 0, connects = 0, disconnects = 0, reloads = 0, subscriptions = 0
        var continuation: AsyncStream<TunnelObservation>.Continuation?
        var suspendStatus = false
        var statusContinuation: CheckedContinuation<ServiceState, Error>?
        @MainActor func status() async throws -> ServiceState {
            if suspendStatus { return try await withCheckedThrowingContinuation { statusContinuation = $0 } }
            if failStatus { throw TestError.unavailable }
            return state
        }
        func ping() async throws -> TunnelStateSnapshot { pings += 1; throw TestError.unavailable }
        func connect(options: TunnelStartOptions) async throws { connects += 1 }
        func disconnect() async throws { disconnects += 1 }
        func reload(configContent: String) async throws { reloads += 1 }
        func observations() -> AsyncStream<TunnelObservation> {
            subscriptions += 1
            return AsyncStream { continuation = $0; $0.yield(.init(serviceState: state)) }
        }
        func emit(_ state: ServiceState) { self.state = state; continuation?.yield(.init(serviceState: state)) }
        enum TestError: Error { case unavailable }
    }
    final class Commands: CoreCommanding, @unchecked Sendable {
        var active = true
        var stops = 0, subscriptions = 0, groupSubscriptions = 0
        var mode: RoutingMode = .rule
        var continuation: AsyncStream<Bool>.Continuation?
        func start() {}
        func stop() { stops += 1; active = false; continuation?.yield(false) }
        var isAvailable: Bool { active }
        var lastChannelError: String? { nil }
        func availability() -> AsyncStream<Bool> {
            subscriptions += 1
            return AsyncStream { continuation = $0; $0.yield(active) }
        }
        func currentClashMode() -> RoutingMode? { mode }
        func setClashMode(_ mode: RoutingMode) async throws {}
        func selectOutbound(groupTag: String, outboundTag: String) async throws {}
        func urlTest(outboundTag: String) async throws {}
        func closeConnection(id: String) async throws {}
        func groups() -> AsyncStream<[GroupSnapshot]> { groupSubscriptions += 1; return AsyncStream { $0.yield([]) } }
        func traffic() -> AsyncStream<TrafficSnapshot> { AsyncStream { $0.finish() } }
        func connections() -> AsyncStream<[ConnectionRecord]> { AsyncStream { $0.finish() } }
        func logs() -> AsyncStream<[String]> { AsyncStream { $0.finish() } }
    }
    private var suite = ""
    private var defaults: UserDefaults!
    override func setUp() async throws {
        try AppServiceSetup.apply()
        suite = "home-lifecycle-" + UUID().uuidString
        defaults = UserDefaults(suiteName: suite)!
    }
    override func tearDown() async throws { defaults.removePersistentDomain(forName: suite) }
    private func makeModel(_ tunnel: Tunnel, _ commands: Commands) -> HomeModel {
        HomeModel(tunnel: tunnel, commanding: commands, store: ConfigStore(persistence: defaults, keyStore: ConfigStoreTests.EmptyKeys()))
    }
    private func waitUntil(_ condition: () -> Bool) async throws {
        for _ in 0..<200 {
            if condition() { return }
            try await Task.sleep(nanoseconds: 10_000_000)
        }
        XCTFail("Observation did not arrive")
    }
    func testRepeatedOpeningReadsSystemStateWithoutIPCOrReconnect() async throws {
        let tunnel = Tunnel(), commands = Commands(), model = makeModel(tunnel, commands)
        XCTAssertFalse(model.hasLoadedStatus)
        for _ in 0..<10 { await model.activate() }
        try await waitUntil { commands.subscriptions == 1 && commands.groupSubscriptions == 1 }
        XCTAssertTrue(model.hasLoadedStatus)
        XCTAssertEqual(model.serviceState, .running)
        XCTAssertEqual(tunnel.subscriptions, 1)
        XCTAssertEqual(commands.subscriptions, 1)
        XCTAssertEqual(commands.groupSubscriptions, 1)
        XCTAssertEqual(tunnel.pings, 0)
        XCTAssertEqual(tunnel.connects + tunnel.disconnects + tunnel.reloads, 0)
        XCTAssertEqual(commands.stops, 0)
        commands.mode = .global
        await model.activate()
        XCTAssertEqual(model.routingMode, .global)
        XCTAssertEqual(commands.subscriptions, 1)
        XCTAssertEqual(commands.groupSubscriptions, 1)
    }
    func testReadFailureDoesNotReplaceKnownRunningState() async throws {
        let tunnel = Tunnel(), commands = Commands(), model = makeModel(tunnel, commands)
        await model.activate()
        try await waitUntil { model.commandChannelActive }
        tunnel.failStatus = true
        await model.activate()
        XCTAssertEqual(model.serviceState, .running)
        XCTAssertTrue(model.commandChannelActive)
        XCTAssertEqual(commands.stops, 0)
        XCTAssertEqual(tunnel.pings, 0)
        XCTAssertNotNil(model.notice)
        tunnel.failStatus = false
        await model.refreshProviderState()
        XCTAssertNil(model.notice)
    }
    func testOlderReadCannotOverwriteNewStopObservation() async throws {
        let tunnel = Tunnel(), commands = Commands(), model = makeModel(tunnel, commands)
        await model.activate()
        try await waitUntil { model.commandChannelActive }
        tunnel.suspendStatus = true
        let read = Task { await model.refreshProviderState() }
        try await waitUntil { tunnel.statusContinuation != nil }
        tunnel.emit(.stopped)
        try await waitUntil { model.serviceState == .stopped }
        tunnel.statusContinuation?.resume(returning: .running)
        await read.value
        XCTAssertEqual(model.serviceState, .stopped)
        XCTAssertEqual(commands.stops, 1)
    }
    func testReassertingKeepsChannelButActualStopClosesIt() async throws {
        let tunnel = Tunnel(), commands = Commands(), model = makeModel(tunnel, commands)
        await model.activate()
        try await waitUntil { model.commandChannelActive }
        tunnel.emit(.starting)
        try await waitUntil { model.serviceState == .starting }
        XCTAssertEqual(commands.stops, 0)
        XCTAssertTrue(model.commandChannelActive)
        tunnel.emit(.stopped)
        try await waitUntil { !model.commandChannelActive }
        XCTAssertEqual(commands.stops, 1)
        XCTAssertEqual(model.serviceState, .stopped)
    }
}
