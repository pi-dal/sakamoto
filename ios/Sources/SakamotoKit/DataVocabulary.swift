import Foundation

// Data-tab vocabulary: plain-value mirrors of the Libbox command streams
// (CommandStatus / CommandConnections / CommandLog) with ALL folding logic
// kept here so it is unit-testable without Libbox. The App-target bridge
// (LibboxCoreCommanding) only maps gomobile objects onto these values; it
// never re-derives semantics — the same boundary rule as the rest of
// SakamotoKit (state logic lives behind the bridge, Swift renders values).
//
// Wire facts (Libbox v1.14.2, daemon/started_service.proto):
//   - StatusMessage: uplink/downlink are per-interval rates; *Total are
//     since core start; trafficAvailable=false means the core cannot
//     measure yet — the UI shows "unavailable", never zeros.
//   - ConnectionEventType: NEW=0, UPDATE=1, CLOSED=2. UPDATE events may
//     carry only traffic deltas (connection field empty); CLOSED carries
//     the final deltas and closedAt.

/// One round of the status stream (libbox StatusMessage).
public struct TrafficSnapshot: Equatable, Sendable {
    public var uplink: Int64
    public var downlink: Int64
    public var uplinkTotal: Int64
    public var downlinkTotal: Int64
    public var connectionsIn: Int32
    public var connectionsOut: Int32
    /// False while the core cannot measure traffic; render "unavailable".
    public var trafficAvailable: Bool

    public init(
        uplink: Int64, downlink: Int64,
        uplinkTotal: Int64, downlinkTotal: Int64,
        connectionsIn: Int32, connectionsOut: Int32,
        trafficAvailable: Bool
    ) {
        self.uplink = uplink
        self.downlink = downlink
        self.uplinkTotal = uplinkTotal
        self.downlinkTotal = downlinkTotal
        self.connectionsIn = connectionsIn
        self.connectionsOut = connectionsOut
        self.trafficAvailable = trafficAvailable
    }
}

/// Event-type constants of daemon.ConnectionEventType (proto wire values).
public enum ConnectionEventKind: Int32, Sendable, Equatable {
    case new = 0
    case update = 1
    case closed = 2
}

/// One event of the connection stream. `record` is present for NEW (and
/// some UPDATE) events; pure-traffic UPDATE events carry only the deltas.
public struct ConnectionEvent: Equatable, Sendable {
    public var kind: ConnectionEventKind
    public var id: String
    public var record: ConnectionRecord?
    public var uplinkDelta: Int64
    public var downlinkDelta: Int64
    public var closedAt: Int64

    public init(
        kind: ConnectionEventKind, id: String, record: ConnectionRecord?,
        uplinkDelta: Int64, downlinkDelta: Int64, closedAt: Int64
    ) {
        self.kind = kind
        self.id = id
        self.record = record
        self.uplinkDelta = uplinkDelta
        self.downlinkDelta = downlinkDelta
        self.closedAt = closedAt
    }
}

/// One connection as displayed by the Data tab (the iOS counterpart of a
/// row in the TUI's Data page). `closed` is a stream fact: CLOSED events
/// mark the row without deleting it, so totals survive the close.
public struct ConnectionRecord: Equatable, Sendable, Identifiable {
    public var id: String
    public var network: String
    public var source: String
    public var destination: String
    public var domain: String
    public var outbound: String
    public var chain: [String]
    public var rule: String
    public var createdAt: Int64
    public var closedAt: Int64
    public var uplinkTotal: Int64
    public var downlinkTotal: Int64
    public var closed: Bool

    public init(
        id: String, network: String, source: String, destination: String,
        domain: String, outbound: String, chain: [String], rule: String,
        createdAt: Int64, closedAt: Int64,
        uplinkTotal: Int64, downlinkTotal: Int64, closed: Bool
    ) {
        self.id = id
        self.network = network
        self.source = source
        self.destination = destination
        self.domain = domain
        self.outbound = outbound
        self.chain = chain
        self.rule = rule
        self.createdAt = createdAt
        self.closedAt = closedAt
        self.uplinkTotal = uplinkTotal
        self.downlinkTotal = downlinkTotal
        self.closed = closed
    }

    /// What the row shows first: the sniffed domain when the core has one,
    /// else the raw destination (TUI Data page rule).
    public var displayName: String { domain.isEmpty ? destination : domain }
}

/// Rolling connection table folded from the connection-event stream —
/// the iOS counterpart of the TUI's m.conns map (internal/tui). Pure value
/// type; the bridge owns one instance and applies batches under its lock.
public struct ConnectionTable: Equatable, Sendable {
    private var records: [String: ConnectionRecord] = [:]
    public let closedCapacity: Int

    /// Keep live connections addressable; bound only completed history.
    public init(closedCapacity: Int = 300) { self.closedCapacity = max(0, closedCapacity) }

    public var count: Int { records.count }

