package mobilecore

// Platform-independent importer API for iOS: content in, report out.
//
// Every function here is pure — no exec, no launchd, no macOS system
// settings, no filesystem, no network. The parsing semantics are the shared
// ones from pkg/mobileconf, which internal/gen (the host generator) uses for
// the very same input, so "valid on device" means exactly "the host importer
// agrees".
//
// What is intentionally NOT here: generation. .srs rule-set compilation and
// `sing-box check` stay on the sakamoto host (internal/gen + the sing-box
// binary). Mobilecore.xcframework is statically linked next to
// Libbox.xcframework, so it must not import sing-box packages at all —
// importing them would duplicate symbols at app link time. ValidateConfigJSON
// below is therefore a structural check, labeled as such; semantic
// validation remains host-owned and the iOS UI says so.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pi-dal/sakamoto/pkg/mobileconf"
)

// PolicyRuleInfo is the normalized view of one user policy rule, safe to
// render: kind + folded value + action.
type PolicyRuleInfo struct {
	Match  string // the original match text as entered
	Kind   string // domain | domain_suffix | domain_keyword | ip_cidr
	Value  string // the value sing-box will match (lowercased, host-only for URLs)
	Action string // proxy | direct | reject
}

// NormalizePolicyRule validates one policy rule with the exact semantics the
// host importer and sidecar use (docs/tui.md Config → Policy): plain
// hostnames, *.suffix, keyword:, cidr:, bare IPs/CIDRs, and http(s) URLs
// (only the host is used). The action must be proxy, direct, or reject.
func NormalizePolicyRule(match, action string) (*PolicyRuleInfo, error) {
	kind, value, normalizedAction, err := mobileconf.NormalizePolicyRule(match, action)
	if err != nil {
		return nil, err
	}
	return &PolicyRuleInfo{
		Match:  strings.TrimSpace(match),
		Kind:   kind,
		Value:  value,
		Action: normalizedAction,
	}, nil
}

// ValidatePolicyAction accepts only proxy, direct, or reject
// (case-insensitive).
func ValidatePolicyAction(action string) error {
	return mobileconf.ValidatePolicyAction(action)
}

// NodeLinkInfo is the credential-free summary of one manual share link.
// It never carries the raw link, passwords, UUIDs or public keys. Field
// semantics mirror mobileconf.NodeLinkInfo (kept as a distinct struct here
// because gobind only binds types declared in this package).
//
// Port-range links (hysteria2 mport=) report Port=0: gomobile skips []string
// struct fields, so the range list stays in the unbound mobileconf struct.
type NodeLinkInfo struct {
	Tag    string
	Type   string
	Server string
	// Port is server_port when the link has a single port; 0 when the link
	// carries only a port range.
	Port          int32
	HasCredential bool
}

// IsSubLink reports whether the raw string is a sub:// subscription link —
// these configure subscription sources, not manual nodes.
func IsSubLink(raw string) bool { return mobileconf.IsSubLink(raw) }

// ParseShareLink validates one manual share link and returns its
// credential-free summary. Errors match internal/gen.ParseNodeLink, so a
// link accepted on iOS is accepted by the host generator too.
func ParseShareLink(raw string) (*NodeLinkInfo, error) {
	info, err := mobileconf.ParseShareLinkInfo(raw)
	if err != nil {
		return nil, err
	}
	return &NodeLinkInfo{
		Tag:           info.Tag,
		Type:          info.Type,
		Server:        info.Server,
		Port:          info.Port,
		HasCredential: info.HasCredential,
	}, nil
}

// ValidateSourceURL validates a Shadowrocket .conf source string with the
// same rules as the host importer (HTTP(S) URL or path shape). The Swift
// layer calls this before fetching so the entry points never diverge.
func ValidateSourceURL(source string) error { return mobileconf.ValidSource(source) }

