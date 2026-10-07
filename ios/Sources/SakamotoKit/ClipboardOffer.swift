import Foundation

/// Dedupes the foreground clipboard-import offer without ever reading
/// clipboard content — only the system `changeCount` revision is tracked.
///
/// UIPasteboard.changeCount increments whenever an app writes the clipboard,
/// so it identifies "is there something new to offer" without touching the
/// payload. Dismissing an offer suppresses that revision; the next copy
/// (new changeCount) offers again. Reading the actual payload stays a
/// user-directed PasteButton action — no silent clipboard access, no
/// unsolicited paste authorization prompts.
public struct ClipboardOffer: Equatable, Sendable {
    /// Whether the pasteboard contains text (hasStrings) or a link (hasURLs).
    /// Presence metadata only — never payload content.
    public enum Content: Equatable, Sendable { case text, link }

    /// changeCount revisions whose offer was already dismissed or consumed.
    private var resolved: Set<Int> = []

    public init() {}

    /// Whether an offer row should be shown for this revision.
    public func shouldOffer(revision: Int) -> Bool {
        !resolved.contains(revision)
    }

    /// Marks this revision's offer as resolved; later probes suppress it.
    /// Paste consumption also resolves — the payload was delivered.
    public mutating func dismiss(revision: Int) {
        resolved.insert(revision)
    }
}
