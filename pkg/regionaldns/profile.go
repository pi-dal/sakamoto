// Package regionaldns builds exact-name DoH exceptions for queries captured by the core.
package regionaldns

import (
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

// Profile stays with the device configuration, outside portable rule sources.
// Empty domains disable the profile and allow endpoints to be staged first.
type Profile struct {
	Domains          string `json:"domains" yaml:"domains,omitempty"`
	ResolverIP       string `json:"resolver_ip" yaml:"resolver_ip,omitempty"`
	TLSName          string `json:"tls_name" yaml:"tls_name,omitempty"`
	Relay            string `json:"relay" yaml:"relay,omitempty"`
	RelayFingerprint string `json:"relay_fingerprint,omitempty" yaml:"relay_fingerprint,omitempty"`
}

// ResolverTag identifies the generated exact-domain evaluation rule in the running core.
const ResolverTag = "regional-protected-doh"

var hostLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func hostname(s string) bool {
	labels := strings.Split(s, ".")
	if len(labels) < 2 || len(s) > 253 {
		return false
	}
	for _, label := range labels {
		if !hostLabel.MatchString(label) {
			return false
		}
	}
	return true
}

// These endpoint classes belong to other routing or test mechanisms.
var overlayRange = netip.MustParsePrefix("100.64.0.0/10")
var syntheticRange = netip.MustParsePrefix("198.18.0.0/15")

func publicLiteral(s string) bool {
	a, e := netip.ParseAddr(s)
	if e != nil {
		return false
	}
	a = a.Unmap()
	return a.IsGlobalUnicast() && !a.IsPrivate() && !overlayRange.Contains(a) && !syntheticRange.Contains(a)
}
func within(name, suffix string) bool {
	suffix = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(suffix)), ".")
	return name == suffix || strings.HasSuffix(name, "."+suffix)
}
func (p Profile) names(private []string) ([]string, error) {
	if !publicLiteral(p.ResolverIP) || !hostname(strings.ToLower(p.TLSName)) || p.Relay == "" {
		return nil, errors.New("regional DNS needs a literal public resolver address, TLS hostname and fixed relay")
	}
	reserved := append([]string{"ts.net", "local", "lan", "localhost", "localdomain", "internal", "home.arpa", "arpa", "onion"}, private...)
	var names []string
	seen := map[string]bool{}
	for _, item := range strings.Split(p.Domains, ",") {
		name := strings.ToLower(strings.TrimSpace(item))
		_, ipErr := netip.ParseAddr(name)
		if !hostname(name) || ipErr == nil {
			return nil, errors.New("use exact DNS hostnames separated by commas")
		}
		for _, s := range reserved {
			if within(name, s) {
				return nil, errors.New("private DNS names cannot use a regional resolver")
			}
		}
		if !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	return names, nil
}
func (p Profile) Validate(private []string) error {
	if p.Domains == "" {
		return nil
	}
	_, e := p.names(private)
	return e
}

// ValidateOutbounds checks every dial target and binds the selected relay to its approval.
func (p Profile) ValidateOutbounds(outboundsJSON []byte) error {
	var outbounds []struct {
		Tag, Type, Server, Detour, URL string
		TLS                            struct{ Enabled, Insecure bool }
		Realm                          struct {
			ServerURL   string          `json:"server_url"`
			STUNServers json.RawMessage `json:"stun_servers"`
		}
	}
	if json.Unmarshal(outboundsJSON, &outbounds) != nil {
		return errors.New("missing outbounds")
	}
	if err := p.ValidateLatencyURL(""); err != nil {
		return err
	}
	matched := 0
	for _, o := range outbounds {
		if err := p.ValidateProxyHost(o.Server); err != nil {
			return err
		}
		if o.Type == "hysteria2" && o.Realm.ServerURL != "" {
			var servers []string
			if len(o.Realm.STUNServers) > 0 && json.Unmarshal(o.Realm.STUNServers, &servers) != nil {
				var single string
				if err := json.Unmarshal(o.Realm.STUNServers, &single); err != nil {
					return errors.New("invalid Realm STUN server list")
				}
				servers = []string{single}
			}
			if err := p.ValidateRealm(o.Realm.ServerURL, servers); err != nil {
				return err
			}
		}
		if o.Type == "urltest" {
			if err := p.ValidateLatencyURL(o.URL); err != nil {
				return err
			}
		}
		if o.Tag != p.Relay {
			continue
		}
		matched++
		switch o.Type {
		case "trojan", "vless", "vmess", "hysteria2", "tuic":
		default:
			return errors.New("choose a fixed encrypted relay node")
		}
		if !publicLiteral(o.Server) || !o.TLS.Enabled || o.TLS.Insecure || o.Detour != "" {
			return errors.New("relay needs a literal public endpoint, verified TLS and no chained selector")
		}
	}
	if matched != 1 {
		return errors.New("approved regional DNS relay is unavailable")
	}
	fingerprint, err := fingerprintOutbound(outboundsJSON, p.Relay)
	if err != nil || fingerprint != p.RelayFingerprint {
		return errors.New("approve the current regional DNS relay endpoint before applying")
	}
	return nil
}

// ProtectsHost includes the absolute spelling of an exact DNS name.
func (p Profile) ProtectsHost(host string) bool {
	if p.Domains == "" {
		return false
	}
	host = strings.TrimSuffix(host, ".")
	for _, name := range strings.Split(p.Domains, ",") {
		if strings.EqualFold(strings.TrimSpace(name), host) {
			return true
		}
	}
	return false
}

// ValidateProxyHost checks the server name used to bootstrap an outbound.
func (p Profile) ValidateProxyHost(host string) error {
	if p.ProtectsHost(host) {
		return errors.New("a proxy endpoint overlaps a protected regional DNS name; use a literal endpoint")
	}
	return nil
}

// ValidateRealm checks the HTTPS rendezvous host and UDP discovery hosts.
func (p Profile) ValidateRealm(link string, servers []string) error {
	u, err := url.Parse(link)
	if err != nil || u.Hostname() == "" {
		return errors.New("invalid Realm server URL")
	}
	if err := p.ValidateProxyHost(u.Hostname()); err != nil {
		return err
	}
	for _, address := range servers {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			host = address
		}
		if err := p.ValidateProxyHost(host); err != nil {
			return err
		}
	}
	return nil
}

