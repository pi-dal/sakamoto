import Foundation

/// Routing predicates are public display content. Only source URL credentials
/// and token-bearing paths are masked; never send a whole rule to maskSecret.
public enum ConfRulePresentation {
    private static let remoteURL = try! NSRegularExpression(pattern: #"https?://[^\s,]+"#, options: [.caseInsensitive])
    private static let inlineComment = try! NSRegularExpression(pattern: #"\s//.*$"#)

    public static func rows(in body: String) -> [String] {
        var section = ""
        return body.replacingOccurrences(of: "\u{FEFF}", with: "").components(separatedBy: .newlines).compactMap { raw in
            let line = raw.trimmingCharacters(in: .whitespacesAndNewlines)
            let predicate = inlineComment.stringByReplacingMatches(in: line, range: NSRange(line.startIndex..., in: line), withTemplate: "").trimmingCharacters(in: .whitespaces)
            if predicate.hasPrefix("["), predicate.hasSuffix("]") { section = predicate.lowercased(); return nil }
            guard !predicate.isEmpty, !predicate.hasPrefix("#"),
                  section == "[rule]" || (section == "[general]" && line.lowercased().hasPrefix("include")) else { return nil }
            return displayLine(line)
        }
    }

    public static func displayLine(_ line: String) -> String {
        var result = line
        for match in remoteURL.matches(in: line, range: NSRange(line.startIndex..., in: line)).reversed() {
            guard let range = Range(match.range, in: result) else { continue }
            let source = String(result[range])
            let masked = SecretMasking.maskSource(source)
            // Invalid URL references must not fall back to displaying credentials.
            result.replaceSubrange(range, with: masked == source ? "https://…" : masked)
        }
        return result
    }
}
