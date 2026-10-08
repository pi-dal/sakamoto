import XCTest
@testable import SakamotoKit

final class NodeFileImportTests: XCTestCase {
    private func validate(_ raw: String) throws -> StagedNode {
        guard raw.hasPrefix("socks5://") else { throw NodeFileImport.Failure("secret: \(raw)") }
        return StagedNode(rawLink: raw, tag: "node", type: "socks", server: "example.com")
    }

    func testBOMCommentsCRLFAndDuplicates() throws {
        let nodes = try NodeFileImport.parse(Data("\u{FEFF}# exported\r\nsocks5://example.com:1080\r\n\r\nsocks5://example.com:1080\n".utf8), validate: validate)
        XCTAssertEqual(nodes.count, 1)
    }

    func testInvalidLineFailsWholeImportWithoutLeakingCredentials() {
        XCTAssertThrowsError(try NodeFileImport.parse(Data("socks5://example.com:1080\ninvalid-secret\n".utf8), validate: validate)) { error in
            XCTAssertTrue(error.localizedDescription.contains("line 2"))
            XCTAssertFalse(error.localizedDescription.contains("invalid-secret"))
        }
    }

    func testMergePreservesExistingNodesAndSubscriptions() throws {
        let raw = "socks5://example.com:1080"
        let old = try validate(raw)
        let source = StagedSubscription(name: "feed", url: "https://example.com")
        let book = NodesSourcesBook(nodes: [old], subscriptions: [source])
        let nodes = try NodeFileImport.parse(Data("\(raw)\nsocks5://other.example:1080".utf8), validate: validate)
        let merged = NodeFileImport.merging(nodes, into: book)
        XCTAssertEqual(merged.nodes.count, 2)
        XCTAssertEqual(merged.nodes.first?.id, old.id)
        XCTAssertEqual(merged.subscriptions, [source])
    }

    func testEmptyAndNonUTF8FilesAreRejected() {
        XCTAssertThrowsError(try NodeFileImport.parse(Data("# only comment".utf8), validate: validate))
        XCTAssertThrowsError(try NodeFileImport.parse(Data([0xff]), validate: validate))
    }
}
