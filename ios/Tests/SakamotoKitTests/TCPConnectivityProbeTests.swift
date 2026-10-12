import XCTest
import Network
@testable import SakamotoKit

@MainActor
final class TCPConnectivityProbeTests: XCTestCase {
    func testActualHandshakeClosesItsSocketAndIncludesMeasuredTarget() async throws {
        let listener = try NWListener(using: .tcp, on: .any)
        let ready = expectation(description: "Local TCP listener ready")
        let closed = expectation(description: "Probe closes its connection")
        let queue = DispatchQueue(label: "tcp-probe-test")
        listener.stateUpdateHandler = { if case .ready = $0 { ready.fulfill() } }
        listener.newConnectionHandler = { connection in
            connection.start(queue: queue)
            connection.receive(minimumIncompleteLength: 1, maximumLength: 1) { _, _, complete, error in
                if complete || error != nil { closed.fulfill() }
                connection.cancel()
            }
        }
        listener.start(queue: queue)
        defer { listener.cancel() }
        await fulfillment(of: [ready], timeout: 3)
        let port = try XCTUnwrap(listener.port).rawValue
        let result = await TCPConnectivityProbe.check(address: "127.0.0.1", port: port, timeout: 2)
        XCTAssertTrue(result.connected, result.report)
        XCTAssertTrue(result.report.contains("[127.0.0.1]:\(port)"))
        XCTAssertTrue(result.report.contains("handshake alone"))
        XCTAssertNil(result.nativeError)
        await fulfillment(of: [closed], timeout: 3)
    }

    func testSSHResponseIsVerifiedSeparatelyFromTheTCPHandshake() async throws {
        let listener = try NWListener(using: .tcp, on: .any)
        let ready = expectation(description: "SSH listener ready")
        let queue = DispatchQueue(label: "ssh-greeting-test")
        listener.stateUpdateHandler = { if case .ready = $0 { ready.fulfill() } }
        listener.newConnectionHandler = { connection in
            connection.start(queue: queue)
            connection.send(content: Data("Server notice\r\nSSH-2.0-".utf8), completion: .contentProcessed { _ in
                connection.send(content: Data("local_test\r\n".utf8), completion: .contentProcessed { _ in })
            })
            connection.receive(minimumIncompleteLength: 1, maximumLength: 1) { _, _, _, _ in connection.cancel() }
        }
        listener.start(queue: queue)
        defer { listener.cancel() }
        await fulfillment(of: [ready], timeout: 3)
        let result = await TCPConnectivityProbe.check(address: "127.0.0.1", port: try XCTUnwrap(listener.port).rawValue, verifySSH: true, timeout: 2)
        XCTAssertTrue(result.connected, result.report)
        XCTAssertTrue(result.checkedSSH)
        XCTAssertEqual(result.sshGreeting?.trimmingCharacters(in: .whitespaces), "SSH-2.0-local_test")
        XCTAssertEqual(result.detail, "SSH greeting received; service responded")
    }

    func testSilentServiceDoesNotBecomeAVerifiedSSHService() async throws {
        let listener = try NWListener(using: .tcp, on: .any)
        let ready = expectation(description: "Silent listener ready")
        let queue = DispatchQueue(label: "silent-greeting-test")
        listener.stateUpdateHandler = { if case .ready = $0 { ready.fulfill() } }
        listener.newConnectionHandler = { connection in
            connection.start(queue: queue)
            connection.receive(minimumIncompleteLength: 1, maximumLength: 1) { _, _, _, _ in connection.cancel() }
        }
        listener.start(queue: queue)
        defer { listener.cancel() }
        await fulfillment(of: [ready], timeout: 3)
        let result = await TCPConnectivityProbe.check(address: "127.0.0.1", port: try XCTUnwrap(listener.port).rawValue, verifySSH: true, timeout: 0.15)
        XCTAssertTrue(result.connected, result.report)
        XCTAssertNil(result.sshGreeting)
        XCTAssertTrue(result.detail.contains("no SSH greeting"))
        XCTAssertLessThan(result.elapsedMS, 2000)
    }

    func testCancellationBeforeStartDoesNotOpenOrWaitForASocket() async {
        let task = Task { await TCPConnectivityProbe.check(address: "127.0.0.1", port: 22, timeout: 30) }
        task.cancel()
        let result = await task.value
        XCTAssertFalse(result.connected)
        XCTAssertEqual(result.detail, "Check cancelled")
        XCTAssertLessThan(result.elapsedMS, 2000)
    }

    func testInvalidPortIsAnExplicitFailure() async {
        let result = await TCPConnectivityProbe.check(address: "127.0.0.1", port: 0)
        XCTAssertFalse(result.connected)
        XCTAssertEqual(result.detail, "Use a port from 1 to 65535")
    }
}
