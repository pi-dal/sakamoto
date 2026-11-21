import Foundation
import NetworkExtension
import SakamotoKit

public enum AutomaticConnectionPolicy {
    public static func rules(for settings: AutomaticConnectionSettings) -> [NEOnDemandRule] {
        switch settings.mode {
        case .off: return []
        case .anyNetwork: return [NEOnDemandRuleConnect()]
        case .wifi, .cellular:
            let rule = NEOnDemandRuleConnect()
            #if os(iOS)
            rule.interfaceTypeMatch = settings.mode == .wifi ? .wiFi : .cellular
            #else
            rule.interfaceTypeMatch = .wiFi
            #endif
            return [rule, NEOnDemandRuleIgnore()]
        case .domains:
            let connection = NEEvaluateConnectionRule(matchDomains: settings.domains, andAction: .connectIfNeeded)
            if !settings.probeURL.isEmpty { connection.probeURL = URL(string: settings.probeURL) }
            let rule = NEOnDemandRuleEvaluateConnection()
            rule.connectionRules = [connection]
            return [rule, NEOnDemandRuleIgnore()]
        }
    }

    public static func settings(from manager: NEVPNManager) -> AutomaticConnectionSettings {
        guard let first = manager.onDemandRules?.first else { return .init() }
        if let rule = first as? NEOnDemandRuleEvaluateConnection, let connection = rule.connectionRules?.first {
            let mode: AutomaticConnectionSettings.Mode = manager.isOnDemandEnabled ? .domains : .off
            return .init(mode: mode, domains: connection.matchDomains, probeURL: connection.probeURL?.absoluteString ?? "")
        }
        guard manager.isOnDemandEnabled else { return .init() }
        if first is NEOnDemandRuleConnect {
            #if os(iOS)
            if first.interfaceTypeMatch == .cellular { return .init(mode: .cellular) }
            #endif
            return .init(mode: first.interfaceTypeMatch == .wiFi ? .wifi : .anyNetwork)
        }
        return .init()
    }

    @MainActor
    static func updatePreferences(_ manager: NEVPNManager, change: () throws -> Void, finish: () throws -> Void = {}) async throws {
        let enabled = manager.isEnabled
        let onDemand = manager.isOnDemandEnabled
        let rules = manager.onDemandRules
        var saved = false
        do {
            try change()
            try Task.checkCancellation()
            try await manager.saveToPreferences()
            saved = true
            try await manager.loadFromPreferences()
            try Task.checkCancellation()
            try finish()
        } catch {
            manager.onDemandRules = rules
            manager.isOnDemandEnabled = onDemand
            manager.isEnabled = enabled
            if saved {
                try await manager.saveToPreferences()
                try await manager.loadFromPreferences()
            }
            throw error
        }
    }

    @MainActor
    static func pause(_ manager: NEVPNManager, then finish: () throws -> Void = {}) async throws {
        try Task.checkCancellation()
        guard manager.isOnDemandEnabled else { try finish(); return }
        try await updatePreferences(manager, change: { manager.isOnDemandEnabled = false }, finish: finish)
    }

    public static func apply(_ settings: AutomaticConnectionSettings, to manager: NEVPNManager) throws {
        let validated = try settings.validated()
        manager.onDemandRules = rules(for: validated)
        manager.isOnDemandEnabled = validated.mode != .off
    }
}
