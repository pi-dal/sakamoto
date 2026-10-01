package gen

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/pi-dal/sakamoto/internal/config"
)

// dnsAssembly keeps resolver construction and its routing order together.
// Public DNS may be proxied while private/MagicDNS and proxy bootstrap retain
// deliberately separate resolvers.
type dnsAssembly struct {
	servers          []map[string]any
	rules            []map[string]any
	final            string
	ordinaryResolver string
}

type dnsResolvers struct {
	servers              []map[string]any
	primary, localPublic string
	lan                  string
}

func buildDNS(p *parsedConf, cfg *config.Config, nodes []map[string]any, exitTag string, ts tailscaleInfo) dnsAssembly {
	resolvers := buildResolvers(p, cfg, exitTag)
	setNodeResolvers(nodes, cfg, ts, resolvers)
	rules := buildDNSRules(p, cfg, ts, exitTag, &resolvers)
	ordinary := resolvers.localPublic
	if cfg.DNSGuard.Enabled {
		ordinary = resolvers.primary
	}
	return dnsAssembly{resolvers.servers, rules, resolvers.primary, ordinary}
}

func buildResolvers(p *parsedConf, cfg *config.Config, exitTag string) dnsResolvers {
	var dnsServers []map[string]any
	localTags, remoteTags := []string{}, []string{}
	localPublic := "" // First public UDP resolver; private DNS can disappear with a VPN.
	addServer := func(tag, typ, server string, detour bool) {
		s := map[string]any{"tag": tag, "type": typ, "server": server}
		if detour {
			s["detour"] = exitTag
			if localPublic != "" {
				s["domain_resolver"] = localPublic // Bootstrap DoH using the public local resolver.
			}
		}
		dnsServers = append(dnsServers, s)
	}
	for _, d := range splitCSV(p.general["dns-server"]) {
		viaProxy := strings.HasSuffix(d, "#proxy")
		d = strings.TrimSuffix(d, "#proxy")
		switch {
		case strings.HasPrefix(d, "https://"):
			host := strings.TrimPrefix(d, "https://")
			host = strings.SplitN(host, "/", 2)[0] // Keep the host; the default path is /dns-query.
			tag := fmt.Sprintf("remote%d", len(remoteTags))
			remoteTags = append(remoteTags, tag)
			addServer(tag, "https", host, viaProxy)
		case strings.HasPrefix(d, "tls://"):
			tag := fmt.Sprintf("remote%d", len(remoteTags))
			remoteTags = append(remoteTags, tag)
			addServer(tag, "tls", strings.TrimPrefix(d, "tls://"), viaProxy)
		default: // Bare IP: add to the local UDP resolver pool.
			tag := fmt.Sprintf("local%d", len(localTags))
			localTags = append(localTags, tag)
			if localPublic == "" {
				if a, err := netip.ParseAddr(strings.SplitN(d, ":", 2)[0]); err == nil && !a.IsPrivate() {
					localPublic = tag
				}
			}
			addServer(tag, "udp", d, false)
		}
	}
	for _, d := range splitCSV(p.general["fallback-dns-server"]) { // Include fallback resolvers in the local pool.
		tag := fmt.Sprintf("local%d", len(localTags))
		localTags = append(localTags, tag)
		if localPublic == "" {
			if a, err := netip.ParseAddr(strings.SplitN(d, ":", 2)[0]); err == nil && !a.IsPrivate() {
				localPublic = tag
			}
		}
		addServer(tag, "udp", strings.TrimPrefix(strings.TrimPrefix(d, "tls://"), "https://"), false)
	}
	if len(localTags) == 0 {
		localTags = []string{"local0"}
		localPublic = "local0"
		addServer("local0", "udp", "223.5.5.5", false)
	}
	if localPublic == "" {
		localPublic = localTags[0]
	}
	// Pin DoH bootstrap resolution to public local DNS.
	for _, s := range dnsServers {
		if s["detour"] != nil {
			s["domain_resolver"] = localPublic
		}
	}
	if len(remoteTags) == 0 {
		remoteTags = []string{"remote0"}
		addServer("remote0", "https", "dns.google", true)
		dnsServers[len(dnsServers)-1]["domain_resolver"] = localPublic
	}
	// Preserve the imported private resolver only for explicit LAN namespaces.
	lanResolver := localTags[0]
	if cfg.DNSGuard.Enabled {
		const bootstrap = "dns-bootstrap"
		dnsServers = append(dnsServers, map[string]any{
			"tag": bootstrap, "type": "https", "server": cfg.DNSGuard.BootstrapIP,
			"tls": map[string]any{"enabled": true, "server_name": cfg.DNSGuard.BootstrapServerName},
		})
		localPublic = bootstrap // Node/bootstrap resolution is direct but encrypted.
		for _, server := range dnsServers {
			tag, _ := server["tag"].(string)
			if containsStr(remoteTags, tag) {
				server["detour"] = exitTag
				server["domain_resolver"] = bootstrap
			}
		}
	}
	return dnsResolvers{dnsServers, remoteTags[0], localPublic, lanResolver}
}