    /// Open (not yet CLOSED) connections — the number the TUI header shows.
    public var openCount: Int {
        records.values.lazy.filter { !$0.closed }.count
    }

    public func record(id: String) -> ConnectionRecord? {
        records[id]
    }

    /// Apply one batch of events. `reset` (daemon ConnectionEvents.reset)
    /// means the core restarted its view: drop the local table first, then
    /// apply. Unknown-id UPDATE events are ignored (nothing to attach
    /// deltas to — the same choice the TUI makes for missing conn entries).
    public mutating func apply(_ events: [ConnectionEvent], reset: Bool) {
        if reset {
            records = [:]
        }
        for event in events {
            switch event.kind {
            case .new:
                guard var record = event.record else { continue }
                record.closed = false
                record.closedAt = 0
                records[record.id] = record
            case .update:
                guard records[event.id] != nil else { continue }
                if var record = event.record {
                    record.uplinkTotal += event.uplinkDelta
                    record.downlinkTotal += event.downlinkDelta
                    if event.closedAt > 0 { record.closedAt = event.closedAt }
                    records[record.id] = record
                } else {
                    records[event.id]?.uplinkTotal += event.uplinkDelta
                    records[event.id]?.downlinkTotal += event.downlinkDelta
                    if event.closedAt > 0 { records[event.id]?.closedAt = event.closedAt }
                }
            case .closed:
                guard records[event.id] != nil || event.record != nil else { continue }
                if var record = event.record ?? records[event.id] {
                    record.uplinkTotal += event.uplinkDelta
                    record.downlinkTotal += event.downlinkDelta
                    record.closed = true
                    record.closedAt = event.closedAt > 0 ? event.closedAt : record.closedAt
                    records[record.id] = record
                }
            }
        }
        let closed = records.values.filter(\.closed)
        if closed.count > closedCapacity {
            let expired = closed.sorted {
                if $0.closedAt != $1.closedAt { return $0.closedAt > $1.closedAt }
                return $0.id > $1.id
            }.dropFirst(closedCapacity)
            for record in expired { records.removeValue(forKey: record.id) }
        }
    }

    /// Local removal after a successful close-connection action (the TUI's
    /// [ Close connection ] clears the row from its map too).
    public mutating func remove(id: String) {
        records.removeValue(forKey: id)
    }

    /// Open connections first (newest first), then closed ones by close
    /// time — the Data page's "recent connections" ordering.
    public func sortedByRecent() -> [ConnectionRecord] {
        records.values.sorted { lhs, rhs in
            switch (lhs.closed, rhs.closed) {
            case (false, true): return true
            case (true, false): return false
            case (false, false): return lhs.createdAt > rhs.createdAt
            case (true, true): return lhs.closedAt > rhs.closedAt
            }
        }
    }
}

/// Capped log tail for the Data page (the TUI keeps the same shape: a
/// bounded slice of the newest lines, oldest first).
public struct LogBuffer: Equatable, Sendable {
    public let capacity: Int
    public let byteCapacity: Int
    public let lineByteCapacity: Int
    private var lines: [String] = []
    private var bytes = 0

    public init(capacity: Int = 300, byteCapacity: Int = 256 << 10, lineByteCapacity: Int = 4 << 10) {
        self.capacity = max(1, capacity)
        self.byteCapacity = max(1, byteCapacity)
        self.lineByteCapacity = max(1, min(lineByteCapacity, byteCapacity))
    }

    public var allLines: [String] { lines }

    public mutating func append(contentsOf newLines: [String]) {
        guard !newLines.isEmpty else { return }
        for line in newLines {
            var end = line.utf8.index(line.utf8.startIndex, offsetBy: lineByteCapacity, limitedBy: line.utf8.endIndex) ?? line.utf8.endIndex
            // Cut at a Unicode scalar boundary, without a replacement character.
            while String.Index(end, within: line) == nil { end = line.utf8.index(before: end) }
            let value = String(line[..<String.Index(end, within: line)!])
            lines.append(value)
            bytes += value.utf8.count
            while lines.count > capacity || bytes > byteCapacity {
                bytes -= lines.removeFirst().utf8.count
            }
        }
    }

    /// The newest `n` lines, oldest first (rendering order).
    public func recent(_ n: Int) -> [String] {
        let count = max(0, n)
        guard lines.count > count else { return lines }
        return Array(lines.suffix(count))
    }
}

/// Byte formatting with the exact TUI scale (internal/tui fmtB): >= 1 MiB →
/// M, >= 1 KiB → K, else B; one decimal for K/M. Kept byte-identical so the
/// iOS Data page and the macOS TUI render the same numbers.
public func formatBytes(_ value: Int64) -> String {
    if value >= 1 << 20 {
        return String(format: "%.1fM", Double(value) / Double(1 << 20))
    }
    if value >= 1 << 10 {
        return String(format: "%.1fK", Double(value) / Double(1 << 10))
    }
    return "\(value)B"
}
