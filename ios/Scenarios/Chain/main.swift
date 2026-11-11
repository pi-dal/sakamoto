import Foundation
import SakamotoKit

// Repeatable profile -> chain -> serialized profile -> native check workflow.
// Runs without a TUN, source fetches, or access to the owner's configuration.
let destination = URL(fileURLWithPath: CommandLine.arguments.dropFirst().first ?? "../dist/chain-e2e")
try FileManager.default.createDirectory(at: destination, withIntermediateDirectories: true)
let uuid = "00000000-0000-4000-8000-000000000001"
let base = """
{"outbounds":[{"type":"selector","tag":"MainProxy","outbounds":["Entries","ExitOnly"],"default":"ExitOnly"},{"type":"urltest","tag":"Entries","outbounds":["Entry","Landing"]},{"type":"selector","tag":"ExitOnly","outbounds":["Landing"]},{"type":"vless","tag":"Entry","server":"127.0.0.1","server_port":18001,"uuid":"\(uuid)"},{"type":"vless","tag":"Landing","server":"127.0.0.1","server_port":18002,"uuid":"\(uuid)"},{"type":"vmess","tag":"Other","server":"127.0.0.1","server_port":18003,"uuid":"\(uuid)","security":"auto"},{"type":"direct","tag":"direct"}],"route":{"final":"MainProxy","rules":[{"clash_mode":"Global","action":"route","outbound":"MainProxy"},{"type":"logical","mode":"or","rules":[{"domain":["example.com"]}],"outbound":"MainProxy","action":"route"},{"domain":["direct.example"],"outbound":"direct","action":"route"}]},"dns":{"servers":[{"type":"https","tag":"remote","server":"dns.example","detour":"MainProxy"}]}}
"""
func object(_ content: String) throws -> [String: Any] { try JSONSerialization.jsonObject(with: Data(content.utf8)) as! [String: Any] }
func require(_ value: @autoclosure () throws -> Bool, _ message: String) throws {
    if try !value() { throw NSError(domain: "ChainScenario", code: 1, userInfo: [NSLocalizedDescriptionKey: message]) }
}
let chain = ProxyChainSettings(enabled: true, exit: "Landing")
var profile = TunnelProfile(name: "Synthetic chain scenario", config: base)
profile.proxyChain = chain
let stored = try JSONEncoder().encode(profile)
let restored = try JSONDecoder().decode(TunnelProfile.self, from: stored)
let applied = try restored.proxyChain!.applying(to: restored.config)
let root = try object(applied)
let outbounds = root["outbounds"] as! [[String: Any]]
try require(outbounds.first { $0["tag"] as? String == "Landing" }?["detour"] as? String == "MainProxy", "Missing detour")
try require(!outbounds.contains { $0["tag"] as? String == "ExitOnly" }, "Empty group retained")
try require(!outbounds.contains { ($0["outbounds"] as? [String] ?? []).contains("Landing") }, "Exit remains in entry group")
try require(outbounds.first { $0["tag"] as? String == "MainProxy" }?["default"] as? String == "Entries", "Invalid default retained")
let route = root["route"] as! [String: Any]
try require(route["final"] as? String == "Landing", "Final bypasses chain")
let rules = route["rules"] as! [[String: Any]]
try require(rules[1]["outbound"] as? String == "Landing" && (rules[1]["rules"] as! [[String: Any]])[0]["domain"] as? [String] == ["example.com"], "Logical route bypasses chain")
try require(rules[2]["outbound"] as? String == "direct", "DIRECT rule changed")
try require(((root["dns"] as! [String: Any])["servers"] as! [[String: Any]])[0]["detour"] as? String == "Landing", "DNS bypasses chain")
try require(ProxyChainSettings.importedChain(in: applied) == chain, "Generic imported chain not recognized")
let unchained = try ProxyChainSettings.unchainedImportedConfig(applied)
try require((try object(unchained))["route"] as? [String: Any] != nil, "Unchain failed")
try require(try ProxyChainSettings(enabled: false, exit: "Landing").applying(to: base) == base, "Disable lost base memberships")
let switched = try ProxyChainSettings(enabled: true, exit: "Other").applying(to: base)
try require((try object(switched))["route"] as? [String: Any] != nil, "Switch failed")
var rejected = 0
for choice in [ProxyChainSettings(enabled: true, exit: "Missing"), ProxyChainSettings(enabled: true, upstream: "Landing", exit: "Landing"), ProxyChainSettings(enabled: true, upstream: "ExitOnly", exit: "Landing")] {
    do { _ = try choice.applying(to: base) } catch { rejected += 1 }
}
try require(rejected == 3, "Invalid chain accepted")
// The native checker validates real protocol option preservation.
for (name, content) in [("ios-vless-chain", applied), ("ios-vmess-chain", switched)] {
    let file = destination.appendingPathComponent(name + ".json")
    try Data(content.utf8).write(to: file)
    let task = Process(); task.executableURL = URL(fileURLWithPath: "/usr/bin/env")
    task.arguments = ["sing-box", "check", "-c", file.path]
    let output = Pipe(); task.standardOutput = output; task.standardError = output
    try task.run()
    let data = output.fileHandleForReading.readDataToEndOfFile(); task.waitUntilExit()
    try require(task.terminationStatus == 0, "Native check: " + String(decoding: data, as: UTF8.self))
}
let receipt: [String: Any] = ["passed": true, "platform": "ios", "vless_exit": true, "vmess_exit": true, "profile_roundtrip": true, "empty_group_pruned": true, "default_repaired": true, "dns_and_nested_rules": true, "disable_preserves_base": true, "invalid_choices_rejected": rejected, "native_validation": true]
try JSONSerialization.data(withJSONObject: receipt, options: [.prettyPrinted, .sortedKeys]).write(to: destination.appendingPathComponent("ios-chain.json"))
print("PASS: iOS chain profile workflow; receipt: \(destination.path)/ios-chain.json")
