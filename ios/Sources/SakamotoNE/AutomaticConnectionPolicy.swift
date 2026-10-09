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
        guard manager.isOnDemandEnabled, let first = manager.onDemandRules?.first else { return .init() }
        if let rule = first as? NEOnDemandRuleEvaluateConnection, let connection = rule.connectionRules?.first {
            return .init(mode: .domains, domains: connection.matchDomains, probeURL: connection.probeURL?.absoluteString ?? "")
        }
        if first is NEOnDemandRuleConnect {
            #if os(iOS)
            if first.interfaceTypeMatch == .cellular { return .init(mode: .cellular) }
            #endif
            return .init(mode: first.interfaceTypeMatch == .wiFi ? .wifi : .anyNetwork)
        }
        return .init()
    }

    public static func apply(_ settings: AutomaticConnectionSettings, to manager: NEVPNManager) throws {
        let validated = try settings.validated()
        manager.onDemandRules = rules(for: validated)
        manager.isOnDemandEnabled = validated.mode != .off
    }
}
