import XCTest
import Libbox
import SakamotoKit
@testable import Sakamoto

@MainActor
final class PolicyEditingTests: XCTestCase {
    func testExplicitMatchChoicesKeepHostNormalizationAndRejectWrongKinds() throws {
        for raw in ["aihubmix.com", "*.AIHUBMIX.com", "suffix:aihubmix.com", "https://aihubmix.com/chat"] {
            var error: NSError?
            let info = try XCTUnwrap(MobilecoreNormalizePolicyRule(raw, "proxy", &error))
            XCTAssertEqual(try PolicyMatchType.suffix.match(raw: raw, normalizedKind: info.kind, value: info.value), "suffix:aihubmix.com")
        }
        XCTAssertEqual(try PolicyMatchType.domain.match(raw: "aihubmix.com", normalizedKind: "domain", value: "aihubmix.com"), "domain:aihubmix.com")
        XCTAssertEqual(PolicyMatchType.automatic.inputForValidation(" *.aihubmix.com "), "*.aihubmix.com")
        XCTAssertThrowsError(try PolicyMatchType.suffix.match(raw: "192.0.2.0/24", normalizedKind: "ip_cidr", value: "192.0.2.0/24"))
        XCTAssertThrowsError(try PolicyMatchType.domain.match(raw: "keyword:aihub", normalizedKind: "domain_keyword", value: "aihub"))
        XCTAssertThrowsError(try PolicyMatchType.cidr.match(raw: "aihubmix.com", normalizedKind: "domain", value: "aihubmix.com"))
        XCTAssertEqual(PolicyMatchType.keyword.inputForValidation("AIhub"), "keyword:AIhub")
    }

    func testEditingExactDomainToSuffixPreservesOrderIdentityAndRestart() throws {
        let suite = "policy-editor-" + UUID().uuidString
        let defaults = UserDefaults(suiteName: suite)!
        defer { defaults.removePersistentDomain(forName: suite) }
        let store = ConfigStore(persistence: defaults, keyStore: ConfigStoreTests.EmptyKeys())
        let exact = StagedPolicyRule(match: "aihubmix.com", action: "proxy")
        let other = StagedPolicyRule(match: "example.org", action: "direct")
        store.commitPolicy(PolicyBook(rules: [exact, other]))
        let model = ConfigModel(tunnel: HomeLifecycleTests.Tunnel(), store: store)
        model.beginPolicyRule(exact)
        XCTAssertEqual(model.newPolicyType, .domain)
        model.newPolicyType = .suffix
        model.addPolicyRule()
        XCTAssertNil(model.policyFormError)
        XCTAssertEqual(store.policy.rules.map(\.match), ["suffix:aihubmix.com", "example.org"])
        XCTAssertEqual(store.policy.rules.first?.id, exact.id)
        let restored = ConfigStore(persistence: defaults, keyStore: ConfigStoreTests.EmptyKeys())
        XCTAssertEqual(restored.policy, store.policy)
        model.beginPolicyRule(store.policy.rules[0])
        XCTAssertEqual(model.newPolicyType, .suffix)
        XCTAssertEqual(model.newPolicyMatch, "aihubmix.com")
        store.commitPolicy(store.policy.adding(StagedPolicyRule(match: "other.example", action: "reject")))
        let before = store.policy
        model.addPolicyRule()
        XCTAssertNotNil(model.policyFormError)
        XCTAssertEqual(store.policy, before)
    }
}
