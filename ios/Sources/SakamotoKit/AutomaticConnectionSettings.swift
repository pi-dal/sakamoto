import Foundation

public struct AutomaticConnectionSettings: Codable, Equatable, Sendable {
    public enum Mode: String, Codable, CaseIterable, Sendable {
        case off, anyNetwork, wifi, cellular, domains
    }
    public var mode: Mode
    public var domains: [String]
    public var probeURL: String

    public init(mode: Mode = .off, domains: [String] = [], probeURL: String = "") {
        self.mode = mode
        self.domains = domains
        self.probeURL = probeURL
    }

    // App budgets, not an Apple-documented maximum. Keep NE preferences and
    // editable text small even when routing rule sets contain millions of hosts.
    public static let maxDomains = 128
    public static let maxInputBytes = 32 << 10

    /// URLs are accepted as a convenience; rules match hosts, never URL paths.
    public static func normalizeDomains(_ input: String) throws -> [String] {
        guard input.utf8.count <= maxInputBytes else { throw ValidationError.inputTooLarge }
        var result: [String] = []
        var seen = Set<String>()
        for token in input.components(separatedBy: .whitespacesAndNewlines.union(CharacterSet(charactersIn: ","))) where !token.isEmpty {
            let candidate = token.contains("://") ? token : "https://" + token
            guard let url = URLComponents(string: candidate),
                  ["http", "https"].contains(url.scheme?.lowercased() ?? ""),
                  url.user == nil, url.password == nil,
                  let host = url.host?.lowercased().trimmingCharacters(in: CharacterSet(charactersIn: ".")),
                  !host.isEmpty, host.count <= 253,
                  host.split(separator: ".", omittingEmptySubsequences: false).allSatisfy({ label in
                      !label.isEmpty && label.count <= 63 && label.first != "-" && label.last != "-" &&
                      label.utf8.allSatisfy { (97...122).contains($0) || (48...57).contains($0) || $0 == 45 }
                  }) else { throw ValidationError.invalidDomain }
            if seen.insert(host).inserted {
                guard result.count < maxDomains else { throw ValidationError.tooManyDomains }
                result.append(host)
            }
        }
        return result
    }

    public func validated() throws -> Self {
        var result = self
        guard domains.count <= Self.maxDomains else { throw ValidationError.tooManyDomains }
        guard domains.reduce(0, { $0 + $1.utf8.count + 1 }) <= Self.maxInputBytes else { throw ValidationError.inputTooLarge }
        result.domains = try Self.normalizeDomains(domains.joined(separator: "\n"))
        if mode == .domains && result.domains.isEmpty { throw ValidationError.emptyDomains }
        result.probeURL = probeURL.trimmingCharacters(in: .whitespacesAndNewlines)
        if !result.probeURL.isEmpty {
            guard let url = URLComponents(string: result.probeURL),
                  ["http", "https"].contains(url.scheme?.lowercased() ?? ""),
                  url.host?.isEmpty == false, url.user == nil, url.password == nil,
                  url.fragment == nil else { throw ValidationError.invalidProbe }
        }
        return result
    }

    public enum ValidationError: LocalizedError {
        case invalidDomain, emptyDomains, invalidProbe, tooManyDomains, inputTooLarge
        public var errorDescription: String? {
            switch self {
            case .tooManyDomains: return "Choose up to \(AutomaticConnectionSettings.maxDomains) trigger domains. The complete proxy rules still apply once the VPN connects."
            case .inputTooLarge: return "The on-demand list is too large. Choose a few trigger domains instead of pasting the full rule configuration."
            case .invalidDomain: return "Enter a domain or an HTTP/HTTPS URL without credentials."
            case .emptyDomains: return "Add at least one domain for on-demand connection."
            case .invalidProbe: return "Enter an HTTP/HTTPS check URL without credentials or a fragment."
            }
        }
    }
}
