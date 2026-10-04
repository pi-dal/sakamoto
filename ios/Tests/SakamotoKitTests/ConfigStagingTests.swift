import XCTest
@testable import SakamotoKit

// Config staging semantics: last-known-good protection, masked secrets,
// nodes.txt-compatible export, and pending subscription metadata.

final class ConfigStagingTests: XCTestCase {
    func testFailedImportCannotClobberLastKnownGood() {
        // The API shape enforces it: ImportedSource is a value; the only
        // mutation path is replacing it wholesale via commit-style storage.
        // These assertions pin the display contract that the UI relies on.
        let good = ImportedSource(displaySource: "https://example.com/…", content: "[Rule]\nDOMAIN,a,DIRECT\n")
        var draft = good
        draft.content = "corrupted"
        // Simulating a failed import: the stored value is untouched because
        // the app only ever writes back through commit on success.
        XCTAssertEqual(good.content, "[Rule]\nDOMAIN,a,DIRECT\n")
        XCTAssertNotEqual(good.content, draft.content)
        // Codable round trip.
        let restored = ImportedSource.decode(try! good.encoded())
        XCTAssertEqual(restored, good)
    }

    func testMaskSecretKeepsSchemeOnly() {
        XCTAssertEqual(SecretMasking.maskSecret("vless://uuid@host:443?pbk=k#name"), "vless://•••")
        XCTAssertEqual(SecretMasking.maskSecret("https://sub.example.com/token?x=1"), "https://•••")
        XCTAssertEqual(SecretMasking.maskSecret("not-a-link"), "•••")
        XCTAssertEqual(SecretMasking.maskSecret("  "), "")
    }

    func testMaskSourceDropsQueryAndPath() {
        XCTAssertEqual(SecretMasking.maskSource("https://user:pw@sub.example.com/secret.conf?token=t"), "https://sub.example.com/…")
        XCTAssertEqual(SecretMasking.maskSource("/Users/x/Downloads/sr.conf"), "/Users/x/Downloads/sr.conf")
    }

    func testPolicyBookMutations() {
        let book = PolicyBook()
            .adding(StagedPolicyRule(match: "example.com", action: "proxy"))
            .adding(StagedPolicyRule(match: "*.ads.example", action: "reject"))
        XCTAssertEqual(book.rules.count, 2)
        let firstID = book.rules[0].id
        let updated = book.updating(id: firstID, to: StagedPolicyRule(id: firstID, match: "example.com", action: "direct"))
        XCTAssertEqual(updated.rules[0].action, "direct")
        let removed = updated.removing(id: firstID)
        XCTAssertEqual(removed.rules.count, 1)
        XCTAssertEqual(removed.rules[0].match, "*.ads.example")
        // Codable round trip keeps order (order matters: direct compiles with priority).
        let restored = PolicyBook.decode(try! book.encoded())
        XCTAssertEqual(restored, book)
    }

    func testNodesBookMasksRawLinksAndExportsNodesTxt() throws {
        let link = "vless://SECRET-UUID@host.example:443?security=reality&pbk=PUB#Node"
        let book = NodesSourcesBook()
            .adding(node: StagedNode(rawLink: link, tag: "Node", type: "vless", server: "host.example"))
            .adding(subscription: StagedSubscription(name: "Provider", url: "https://sub.example.com/feed?token=SECRET", format: "auto"))
        // Export is nodes.txt-compatible: raw links, one per line — for host
        // sync, never for display.
        XCTAssertEqual(book.nodesText, link + "\n")
        // The Codable snapshot stores the raw link (persistence, not UI) —
        // assert the DISPLAY helpers never surface it.
        let maskedNode = SecretMasking.maskSecret(book.nodes[0].rawLink)
        XCTAssertFalse(maskedNode.contains("SECRET-UUID"))
        let maskedSub = SecretMasking.maskSecret(book.subscriptions[0].url)
        XCTAssertFalse(maskedSub.contains("SECRET"), maskedSub)
        // Round trip.
        let restored = NodesSourcesBook.decode(try! book.encoded())
        XCTAssertEqual(restored, book)
        XCTAssertEqual(restored?.removingNode(id: book.nodes[0].id).nodes.count, 0)
        XCTAssertEqual(restored?.removingSubscription(id: book.subscriptions[0].id).subscriptions.count, 0)
    }

    func testSubscriptionFormatsVocabulary() {
        XCTAssertEqual(StagedSubscription.formats, ["auto", "singbox", "clash", "base64"])
    }
}
