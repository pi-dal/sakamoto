import XCTest
@testable import SakamotoKit

final class ICloudRealFilesystemTests: XCTestCase {
    private struct Container: ICloudContainer {
        func containerURL() -> URL? { nil }
    }

    func testMissingConfDirectoryCanBeCreatedBySync() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        let cloud = root.appendingPathComponent("cloud")
        let local = root.appendingPathComponent("local")
        try FileManager.default.createDirectory(at: cloud, withIntermediateDirectories: true)
        var settings = ICloudSyncSettings()
        settings.enabled = true
        let store = ICloudSyncStore(settings: settings, localFilesystem: LocalFilesystem(), cloudFilesystem: CloudFilesystem(), container: Container(), localDirectory: local.path)
        let conf = Data("[General]\n[Rule]\nFINAL,DIRECT\n".utf8)
        let result = await store.syncNow(staging: [.init(name: "conf/main.conf", data: conf)], directoryURL: cloud)
        guard case .synced = result.status else { return XCTFail(result.status.summary) }
        XCTAssertEqual(try Data(contentsOf: cloud.appendingPathComponent("conf/main.conf")), conf)
        // A second pass exercises baseline reads on the real local adapter.
        let second = await store.syncNow(staging: [.init(name: "conf/main.conf", data: conf)], directoryURL: cloud)
        guard case .upToDate = second.status else { return XCTFail(second.status.summary) }
    }

    func testRealAdaptersAllowMissingEntriesButDetectDanglingLinks() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        let link = root.appendingPathComponent("conf")
        let missing = root.appendingPathComponent("missing")
        for fs: any ICloudFilesystem in [LocalFilesystem(), CloudFilesystem()] {
            XCTAssertFalse(try fs.isSymbolicLink(atPath: missing.path))
        }
        try FileManager.default.createSymbolicLink(at: link, withDestinationURL: missing)
        for fs: any ICloudFilesystem in [LocalFilesystem(), CloudFilesystem()] {
            XCTAssertTrue(try fs.isSymbolicLink(atPath: link.path))
            XCTAssertThrowsError(try ICloudSyncStore.checkPath(root: root.path, path: link.appendingPathComponent("main.conf").path, filesystem: fs))
        }
    }

    func testRegularFileUsedAsParentRemainsAnError() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        let file = root.appendingPathComponent("conf")
        try Data("file".utf8).write(to: file)
        for fs: any ICloudFilesystem in [LocalFilesystem(), CloudFilesystem()] {
            XCTAssertThrowsError(try fs.isSymbolicLink(atPath: file.appendingPathComponent("main.conf").path))
        }
    }
}
