import XCTest
@testable import SakamotoKit

final class AutomaticGroupSourcesTests: XCTestCase {
    private let content = #"{"outbounds":[{"tag":"A","type":"trojan"},{"tag":"Local Japan","type":"trojan"},{"tag":"OthersAuto","type":"urltest","outbounds":["A","Local Japan"]},{"tag":"Custom","type":"selector","outbounds":["A"]}]}"#
    func testExclusionsUseSourceNamesAndClearKeepsExplicitIntent() throws {
        var edit = try XCTUnwrap(ProxyGroupEditing.groups(content).first { $0.tag == "OthersAuto" }); edit.members = ["A"]
        edit = ProxyGroupEditing.recording(edit, in: content)
        let overrides = ["B": #"{"tag":"Local Japan","type":"trojan"}"#]
        let groups = try XCTUnwrap(AutomaticGroupSources.exclusions(edits: [edit], content: content, overrides: overrides))
        XCTAssertEqual(groups, ["OthersAuto": ["B"]])
        var bundle = SourceBundle(); bundle.files["nodes.txt"] = "original bytes"
        bundle = try AutomaticGroupSources.updating(bundle, edits: [edit], content: content, overrides: overrides)
        XCTAssertEqual(bundle.files["groups.json"], #"{"OthersAuto":["B"]}"#)
        let adopted = AutomaticGroupSources.adopting(groups, edits: ProxyGroupEditing.groups(content), content: content, overrides: overrides)
        XCTAssertEqual(adopted.first { $0.tag == "OthersAuto" }?.members, ["A"])
        XCTAssertEqual(adopted.first { $0.tag == "OthersAuto" }?.excludedMembers, ["Local Japan"])
        XCTAssertEqual(adopted.first { $0.tag == "Custom" }?.members, ["A"])
        let cleared = AutomaticGroupSources.adopting([:], edits: adopted, content: content, overrides: overrides)
        XCTAssertEqual(cleared.first { $0.tag == "OthersAuto" }?.members, ["A", "Local Japan"])
        bundle = try AutomaticGroupSources.updating(bundle, edits: cleared, content: content, overrides: overrides)
        XCTAssertEqual(bundle.files["groups.json"], "{}")
        XCTAssertEqual(bundle.files["nodes.txt"], "original bytes")
    }
    func testInvalidSourcesRejectedAndUnchangedSourceBytesPreserved() throws {
        for source in ["null", "[]", #"{"Custom":[]}"#, #"{"OthersAuto":null}"#, #"{"OthersAuto":["B","B"]}"#, #"{"OthersAuto":[42]}"#, #"{"OthersAuto":["bad\nname"]}"#] {
            XCTAssertThrowsError(try AutomaticGroupSources.decode(Data(source.utf8)), source)
        }
        var edit = ProxyGroupEdit(tag: "OthersAuto", type: "urltest"); edit.excludedMembers = ["B"]
        var bundle = SourceBundle(); let raw = " { \"OthersAuto\" : [ \"B\" ] } "; bundle.files["groups.json"] = raw
        XCTAssertEqual(try AutomaticGroupSources.updating(bundle, edits: [edit], content: content).files["groups.json"], raw)
        XCTAssertNil(try AutomaticGroupSources.updating(SourceBundle(), edits: [], content: content).files["groups.json"])
    }
}