// ParseConfContentJSON parses Shadowrocket conf CONTENT (already fetched or
// read by the app) and returns the validation report as a JSON object —
// content in, content out. Shape (asserted in importer_test.go, mirrored by
// the Swift Codable ConfigModel.ImportSummary):
//
//	{"totalRules": n, "proxyRules": n, "directRules": n, "rejectRules": n,
//	 "finalTarget": "", "hostCount": n, "dnsResolvers": [],
//	 "includesPending": [], "ruleSetsPending": [], "unsupported": []}
//
// The device never fetches anything and never resolves includes: those
// become pending entries. Includes and remote rule sets are masked
// references — pending work for the host generator, never generated rules.
// An error means the content is not a usable Shadowrocket conf — callers
// must keep the last-known-good import untouched. (The report travels as
// one JSON string because gomobile bind cannot carry []string across the
// boundary; it silently skips struct fields of slice type, which is worse
// than an explicit contract.)
func ParseConfContentJSON(content string) (string, error) {
	doc, err := mobileconf.ParseDocument(content, mobileconf.Hooks{})
	if err != nil {
		return "", err
	}
	var resolvers []string
	if servers := strings.TrimSpace(doc.General["dns-server"]); servers != "" {
		for _, resolver := range strings.Split(servers, ",") {
			if r := strings.TrimSpace(resolver); r != "" {
				resolvers = append(resolvers, r)
			}
		}
	}
	var includes, ruleSets []string
	for _, inc := range doc.Includes {
		includes = append(includes, mobileconf.DisplaySource(inc))
	}
	for _, ref := range doc.RuleSets {
		ruleSets = append(ruleSets, mobileconf.DisplaySource(ref.URL))
	}
	var unsupported []string
	if len(doc.Rewrites) > 0 {
		unsupported = append(unsupported,
			fmt.Sprintf("[URL Rewrite] %d entries omitted (sing-box does not rewrite URLs)", len(doc.Rewrites)))
	}
	if len(doc.Mitm) > 0 {
		unsupported = append(unsupported,
			fmt.Sprintf("[MITM] %d entries omitted (sing-box does not decrypt traffic)", len(doc.Mitm)))
	}
	if len(doc.Scripts) > 0 {
		unsupported = append(unsupported,
			fmt.Sprintf("[Script]/[Host] %d entries omitted (no script engine)", len(doc.Scripts)))
	}
	if len(doc.Proxies) > 0 {
		unsupported = append(unsupported,
			fmt.Sprintf("[Proxy] %d entries: nodes come from manual links/subscriptions", len(doc.Proxies)))
	}
	if len(doc.PGroups) > 0 {
		unsupported = append(unsupported,
			fmt.Sprintf("[Proxy Group] %d entries replaced by sakamoto groups", len(doc.PGroups)))
	}
	// nil slices must encode as [] (Swift decodes option-free lists).
	if resolvers == nil {
		resolvers = []string{}
	}
	if includes == nil {
		includes = []string{}
	}
	if ruleSets == nil {
		ruleSets = []string{}
	}
	if unsupported == nil {
		unsupported = []string{}
	}
	report := struct {
		TotalRules      int32    `json:"totalRules"`
		ProxyRules      int32    `json:"proxyRules"`
		DirectRules     int32    `json:"directRules"`
		RejectRules     int32    `json:"rejectRules"`
		FinalTarget     string   `json:"finalTarget"`
		HostCount       int32    `json:"hostCount"`
		DNSResolvers    []string `json:"dnsResolvers"`
		IncludesPending []string `json:"includesPending"`
		RuleSetsPending []string `json:"ruleSetsPending"`
		Unsupported     []string `json:"unsupported"`
	}{
		TotalRules:      int32(doc.TotalRules()),
		ProxyRules:      int32(doc.RuleCount("proxy")),
		DirectRules:     int32(doc.RuleCount("direct")),
		RejectRules:     int32(doc.RuleCount("reject")),
		FinalTarget:     doc.Final,
		HostCount:       int32(len(doc.Hosts)),
		DNSResolvers:    resolvers,
		IncludesPending: includes,
		RuleSetsPending: ruleSets,
		Unsupported:     unsupported,
	}
	b, err := json.Marshal(report)
	if err != nil {
		return "", fmt.Errorf("encode report: %w", err)
	}
	return string(b), nil
}

// ValidateConfigJSON performs the in-process STRUCTURAL check of a generated
// sing-box config: it must parse as a JSON object with the expected
// top-level sections, and every outbound/endpoint must carry type and tag.
// This catches truncated or hand-mangled content before a provider reload.
// It is NOT the full `sing-box check` semantic validation — that stays on
// the sakamoto host, and the UI states this boundary.
func ValidateConfigJSON(content string) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return fmt.Errorf("config is not valid JSON: %w", err)
	}
	for _, section := range []string{"log", "dns", "inbounds", "outbounds", "route"} {
		if _, ok := root[section]; !ok {
			return fmt.Errorf("config is missing the %q section", section)
		}
	}
	var checkList func(section string) error
	checkList = func(section string) error {
		var list []map[string]json.RawMessage
		if err := json.Unmarshal(root[section], &list); err != nil {
			return fmt.Errorf("config %s must be a list of objects", section)
		}
		for i, item := range list {
			for _, field := range []string{"type", "tag"} {
				var value string
				if err := json.Unmarshal(item[field], &value); err != nil || strings.TrimSpace(value) == "" {
					return fmt.Errorf("config %s[%d] is missing %s", section, i, field)
				}
			}
		}
		return nil
	}
	if err := checkList("outbounds"); err != nil {
		return err
	}
	if endpoints, ok := root["endpoints"]; ok && string(endpoints) != "null" {
		var list []map[string]json.RawMessage
		if err := json.Unmarshal(endpoints, &list); err != nil {
			return fmt.Errorf("config endpoints must be a list of objects")
		}
		for i, item := range list {
			for _, field := range []string{"type", "tag"} {
				var value string
				if err := json.Unmarshal(item[field], &value); err != nil || strings.TrimSpace(value) == "" {
					return fmt.Errorf("config endpoints[%d] is missing %s", i, field)
				}
			}
		}
	}
	var route struct {
		Rules []json.RawMessage `json:"rules"`
		Final string            `json:"final"`
	}
	if err := json.Unmarshal(root["route"], &route); err != nil {
		return fmt.Errorf("config route must be an object")
	}
	if strings.TrimSpace(route.Final) == "" {
		return fmt.Errorf("config route.final is empty")
	}
	return nil
}
