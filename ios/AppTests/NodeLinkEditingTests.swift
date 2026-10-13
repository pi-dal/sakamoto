import XCTest
import SakamotoKit

final class NodeLinkEditingTests: XCTestCase {
    func testEndpointFieldsPreserveEncodedCredentialsTLSAndTransportAndAcceptIPv6() throws {
        let raw = "vless://uuid%2Bsecret@old.example:443?security=reality&pbk=key%2Bvalue&type=ws&path=%2Fa%3Fb%3D1&sni=cdn.example#Old"
        let saved = try NodeLinkEditing.updating(raw, remark: "Japan #1 / \u{65e5}\u{672c}", server: "2001:db8::8", port: 8443)
        let endpoint = try NodeLinkEditing.endpoint(saved)
        XCTAssertEqual(endpoint.remark, "Japan #1 / \u{65e5}\u{672c}")
        XCTAssertEqual(endpoint.server, "2001:db8::8")
        XCTAssertEqual(endpoint.port, 8443)
        let oldURL = try XCTUnwrap(URLComponents(string: raw)), url = try XCTUnwrap(URLComponents(string: saved))
        XCTAssertEqual(url.percentEncodedUser, oldURL.percentEncodedUser)
        XCTAssertEqual(url.percentEncodedQuery, oldURL.percentEncodedQuery)
    }
    func testShadowrocketAuthorityAndPortRangesKeepTheirOptions() throws {
        let authority = Data("user:secret@old.example:1080".utf8).base64EncodedString()
        let raw = "socks://\(authority)?remarks=Old&chain=PROXY"
        let endpoint = try NodeLinkEditing.endpoint(raw)
        XCTAssertEqual(endpoint.server, "old.example")
        let saved = try NodeLinkEditing.updating(raw, remark: "New", server: "192.0.2.1", port: 1081)
        XCTAssertEqual(try NodeLinkEditing.endpoint(saved), .init(remark: "New", server: "192.0.2.1", port: 1081))
        XCTAssertTrue(saved.contains("chain=PROXY"))
        let ranged = "hy2://secret@old.example?mport=443-445&sni=cdn.example#Old"
        XCTAssertNil(try NodeLinkEditing.endpoint(ranged).port)
        let next = try NodeLinkEditing.updating(ranged, remark: "New", server: "192.0.2.2", port: nil)
        XCTAssertTrue(next.contains("mport=443-445"))
    }
    func testVMessJSONRetainsUnknownProtocolOptions() throws {
        let json = #"{"add":"old.example","port":"443","ps":"Old","id":"uuid","net":"ws","path":"/keep","future":{"keep":true}}"#
        let raw = "vmess://" + Data(json.utf8).base64EncodedString()
        let saved = try NodeLinkEditing.updating(raw, remark: "New", server: "192.0.2.3", port: 8443)
        XCTAssertEqual(try NodeLinkEditing.endpoint(saved), .init(remark: "New", server: "192.0.2.3", port: 8443))
        let root = try XCTUnwrap(try JSONSerialization.jsonObject(with: XCTUnwrap(Data(base64Encoded: String(saved.dropFirst(8))))) as? [String: Any])
        XCTAssertEqual(root["path"] as? String, "/keep")
        XCTAssertEqual((root["future"] as? [String: Bool])?["keep"], true)
        XCTAssertThrowsError(try NodeLinkEditing.updating(raw, remark: "", server: "x", port: 443))
        XCTAssertThrowsError(try NodeLinkEditing.updating(raw, remark: "X", server: "https://x", port: 443))
        XCTAssertThrowsError(try NodeLinkEditing.updating(raw, remark: "X", server: "x", port: 65536))
    }
}
