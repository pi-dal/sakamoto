// Package mobileconf holds the platform-independent core of the sakamoto
// importer: Shadowrocket conf content parsing, share-link parsing, policy
// match normalization and source validation.
//
// Everything here is content-in/content-out: no exec, no launchd, no macOS
// system settings, no filesystem and no network access. The only I/O is the
// optional Hooks.FetchRuleSet callback that the HOST generator (internal/gen)
// supplies to keep its remote RULE-SET behavior identical; mobile callers
// pass no hook and receive the rule-set references as pending entries
// instead — a pending entry is never reported as a generated node or rule.
//
// This package deliberately does NOT import sing-box: pkg/mobilecore is bound
// into Mobilecore.xcframework next to Libbox.xcframework, and both are static
// archives, so a second copy of the sing-box packages would collide at link
// time. internal/gen stays the host-side owner of .srs compilation and
// `sing-box check`; generation is therefore not reimplemented here.
package mobileconf

import "strings"

// maxIncludeDepth mirrors internal/gen: relative include chains are resolved
// recursively on the host with the same bound.
const maxIncludeDepth = 8

// ruleTypes maps Shadowrocket rule names to sing-box rule kinds. An empty
// kind means "no sing-box equivalent; skip".
var ruleTypes = map[string]string{
	"DOMAIN": "domain", "DOMAIN-SUFFIX": "domain_suffix",
	"DOMAIN-KEYWORD": "domain_keyword", "IP-CIDR": "ip_cidr", "IP-CIDR6": "ip_cidr",
	"IP-ASN": "", // No equivalent rule; skip.
}

// commentRe is intentionally shared with internal/gen's scanner semantics:
// only whitespace-prefixed // comments are stripped so https:// URLs in rule
// values survive.
//
// Kept as a plain helper here (regexp compiled once in document.go).

// normTarget folds Shadowrocket targets onto the three sakamoto buckets.
func normTarget(t string) string {
	switch strings.ToUpper(strings.TrimSpace(t)) {
	case "REJECT", "REJECT-DROP", "REJECT-NO-DROP":
		return "reject"
	case "DIRECT", "TAILSCALE":
		return "direct" // Let the desktop Tailscale client handle its own traffic.
	default:
		return "proxy"
	}
}
