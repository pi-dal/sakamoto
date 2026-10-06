import XCTest
@testable import SakamotoKit

final class S3SyncTests: XCTestCase {
    func testSharedWireFormatAndOptionalFields() throws {
        let raw = #"{"bundle":{"version":1,"files":{"policy.json":"[]"}},"baseline":{"target":"https://s3.example/b/sources-v1.json","hashes":{}},"downloads":null,"uploaded":false}"#
        let response = try JSONDecoder().decode(S3SyncResponse.self, from: Data(raw.utf8))
        XCTAssertEqual(response.bundle.mainConf, "")
        XCTAssertEqual(response.bundle.files["policy.json"], "[]")
        XCTAssertNil(response.downloads)
    }
    func testCredentialsSeparatedFromSettings() throws {
        let settings = try JSONEncoder().encode(S3SyncSettings())
        XCTAssertFalse(String(decoding: settings, as: UTF8.self).contains("secret_key"))
        let credentials = S3SyncCredentials(accessKey: "synthetic", secretKey: "example")
        let data = try JSONEncoder().encode(credentials)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: String])
        XCTAssertEqual(object["secret_key"], "example")
        XCTAssertEqual(object["access_key"], "synthetic")
    }
}