func (p Profile) validateEndpoints(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var endpoints []struct {
		Type  string
		Peers []struct{ Address string }
	}
	if json.Unmarshal(raw, &endpoints) != nil {
		return errors.New("invalid endpoints")
	}
	for _, endpoint := range endpoints {
		if endpoint.Type == "wireguard" {
			for _, peer := range endpoint.Peers {
				if err := p.ValidateProxyHost(peer.Address); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// DefaultLatencyURL is also used by the pinned core for single-node and selector tests,
// even when every urltest group has its own URL.
const DefaultLatencyURL = "https://www.gstatic.com/generate_204"

func (p Profile) ValidateLatencyURL(link string) error {
	if link == "" {
		link = DefaultLatencyURL
	}
	u, err := url.Parse(link)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("latency test requires an HTTP or HTTPS URL with a hostname")
	}
	if p.ProtectsHost(u.Hostname()) {
		return errors.New("latency test target overlaps a protected regional DNS name")
	}
	return nil
}

// ValidateResolverHost checks the dial address. A TLS certificate name on a
// literal address does not require bootstrap resolution.
func (p Profile) ValidateResolverHost(server string) error {
	if host, _, err := net.SplitHostPort(server); err == nil {
		server = host
	}
	if p.ProtectsHost(server) {
		return errors.New("a DNS server address overlaps a protected regional DNS name; use a literal address")
	}
	return nil
}

func (p Profile) ValidateResolverServers(servers []map[string]any) error {
	for _, s := range servers {
		host, _ := s["server"].(string)
		if err := p.ValidateResolverHost(host); err != nil {
			return err
		}
	}
	return nil
}

// Apply operates on a candidate in memory. Only DNS changes. Unknown DNS rule
// shapes are rejected rather than accidentally overriding private split DNS.
func Apply(raw []byte, p Profile, private []string) ([]byte, error) {
	if p.Domains == "" {
		return raw, nil
	}
	names, err := p.names(private)
	if err != nil {
		return nil, err
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return nil, errors.New("invalid core configuration")
	}
	if err := p.validateEndpoints(root["endpoints"]); err != nil {
		return nil, err
	}
	if err := p.ValidateOutbounds(root["outbounds"]); err != nil {
		return nil, err
	}
	var dns map[string]json.RawMessage
	if json.Unmarshal(root["dns"], &dns) != nil || dns == nil {
		return nil, errors.New("missing DNS configuration")
	}
	var servers []map[string]any
	var rules []map[string]any
	if json.Unmarshal(dns["servers"], &servers) != nil {
		return nil, errors.New("missing DNS servers")
	}
	if r := dns["rules"]; len(r) > 0 && json.Unmarshal(r, &rules) != nil {
		return nil, errors.New("invalid DNS rules")
	}
	if err := p.ValidateResolverServers(servers); err != nil {
		return nil, err
	}
	for _, s := range servers {
		if s["tag"] == ResolverTag {
			return nil, errors.New("regional resolver already configured")
		}
	}
	// Support host-generated rule shapes. Reject ambiguous imported predicates,
	// including a custom branch whose confidentiality requirements are unknown.
	// Keep dynamic MagicDNS ownership ahead of public DNS exceptions.
	var preferred, ordinary []map[string]any
	for _, r := range rules {
		if r["preferred_by"] != nil && r["action"] == "route" && len(r) == 3 {
			preferred = append(preferred, r)
			continue
		}
		ordinary = append(ordinary, r)
		for k := range r {
			switch k {
			case "action", "domain", "domain_suffix", "server", "clash_mode", "rule_set", "ip_is_private", "match_response":
			default:
				return nil, errors.New("regional DNS conflicts with custom DNS rules")
			}
		}
		for _, k := range []string{"domain", "domain_suffix"} {
			if v, ok := r[k]; ok {
				var values []string
				switch x := v.(type) {
				case string:
					values = []string{x}
				case []any:
					for _, entry := range x {
						s, ok := entry.(string)
						if !ok {
							return nil, errors.New("invalid DNS match")
						}
						values = append(values, s)
					}
				default:
					return nil, errors.New("invalid DNS match")
				}
				for _, s := range values {
					for _, name := range names {
						if strings.EqualFold(name, s) || (k == "domain_suffix" && within(name, s)) {
							return nil, errors.New("regional domain overlaps an existing DNS route")
						}
					}
				}
			}
		}
		if r["action"] == "reject" && !(r["match_response"] == true && r["ip_is_private"] == true) {
			return nil, errors.New("regional DNS conflicts with DNS rejection policy")
		}
		if r["rule_set"] != nil {
			sets, ok := r["rule_set"].([]any)
			if !ok || len(sets) != 1 || (sets[0] != "rs-direct" && sets[0] != "rs-direct-domains") || r["clash_mode"] != "Rule" {
				return nil, errors.New("regional DNS conflicts with an imported DNS rule set")
			}
		}
	}
	servers = append(servers, map[string]any{"type": "https", "tag": ResolverTag, "server": p.ResolverIP, "detour": p.Relay, "tls": map[string]any{"enabled": true, "server_name": p.TLSName}})
	scoped := []map[string]any{
		{"domain": names, "action": "evaluate", "tag": ResolverTag, "server": ResolverTag, "remove_client_subnet": true, "disable_cache": true},
		{"match_response": ResolverTag, "ip_is_private": true, "action": "reject"},
		{"match_response": ResolverTag, "action": "respond"},
		// Evaluation failure cannot reach a generic resolver.
		{"domain": names, "action": "predefined", "rcode": "SERVFAIL"},
	}
	dns["servers"], _ = json.Marshal(servers)
	dns["rules"], _ = json.Marshal(append(append(preferred, scoped...), ordinary...))
	root["dns"], _ = json.Marshal(dns)
	if err := scopeDomainDials(root, names, false); err != nil {
		return nil, err
	}
	return json.MarshalIndent(root, "", "  ")
}
