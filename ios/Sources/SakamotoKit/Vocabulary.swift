import Foundation

// State vocabulary shared with pkg/mobilecore.
//
// Every raw value here is locked to the Go side through
// ios/contract/vocabulary.json (generated from internal/core by
// pkg/mobilecore/contract_test.go). ContractAlignmentTests fails if this
// file and the golden drift apart, so the iOS frontend always renders the
// same words as the macOS TUI (docs/tui.md).
//
// Rule kept from pkg/mobilecore/doc.go: state *logic* lives in
// internal/core and must be consumed through the gomobile bridge
// (SakamotoCore). Swift re-implements none of it; these types are strict
// carriers of the values the bridge produces.

/// Connection lifecycle of the tunnel engine (core.ServiceState).
/// Says nothing about whether the network behind the tunnel works.
public enum ServiceState: String, Codable, CaseIterable, Sendable, Equatable {
    /// No TUN is up; also the state before the first report arrives.
    case stopped = "Stopped"
    /// Requested-but-unconfirmed start. May show a pending indicator,
    /// never Reachable.
    case starting = "Starting"
    /// The TUN interface is up. NOT network reachability; the probe decides.
    case running = "Running"
    /// Requested-but-unconfirmed stop. The tunnel is treated as up until
    /// the stop is confirmed.
    case stopping = "Stopping"
    /// The tunnel provider handle could not be reached at all. No other
    /// state can be trusted while this holds.
    case unavailable = "Unavailable"

    /// Mirrors the documented mobilecore normalization: inputs are case- and
    /// surrounding-space-insensitive; an unknown service report counts as
    /// "Stopped" so a status label always renders.
    public init(flexible raw: String) {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        self = ServiceState.allCases.first {
            $0.rawValue.caseInsensitiveCompare(trimmed) == .orderedSame
        } ?? .stopped
    }

    /// Whether the TUN should be treated as up (core.ServiceState.Running).
    /// Starting/Stopping are in-flight guesses, never facts.
    public var running: Bool { self == .running }
}

/// The single status-bar phase (core.SessionPhase). Derived on the Go side
/// via mobilecore.SessionPhase(serviceState, probeState, conflict); iOS
/// renders the value, it never folds the inputs itself.
public enum SessionPhase: String, Codable, CaseIterable, Sendable, Equatable {
    case disconnected = "Disconnected"
    case starting = "Starting"
    case tunRunning = "TUNRunning"
    case reachable = "Reachable"
    /// Running but the last probe failed once; retry state, never a verdict.
    case unverified = "Unverified"
    /// Another VPN TUN is active while ours runs.
    case conflict = "Conflict"
    /// The controller cannot be reached; nothing else is known.
    case unavailable = "Unavailable"
    /// A stop is in flight.
    case stopping = "Stopping"

    /// Strict lookup with forward compatibility: returns nil for values
    /// from a newer core instead of guessing (the phase is a rendered
    /// fact; an unknown one must be surfaced, not normalized).
    public init?(raw: String) {
        self.init(rawValue: raw.trimmingCharacters(in: .whitespacesAndNewlines))
    }
}

/// Outcome of the HTTPS probe (core.ProbeState). One failed probe is
/// Unverified — a retry state, never "network down".
public enum ProbeState: String, Codable, CaseIterable, Sendable, Equatable {
    /// No probe result exists; adapters reset here on disconnect.
    case idle = "Idle"
    /// A probe is in flight.
    case checking = "Checking"
    /// The displayed path was verified; the path detail travels alongside.
    case reachable = "Reachable"
    /// The probe did not pass this round; the tunnel is still running.
    case unverified = "Unverified"

    /// Mirrors the documented mobilecore normalization (case- and
    /// surrounding-space-insensitive; unknown -> "Idle").
    public init(flexible raw: String) {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        self = ProbeState.allCases.first {
            $0.rawValue.caseInsensitiveCompare(trimmed) == .orderedSame
        } ?? .idle
    }
}

/// Verified path descriptions (core.Path* constants). Product wording the
/// front ends render verbatim after "Network reachable".
public enum ProbePaths {
    public static let system = "system routing"
    public static let proxy = "browser proxy"
    public static let both = "system routing and browser proxy"
    public static let systemOnly = "system routing (browser proxy unverified)"
    public static let proxyOnly = "browser proxy (system routing unverified)"

    /// All path strings, in contract order (vocabulary.json "probePaths").
    public static let all: [String] = [system, proxy, both, systemOnly, proxyOnly]
}

/// sing-box clash routing mode (core.RoutingMode). The Mode button cycles
/// Rule -> Global -> Direct exactly like the macOS TUI's `m` key.
public enum RoutingMode: String, Codable, CaseIterable, Sendable, Equatable {
    case rule = "Rule"
    case global = "Global"
    case direct = "Direct"

    /// Cycle order as data (vocabulary.json "routingModeCycle"), mirroring
    /// core.NextRoutingMode. The actual cycling is driven by the gomobile
    /// bridge (mobilecore.NextRoutingMode); this constant exists only for
    /// display affordances (e.g. previewing the next mode) and for the
    /// contract test that keeps the two sides honest.
    public static let cycleOrder: [RoutingMode] = [.rule, .global, .direct]

    /// The lowercase clash string the running core reports and accepts
    /// (core/mode.go: the native API uses lowercase clash mode strings).
    /// Transport encoding only; parsing stays on the Go side.
    public var clashModeValue: String { rawValue.lowercased() }
}

/// Latency-test vocabulary for a single node (core.NodeStatus).
/// Selected (the filled dot) is a separate fact and never a connectivity
/// claim (skills.md invariant 9).
public enum NodeStatus: String, Codable, CaseIterable, Sendable, Equatable {
    /// No URL test result exists yet.
    case untested = "Untested"
    /// A URL test is in flight ("Testing…").
    case testing = "Testing"
    /// The last URL test measured a positive latency — for this node only.
    case reachable = "Reachable"
    /// The last URL test failed or timed out. A selected node may be Failed.
    case failed = "Failed"
}

/// Configuration-change state machine (core.ConfigState). Transitions run
/// through the bridge (mobilecore.ConfigStateTransition) and are loud on
/// unknown input; Swift only carries the result.
public enum ConfigState: String, Codable, CaseIterable, Sendable, Equatable {
    /// The generated config is what the core loaded; nothing pending.
    case clean = "Clean"
    /// Saved changes exist that Regenerate has not folded in yet.
    case needsRegenerate = "NeedsRegenerate"
    /// The generated config is newer than what the core loaded; a
    /// disconnect + reconnect applies it.
    case needsReconnect = "NeedsReconnect"
}

/// Transient status line (core.Notice). Notices replace each other; they
/// are not a log and never contain credentials or node addresses.
public enum NoticeKind: String, Codable, CaseIterable, Sendable, Equatable {
    case info = "Info"
    case progress = "Progress"
    case success = "Success"
    /// Degraded-but-recovering; warnings never claim a hard failure.
    case warning = "Warning"
    /// A failed action; the text carries the cause.
    case error = "Error"
}

/// One transient user-facing message (core.Notice).
public struct Notice: Codable, Equatable, Sendable {
    public var kind: NoticeKind
    public var text: String

    public init(kind: NoticeKind, text: String) {
        self.kind = kind
        self.text = text
    }
}
