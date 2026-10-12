import Foundation
import Network

/// A real app socket, following the OS routing table; never an endpoint-direct dial.
public enum TCPConnectivityProbe {
    public struct Outcome: Sendable, Equatable {
        public let address: String
        public let port: UInt16
        public let measuredAt: Date
        public let elapsedMS: Int
        public let connected: Bool
        public let detail: String
        public let nativeError: String?
        public let path: String
        public let checkedSSH: Bool
        public let sshGreeting: String?

        public var report: String {
            """
            App TCP check
            Time: \(ISO8601DateFormatter().string(from: measuredAt))
            Target: [\(address)]:\(port)
            Result: \(detail)
            TCP handshake: \(connected ? "completed" : "not completed")
            Service response: \(checkedSSH ? (sshGreeting ?? "SSH greeting not verified") : "not checked")
            Duration: \(elapsedMS) ms
            Observed path: \(path)
            Native error: \(nativeError ?? "none")
            This does not verify SSH authentication or another app's routing. A TCP handshake alone may be acknowledged by the VPN's user-space stack.
            """
        }
    }

    public static func check(address: String, port: UInt16, verifySSH: Bool = false, timeout: TimeInterval = 8) async -> Outcome {
        let session = Session(address: address, port: port, verifySSH: verifySSH, timeout: timeout)
        return await withTaskCancellationHandler {
            await withCheckedContinuation { session.start($0) }
        } onCancel: {
            session.cancel()
        }
    }

    private final class Session: @unchecked Sendable {
        private let queue = DispatchQueue(label: "sakamoto.tcp-check")
        private let address: String
        private let port: UInt16
        private let timeout: TimeInterval
        private let verifySSH: Bool
        private var handshakeCompleted = false
        private var received = Data()
        private var sshGreeting: String?
        private let connection: NWConnection
        private var continuation: CheckedContinuation<Outcome, Never>?
        private var deadline: DispatchWorkItem?
        private var cancelled = false
        private var lastError: NWError?
        private var startedAt = DispatchTime.now()

        init(address: String, port: UInt16, verifySSH: Bool, timeout: TimeInterval) {
            self.address = address
            self.port = port
            self.verifySSH = verifySSH
            self.timeout = timeout
            connection = NWConnection(host: NWEndpoint.Host(address), port: NWEndpoint.Port(rawValue: max(1, port))!, using: .tcp)
        }

        func start(_ continuation: CheckedContinuation<Outcome, Never>) {
            queue.async {
                self.continuation = continuation
                self.startedAt = .now()
                guard !self.cancelled else { self.finish(connected: false, detail: "Check cancelled"); return }
                guard self.port > 0 else { self.finish(connected: false, detail: "Use a port from 1 to 65535"); return }
                self.connection.stateUpdateHandler = { state in
                    switch state {
                    case .ready:
                        self.handshakeCompleted = true
                        if self.verifySSH { self.receiveGreeting() }
                        else { self.finish(connected: true, detail: "App TCP handshake completed; service response not checked") }
                    case .failed(let error):
                        self.lastError = error
                        self.finish(connected: self.handshakeCompleted, detail: self.handshakeCompleted
                            ? "App TCP handshake completed; SSH greeting not verified"
                            : (error == .posix(.ECONNREFUSED)
                                ? "Connection refused; check the listening service and firewall"
                                : "TCP connection failed; check the route, service and tailnet policy"))
                    case .waiting(let error): self.lastError = error
                    default: break
                    }
                }
                let deadline = DispatchWorkItem {
                    self.finish(connected: self.handshakeCompleted, detail: self.handshakeCompleted
                        ? "App TCP handshake completed; no SSH greeting before the deadline"
                        : "Check timed out; the route, firewall, tailnet policy or service may be blocking TCP")
                }
                self.deadline = deadline
                self.queue.asyncAfter(deadline: .now() + max(0.01, self.timeout), execute: deadline)
                self.connection.start(queue: self.queue)
            }
        }

        private func receiveGreeting() {
            guard continuation != nil else { return }
            connection.receive(minimumIncompleteLength: 1, maximumLength: 1024 - received.count) { data, _, complete, error in
                guard self.continuation != nil else { return }
                if let data { self.received.append(data) }
                let lines = self.received.split(separator: 10, omittingEmptySubsequences: false).dropLast()
                if let line = lines.first(where: { $0.starts(with: Data("SSH-2.0-".utf8)) || $0.starts(with: Data("SSH-1.99-".utf8)) }) {
                    self.sshGreeting = String(line.prefix(255).map { (32...126).contains($0) ? Character(UnicodeScalar($0)) : " " }).trimmingCharacters(in: .whitespaces)
                    self.finish(connected: true, detail: "SSH greeting received; service responded")
                } else if complete || error != nil || self.received.count >= 1024 {
                    self.lastError = error
                    self.finish(connected: true, detail: "App TCP handshake completed; no valid SSH greeting received")
                } else {
                    self.receiveGreeting()
                }
            }
        }

        func cancel() {
            queue.async {
                self.cancelled = true
                self.finish(connected: false, detail: "Check cancelled")
            }
        }

        private func finish(connected: Bool, detail: String) {
            guard let continuation else { return }
            self.continuation = nil
            deadline?.cancel()
            deadline = nil
            let path = connection.currentPath
            let interfaces: [(NWInterface.InterfaceType, String)] = [(.wifi, "Wi-Fi"), (.cellular, "cellular"), (.wiredEthernet, "Ethernet"), (.other, "other / virtual"), (.loopback, "loopback")]
            let observedPath = interfaces.filter { path?.usesInterfaceType($0.0) == true }.map(\.1).joined(separator: ", ")
            let result = Outcome(address: address, port: port, measuredAt: Date(),
                elapsedMS: Int((DispatchTime.now().uptimeNanoseconds - startedAt.uptimeNanoseconds) / 1_000_000),
                connected: connected, detail: detail, nativeError: lastError.map { String(describing: $0) },
                path: observedPath.isEmpty ? "unavailable" : observedPath, checkedSSH: verifySSH, sshGreeting: sshGreeting)
            connection.stateUpdateHandler = nil
            connection.cancel()
            continuation.resume(returning: result)
        }
    }
}
