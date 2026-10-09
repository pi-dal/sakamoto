import XCTest
@testable import SakamotoKit

final class ConfRulePresentationTests: XCTestCase {
    func testOrdinaryRulesAreReadableInsteadOfSecretBullets() {
        let rows = ConfRulePresentation.rows(in: "[General]\ninclude=extra.conf\n[Rule]\n# comment\nDOMAIN,example.com,PROXY\nDOMAIN-SUFFIX,private.example,DIRECT\nIP-CIDR,192.0.2.0/24,REJECT\nFINAL,DIRECT\n[Proxy]\nsecret=node-password")
        XCTAssertEqual(rows, ["include=extra.conf", "DOMAIN,example.com,PROXY", "DOMAIN-SUFFIX,private.example,DIRECT", "IP-CIDR,192.0.2.0/24,REJECT", "FINAL,DIRECT"])
        XCTAssertFalse(rows.contains("•••"))
    }

    func testCommentedSectionHeadersAndMalformedRemoteReferences() {
        XCTAssertEqual(ConfRulePresentation.rows(in: "\u{FEFF}[Rule] // custom rules\nDOMAIN,example.com,PROXY // note"), ["DOMAIN,example.com,PROXY // note"])
        let display = ConfRulePresentation.displayLine("RULE-SET,https://user:secret@[broken/list?token=private,PROXY")
        XCTAssertEqual(display, "RULE-SET,https://…,PROXY")
    }

    func testOnlyRemoteReferencesAreMasked() {
        let line = "RULE-SET,https://user:password@rules.example/private-token/list?token=secret,PROXY"
        let display = ConfRulePresentation.displayLine(line)
        XCTAssertEqual(display, "RULE-SET,https://rules.example/…,PROXY")
        for secret in ["password", "private-token", "secret", "user:"] { XCTAssertFalse(display.contains(secret)) }
        XCTAssertEqual(ConfRulePresentation.displayLine("include=one.conf,https://rules.example/token/two.conf?key=secret"), "include=one.conf,https://rules.example/…")
    }
}
