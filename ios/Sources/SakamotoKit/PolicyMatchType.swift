import Foundation

/// Turns a visible policy match choice into the existing host policy syntax.
public enum PolicyMatchType: String, CaseIterable, Sendable {
    case automatic, domain, suffix, keyword, cidr

    public var title: String {
        switch self {
        case .automatic: return "From input / prefixes"
        case .domain: return "Exact domain"
        case .suffix: return "Domain & all subdomains"
        case .keyword: return "Domain contains"
        case .cidr: return "IP address / CIDR"
        }
    }

    public var help: String {
        switch self {
        case .automatic: return "A hostname matches exactly. Use *.aihubmix.com or suffix:aihubmix.com to include all subdomains. keyword: and cidr: are also supported."
        case .domain: return "aihubmix.com matches only that domain. Subdomains such as api.aihubmix.com are excluded."
        case .suffix: return "aihubmix.com matches the domain itself and every subdomain, including api.aihubmix.com and chat.api.aihubmix.com."
        case .keyword: return "aihubmix matches any domain containing that text."
        case .cidr: return "Enter an IP address or a range such as 192.0.2.0/24."
        }
    }

    public func inputForValidation(_ raw: String) -> String {
        let text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        return self == .keyword && !text.lowercased().hasPrefix("keyword:") ? "keyword:" + text : text
    }

    public func match(raw: String, normalizedKind: String, value: String) throws -> String {
        switch self {
        case .automatic: return raw.trimmingCharacters(in: .whitespacesAndNewlines)
        case .domain, .suffix:
            guard ["domain", "domain_suffix"].contains(normalizedKind), !value.isEmpty,
                  !value.contains(where: { $0.isWhitespace }), !value.contains("/"), !value.contains("*"), !value.contains(":") else {
                throw TunnelProfile.InvalidProfile("Enter a domain or HTTP(S) URL, or choose a different match type.")
            }
            return (self == .suffix ? "suffix:" : "domain:") + value
        case .keyword: return "keyword:" + value
        case .cidr:
            guard normalizedKind == "ip_cidr" else { throw TunnelProfile.InvalidProfile("Enter an IP address or CIDR range.") }
            return "cidr:" + value
        }
    }
}
