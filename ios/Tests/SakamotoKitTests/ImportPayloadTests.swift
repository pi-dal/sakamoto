import XCTest
@testable import SakamotoKit

final class ImportPayloadTests: XCTestCase {
    func testSupportedShapes() throws {
        XCTAssertEqual(try ImportPayload.detect(" vless://id@example.org:443#node ").kind, .node)
        XCTAssertEqual(try ImportPayload.detect("https://example.org/sub?token=secret").kind, .url)
        XCTAssertEqual(try ImportPayload.detect("\u{FEFF}{\"outbounds\":[]}").kind, .generatedConfig)
        XCTAssertEqual(try ImportPayload.detect("[Rule]\nFINAL,proxy").kind, .conf)
    }
    func testInvalidInput() {
        for raw in ["", "ordinary text", "file:///etc/config", "https://", "vless://one\nvless://two", String(repeating: "a", count: 1_048_577)] {
            XCTAssertThrowsError(try ImportPayload.detect(raw))
        }
    }
    func testPreservesRawLinkForParser() throws {
        let raw = "ss://secret@example.org:443#My%20node"
        XCTAssertEqual(try ImportPayload.detect(raw).text, raw)
    }
}