func setNodeResolvers(nodes []map[string]any, cfg *config.Config, ts tailscaleInfo, resolvers dnsResolvers) {
	// Resolve proxy server hostnames separately to avoid a detour bootstrap loop.
	for _, n := range nodes {
		n["domain_resolver"] = resolvers.localPublic
		if cfg.DNSGuard.Enabled {
			host, _ := n["server"].(string)
			host = strings.ToLower(host)
			for _, suffix := range cfg.DNSGuard.LocalDomains {
				if host == strings.ToLower(suffix) || strings.HasSuffix(host, "."+strings.ToLower(suffix)) {
					n["domain_resolver"] = resolvers.lan
				}
			}
			if ts.Present && (host == "ts.net" || strings.HasSuffix(host, ".ts.net")) {
				n["domain_resolver"] = "ts-dns"
			}
		}
	}
}

func dnsDirectRuleSet(p *parsedConf) string {
	// IP CIDRs require response matching, so DNS only uses domain-only sets.
	direct, ok := p.bk["direct"]
	if !ok || len(direct["domain"])+len(direct["domain_suffix"])+len(direct["domain_keyword"]) == 0 {
		return ""
	}
	if len(direct["ip_cidr"]) > 0 {
		return "rs-direct-domains"
	}
	return "rs-direct"
}

func buildDNSRules(p *parsedConf, cfg *config.Config, ts tailscaleInfo, exitTag string, resolvers *dnsResolvers) []map[string]any {
	// Global DNS always detours through the selected proxy chain, even if the
	// imported resolver lacked Shadowrocket's #proxy suffix.
	const globalDNS = "mode-global-dns"
	for _, server := range resolvers.servers {
		if server["tag"] == resolvers.primary {
			global := make(map[string]any, len(server)+2)
			for key, value := range server {
				global[key] = value
			}
			global["tag"], global["detour"], global["domain_resolver"] = globalDNS, exitTag, resolvers.localPublic
			resolvers.servers = append(resolvers.servers, global)
			break
		}
	}
	dnsRules := []map[string]any{}
	if len(p.hosts) > 0 {
		resolvers.servers = append(resolvers.servers, map[string]any{"type": "hosts", "tag": "sr-hosts", "predefined": p.hosts})
		names := make([]string, 0, len(p.hosts))
		for name := range p.hosts {
			names = append(names, name)
		}
		sort.Strings(names)
		dnsRules = append(dnsRules, map[string]any{"domain": names, "action": "route", "server": "sr-hosts"})
	}
	if ts.Present {
		// Resolve *.ts.net with Tailscale quad100 before generic direct rules.
		resolvers.servers = append(resolvers.servers, map[string]any{
			"tag": "ts-dns", "type": "udp", "server": "100.100.100.100"})
		dnsRules = append(dnsRules, map[string]any{
			"domain_suffix": []string{"ts.net"}, "server": "ts-dns"})
	}
	// Protected DNS takes precedence over traffic mode: only explicit private
	// namespaces and MagicDNS use LAN resolvers; public DNS stays on proxy DoH.
	if cfg.DNSGuard.Enabled && len(cfg.DNSGuard.LocalDomains) > 0 {
		dnsRules = append(dnsRules, map[string]any{"domain_suffix": cfg.DNSGuard.LocalDomains, "server": resolvers.lan, "action": "route"})
	}
	if !cfg.DNSGuard.Enabled {
		dnsRules = append(dnsRules, map[string]any{"clash_mode": "Direct", "action": "route", "server": resolvers.localPublic})
	}
	if directSet := dnsDirectRuleSet(p); directSet != "" && !cfg.DNSGuard.Enabled {
		// Explicit DIRECT domains use local DNS only in Rule mode. Otherwise
		// Global would proxy traffic after resolving those names locally.
		dnsRules = append(dnsRules, map[string]any{"clash_mode": "Rule", "rule_set": []string{directSet}, "server": resolvers.localPublic})
	}
	// private-ip-answer: evaluate remote DNS first, then reject private answers.
	if strings.EqualFold(p.general["private-ip-answer"], "true") {
		if cfg.DNSGuard.Enabled {
			dnsRules = append(dnsRules, map[string]any{"action": "evaluate", "server": resolvers.primary})
		} else {
			dnsRules = append(dnsRules,
				map[string]any{"clash_mode": "Global", "action": "evaluate", "server": globalDNS},
				map[string]any{"clash_mode": "Rule", "action": "evaluate", "server": resolvers.primary})
		}
		dnsRules = append(dnsRules, map[string]any{"match_response": true, "ip_is_private": true, "action": "reject"})
	}
	return append(dnsRules, map[string]any{"clash_mode": "Global", "action": "route", "server": globalDNS})
}
