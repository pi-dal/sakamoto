import Foundation

// Staging models for the iOS Config tab (Import config / Policy / Nodes &
// sources). Foundation-only and pure: every mutation returns a new value so
// the app layer can persist the result and tests need no UserDefaults.
//
// Boundary this file encodes (mirrors pkg/mobilecore/importer.go):
//   * Policy rules and node/subscription metadata are STAGED on device.
//     Folding them into generated rules is host-owned (internal/gen during
//     Regenerate); nothing here claims a device-side regeneration.
//   * The last-known-good import is only ever replaced through commit(),
//     which the app calls after the Go bridge validated the new content. A
//     failed fetch or parse can therefore never clobber it.
//   * Secrets (raw share links, subscription URLs, the Tailscale auth key)
//     live in storage only, render masked by default, and never appear in
//     log-style action strings — see SecretMasking.

// MARK: - Masking

public enum SecretMasking {
    /// Mask a secret URL/link for list rows: keep the scheme (so the type is
    /// obvious), hide everything else. The TUI equivalent is Ctrl+R reveal.
    public static func maskSecret(_ value: String) -> String {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let schemeEnd = trimmed.range(of: "://") else {
            return trimmed.isEmpty ? "" : "•••"
        }
        return String(trimmed[..<schemeEnd.lowerBound]) + "://•••"
    }

    /// Mask a conf source for display: scheme + host for remote URLs,
    /// verbatim local paths (they carry no credentials and iOS shows its own
    /// document names), query/fragment/userinfo always dropped.
    public static func maskSource(_ source: String) -> String {
        let trimmed = source.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: trimmed), let scheme = url.scheme?.lowercased(),
              scheme == "http" || scheme == "https", let host = url.host, !host.isEmpty
        else {
            return trimmed
        }
        return "\(scheme)://\(host)/…"
    }
}

// MARK: - Policy

/// One user-owned routing override, identical in shape and validation to the
/// TUI's Config → Policy (docs/tui.md): match is host / *.suffix /
/// keyword: / cidr: / http(s) URL; action is proxy | direct | reject.
public struct StagedPolicyRule: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var match: String
    public var action: String

    public init(id: String = UUID().uuidString, match: String, action: String) {
        self.id = id
        self.match = match
        self.action = action
    }
}

/// Ordered policy list with Codable persistence.
public struct PolicyBook: Codable, Equatable, Sendable {
    public var rules: [StagedPolicyRule] = []

    public init(rules: [StagedPolicyRule] = []) { self.rules = rules }

    public func index(of id: String) -> Int? { rules.firstIndex { $0.id == id } }

    /// Insert validated rule at the end (order matters: direct rules are
    /// compiled into the direct set, which has priority over proxy).
    public func adding(_ rule: StagedPolicyRule) -> PolicyBook {
        var next = self
        next.rules.append(rule)
        return next
    }

    public func updating(id: String, to rule: StagedPolicyRule) -> PolicyBook {
        var next = self
        if let index = index(of: id) { next.rules[index] = rule }
        return next
    }

    public func removing(id: String) -> PolicyBook {
        var next = self
        next.rules.removeAll { $0.id == id }
        return next
    }

    public func encoded() throws -> Data { try JSONEncoder().encode(self) }

    public static func decode(_ data: Data) -> PolicyBook? {
        try? JSONDecoder().decode(PolicyBook.self, from: data)
    }
}

// MARK: - Nodes & sources

/// A manual node staged on device. `rawLink` is the secret one-line share
/// link — stored, never rendered unmasked by default, never logged.
public struct StagedNode: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var rawLink: String
    public var tag: String
    public var type: String
    public var server: String
    public var addedAt: Date

    public init(id: String = UUID().uuidString, rawLink: String, tag: String, type: String, server: String, addedAt: Date = Date()) {
        self.id = id
        self.rawLink = rawLink
        self.tag = tag
        self.type = type
        self.server = server
        self.addedAt = addedAt
    }
}

/// Subscription METADATA only. Fetching/decoding subscriptions is host-owned
/// (internal/gen fetchSub during Regenerate); on device this stays an
/// explicitly pending source — never a generated node.
public struct StagedSubscription: Codable, Equatable, Identifiable, Sendable {
    public static let formats = ["auto", "singbox", "clash", "base64"]

    public var id: String
    public var name: String
    public var url: String
    public var format: String

    public init(id: String = UUID().uuidString, name: String, url: String, format: String = "auto") {
        self.id = id
        self.name = name
        self.url = url
        self.format = format
    }
}

public struct NodesSourcesBook: Codable, Equatable, Sendable {
    public var nodes: [StagedNode] = []
    public var subscriptions: [StagedSubscription] = []

    public init(nodes: [StagedNode] = [], subscriptions: [StagedSubscription] = []) {
        self.nodes = nodes
        self.subscriptions = subscriptions
    }

    public func adding(node: StagedNode) -> NodesSourcesBook {
        var next = self
        next.nodes.append(node)
        return next
    }

    public func adding(subscription: StagedSubscription) -> NodesSourcesBook {
        var next = self
        next.subscriptions.append(subscription)
        return next
    }

    public func removingNode(id: String) -> NodesSourcesBook {
        var next = self
        next.nodes.removeAll { $0.id == id }
        return next
    }

    public func removingSubscription(id: String) -> NodesSourcesBook {
        var next = self
        next.subscriptions.removeAll { $0.id == id }
        return next
    }

    /// nodes.txt-compatible export (one share link per line, in order) so a
    /// future host-sync can consume the staging file unchanged.
    public var nodesText: String {
        nodes.map(\.rawLink).joined(separator: "\n") + (nodes.isEmpty ? "" : "\n")
    }

    public func encoded() throws -> Data { try JSONEncoder().encode(self) }

    public static func decode(_ data: Data) -> NodesSourcesBook? {
        try? JSONDecoder().decode(NodesSourcesBook.self, from: data)
    }
}

// MARK: - Import staging

/// The last-known-good Shadowrocket import. `content` is device-local state;
/// the host generator consumes the corresponding source during Regenerate.
public struct ImportedSource: Codable, Equatable, Sendable {
    /// Masked display form (SecretMasking.maskSource) — the only form that
    /// may reach UI strings.
    public var displaySource: String
    public var content: String
    public var importedAt: Date

    public init(displaySource: String, content: String, importedAt: Date = Date()) {
        self.displaySource = displaySource
        self.content = content
        self.importedAt = importedAt
    }

    public func encoded() throws -> Data { try JSONEncoder().encode(self) }

    public static func decode(_ data: Data) -> ImportedSource? {
        try? JSONDecoder().decode(ImportedSource.self, from: data)
    }
}
