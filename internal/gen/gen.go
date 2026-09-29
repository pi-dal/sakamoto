package gen

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/experiment"
)

// ---------------- Main generation pipeline ----------------

type Options struct {
	ConfPath   string
	SRJSONPath string
	NodesFile  string // optional share-link file
	AllowHosts string // comma-separated manual-node host allowlist
	Cfg        *config.Config
	OutDir     string // output directory (defaults to the active runtime)
	Quiet      bool   // suppress stdout so generation does not disrupt the TUI
}

func Run(o Options) error {
	reportMu.Lock()
	defer reportMu.Unlock()
	if o.Quiet {
		reportOutput = io.Discard
	} else {
		reportOutput = os.Stdout
	}
	defer func() { reportOutput = os.Stdout }()
	cfg := o.Cfg
	if cfg == nil {
		cfg = config.Default()
	}
	if err := config.ValidateAPISecret(cfg.API.Secret); err != nil {
		return err
	}
	if err := cfg.ValidateAPIEndpoint(); err != nil {
		return err
	}
	if err := cfg.ValidateExperiment(); err != nil {
		return err
	}
	out := o.OutDir
	if out == "" {
		out = config.DefaultDir()
	}
	rulesDir := filepath.Join(out, "rules")
	if err := os.MkdirAll(rulesDir, 0700); err != nil {
		return err
	}

	hc := &http.Client{Timeout: 30 * time.Second} // Honors HTTP(S)_PROXY from the environment.

	p, err := parseConf(o.ConfPath, hc)
	if err != nil {
		return err
	}
	if isRemote(o.ConfPath) {
		cache := filepath.Join(out, "imports")
		if err := os.MkdirAll(cache, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(cache, "macOS.conf"), []byte(p.rawRoot), 0600); err != nil {
			return err
		}
	}

	ts := tailscaleInfo{}
	if cfg.TailscaleOptimize {
		ts = detectTailscale()
	}
	srsEntries, err := buildRules(p, rulesDir, hc, ts)
	if err != nil {
		return err
	}

	outbounds, nodes, exitTag, mainMembers, err := buildOutbounds(o, cfg, hc)
	if err != nil {
		return err
	}

	// 6) Map [General] dns-server entries to UDP, HTTPS, or TLS resolvers.
	var directSets []string
	if _, ok := p.bk["direct"]; ok {
		directSets = append(directSets, "rs-direct")
	}
	for _, gp := range p.geoip {
		if gp[1] == "direct" {
			directSets = append(directSets, "geoip-"+strings.ToLower(gp[0]))
		}
	}
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
	// Resolve proxy server hostnames locally to avoid a detour bootstrap loop.
	for _, n := range nodes {
		n["domain_resolver"] = localPublic
	}
	// DNS request rules use domain-only sets; IP CIDRs require response matching.
	var dnsDirectSets []string
	if direct, ok := p.bk["direct"]; ok && len(direct["domain"])+len(direct["domain_suffix"])+len(direct["domain_keyword"]) > 0 {
		tag := "rs-direct"
		if len(direct["ip_cidr"]) > 0 {
			tag = "rs-direct-domains"
		}
		dnsDirectSets = []string{tag}
	}
	// Global DNS uses the same protocol as the primary remote resolver but
	// always detours through the selected proxy chain, even if the imported
	// resolver lacked Shadowrocket's #proxy suffix. Bootstrap remains local.
	const globalDNS = "mode-global-dns"
	for _, server := range dnsServers {
		if server["tag"] == remoteTags[0] {
			global := make(map[string]any, len(server)+2)
			for key, value := range server {
				global[key] = value
			}
			global["tag"], global["detour"], global["domain_resolver"] = globalDNS, exitTag, localPublic
			dnsServers = append(dnsServers, global)
			break
		}
	}
	dnsRules := []map[string]any{}
	if len(p.hosts) > 0 {
		dnsServers = append(dnsServers, map[string]any{"type": "hosts", "tag": "sr-hosts", "predefined": p.hosts})
		names := make([]string, 0, len(p.hosts))
		for name := range p.hosts {
			names = append(names, name)
		}
		sort.Strings(names)
		dnsRules = append(dnsRules, map[string]any{"domain": names, "action": "route", "server": "sr-hosts"})
	}
	if ts.Present {
		// Resolve *.ts.net with Tailscale quad100 before generic direct rules.
		dnsServers = append(dnsServers, map[string]any{
			"tag": "ts-dns", "type": "udp", "server": "100.100.100.100"})
		dnsRules = append(dnsRules, map[string]any{
			"domain_suffix": []string{"ts.net"}, "server": "ts-dns"})
	}
	// Direct mode keeps DNS off the chained proxy. Internal hosts and Tailscale
	// rules above retain precedence in every mode.
	dnsRules = append(dnsRules, map[string]any{"clash_mode": "Direct", "action": "route", "server": localPublic})
	if len(dnsDirectSets) > 0 {
		// Explicit DIRECT domains use local DNS only in Rule mode. Otherwise
		// Global would proxy traffic after resolving those names locally.
		dnsRules = append(dnsRules, map[string]any{"clash_mode": "Rule", "rule_set": dnsDirectSets, "server": localPublic})
	}
	// private-ip-answer: evaluate remote DNS first, then reject private answers.
	if strings.EqualFold(p.general["private-ip-answer"], "true") {
		dnsRules = append(dnsRules,
			map[string]any{"clash_mode": "Global", "action": "evaluate", "server": globalDNS},
			map[string]any{"clash_mode": "Rule", "action": "evaluate", "server": remoteTags[0]},
			map[string]any{"match_response": true, "ip_is_private": true, "action": "reject"})
	}
	dnsRules = append(dnsRules, map[string]any{"clash_mode": "Global", "action": "route", "server": globalDNS})

	// 7) route + tun
	routeRules := []map[string]any{
		{"action": "sniff"},
		{"protocol": "dns", "action": "hijack-dns"}, // Globally hijack DNS traffic routed through sing-box.
		{"ip_is_private": true, "action": "route", "outbound": "direct"},
		{"rule_set": []string{"rs-reject"}, "action": "reject"},
		// Global and Direct take precedence over the ordinary split-routing
		// buckets; private routes and reject/security rules above remain active.
		{"clash_mode": "Global", "action": "route", "outbound": exitTag},
		{"clash_mode": "Direct", "action": "route", "outbound": "direct"},
		{"rule_set": directSets, "action": "route", "outbound": "direct"},
		{"rule_set": []string{"rs-proxy"}, "action": "route", "outbound": exitTag},
	}
	var udpProtections []map[string]any
	if cfg.BlockSTUN {
		// Reject identified STUN packets that could expose a public mapped address.
		udpProtections = append(udpProtections, map[string]any{"protocol": "stun", "action": "reject"})
	}
	if cfg.BlockQUIC {
		// Reject UDP:443 to fall back to TCP/TLS instead of QUIC over TCP.
		udpProtections = append(udpProtections, map[string]any{"network": "udp", "port": 443, "action": "reject"})
	}
	if len(udpProtections) > 0 {
		routeRules = append(append(append([]map[string]any{}, routeRules[:2]...), udpProtections...), routeRules[2:]...)
	}
	bypass := splitCSV(p.general["bypass-tun"])
	bypass = append(bypass, splitCSV(p.general["tun-excluded-routes"])...)
	if ts.Present {
		for _, c := range []string{"100.64.0.0/10", "fd7a:115c:a1e0::/48"} {
			if !containsStr(bypass, c) {
				bypass = append(bypass, c)
			}
		}
	}
	{ // Deduplicate exclusions without changing order.
		seen := map[string]bool{}
		dd := bypass[:0]
		for _, x := range bypass {
			if !seen[x] {
				seen[x] = true
				dd = append(dd, x)
			}
		}
		bypass = dd
	}
	tun := map[string]any{
		"type": "tun", "tag": "tun-in", "address": []string{"172.18.0.1/30"},
		// strict_route corresponds to Shadowrocket TunnelEnforceRoutesKey.
		"auto_route": true, "strict_route": cfg.StrictRoute, "stack": cfg.TunStack,
		"udp_timeout": "5m",
	}
	if len(bypass) > 0 {
		tun["route_exclude_address"] = bypass
	}
	if inc := splitCSV(p.general["tun-included-routes"]); len(inc) > 0 {
		tun["route_address"] = inc // Only route listed prefixes through the TUN.
	}
	inbounds := []map[string]any{tun}
	if cfg.MixedInbound.Enabled { // SR ProxyServerType/Port + ProxyShareEnabled
		listen := "127.0.0.1"
		if cfg.MixedInbound.AllowLAN {
			listen = "0.0.0.0"
		}
		inbounds = append(inbounds, map[string]any{
			"type": "mixed", "tag": "in-mixed",
			"listen": listen, "listen_port": cfg.MixedInbound.Port,
		})
	}

	finalTag := "direct"
	if cfg.Experiment.Mode == "on" {
		finalTag = exitTag
	}
	if cfg.Experiment.Mode == "auto" {
		learned, err := experiment.Load(out)
		if err != nil {
			return fmt.Errorf("load auto-proxy rules: %w", err)
		}
		if len(learned.Domains) > 0 {
			// Explicit DIRECT/REJECT stay authoritative: learn only the fallback case.
			at := len(routeRules) - 1
			routeRules = append(routeRules, nil)
			copy(routeRules[at+1:], routeRules[at:])
			routeRules[at] = map[string]any{"domain": learned.Domains, "action": "route", "outbound": exitTag}
		}
	}
	config := map[string]any{
		"log": map[string]any{"level": cfg.LogLevel, "timestamp": true},
		"dns": map[string]any{
			"servers": dnsServers,
			"rules":   dnsRules,
			"final":   remoteTags[0], "strategy": "ipv4_only",
		},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route": map[string]any{
			"rules": routeRules,
			"final": finalTag, "auto_detect_interface": true, "rule_set": srsEntries,
			"default_domain_resolver": map[string]any{"server": localPublic, "strategy": "ipv4_only"},
		},
		"services": []map[string]any{{
			"type": "api", "listen": "127.0.0.1", "listen_port": 9090,
			"secret": cfg.API.Secret, "dashboard": map[string]any{"enabled": false},
		}},
	}
	b, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sing-box config: %w", err)
	}
	cfgPath := filepath.Join(out, "config.json")
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
		return err
	}

	// 6) Write a sidecar template only if the user has not created one.
	var chain []string
	for _, t := range []string{"RealityAuto", "OthersAuto"} {
		for _, m := range mainMembers {
			if m == t {
				chain = append(chain, t)
			}
		}
	}
	sidecarPath := filepath.Join(out, "sakamoto.yaml")
	if _, err := os.Stat(sidecarPath); os.IsNotExist(err) {
		yamlText := fmt.Sprintf("api:\n  url: http://127.0.0.1:9090\n  secret: %s\n", cfg.API.Secret) +
			"check_interval: 30s\nrecover_after: 2\ntest_settle: 5s\nfallback_enabled: true\n"
		if len(chain) > 1 {
			yamlText += "fallbacks:\n  MainProxy: [" + strings.Join(chain, ", ") + "]\n"
		}
		yamlText += "# Add subscriptions in Config; put manual share links in nodes.txt.\nsubscriptions: []\n"
		if err := os.WriteFile(sidecarPath, []byte(yamlText), 0600); err != nil {
			return fmt.Errorf("write template: %w", err)
		}
		reportf("→ %s (template)\n", sidecarPath)
	}

	// 8) Report unsupported source features without claiming parity.
	if len(p.rewrites) > 0 {
		reportf("  ⚠ [URL Rewrite] %d entries omitted (sing-box has no HTTP rewriting layer): %s\n",
			len(p.rewrites), strings.Join(p.rewrites, " | "))
	}
	if len(p.mitm) > 0 {
		reportf("  ⚠ [MITM] omitted (sing-box does not decrypt traffic): %s\n", strings.Join(p.mitm, " | "))
	}
	if len(p.proxies) > 0 {
		reportf("  · [Proxy] %d entries omitted (nodes come from nodes.txt/subscriptions)\n", len(p.proxies))
	}
	if len(p.pgroups) > 0 {
		reportf("  · [Proxy Group] %d entries replaced by RealityAuto/OthersAuto/ManualPick/MainProxy\n", len(p.pgroups))
	}
	if len(p.scripts) > 0 {
		reportf("  ⚠ [Script]/[Host] %d entries omitted (no compatible script/host rewrite engine)\n", len(p.scripts))
	}
	// Report every unmapped [General] option for auditability.
	handled := map[string]bool{"ipv6": true, "prefer-ipv6": true, "bypass-system": true,
		"bypass-tun": true, "skip-proxy": true, "dns-server": true,
		"fallback-dns-server": true, "private-ip-answer": true, "dns-direct-system": true,
		"dns-direct-fallback-proxy": true, "icmp-auto-reply": true,
		"always-reject-url-rewrite": true, "hijack-dns": true,
		"tun-included-routes": true, "tun-excluded-routes": true,
		"always-real-ip": true}
	for k, v := range p.general {
		if !handled[k] {
			reportf("  ? [General] unmapped option: %s = %s\n", k, v)
		}
	}
	reportf("→ %s  final route=%s, proxy exit=%s (detour to MainProxy)\n", cfgPath, finalTag, exitTag)
	reportf("   fallback chain: %v\n", chain)
	return nil
}

// detectTailscale checks local installation, runtime state, and MagicDNS suffix.
type tailscaleInfo struct {
	Present        bool
	MagicDNSSuffix string
}

func detectTailscale() tailscaleInfo {
	var ts tailscaleInfo
	_, errApp := os.Stat("/Applications/Tailscale.app")
	cli, errCli := exec.LookPath("tailscale")
	if errCli != nil {
		if b, err := os.ReadDir("/Applications/Tailscale.app/Contents/MacOS"); err == nil && len(b) > 0 {
			cli = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
		}
	}
	ts.Present = errApp == nil || cli != ""
	if cli != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, cli, "status", "--json").Output(); err == nil {
			var st struct {
				MagicDNSSuffix string `json:"MagicDNSSuffix"`
				BackendState   string `json:"BackendState"`
			}
			if json.Unmarshal(out, &st) == nil {
				ts.MagicDNSSuffix = st.MagicDNSSuffix
				if st.BackendState == "Running" {
					ts.Present = true
				}
			}
		}
	}
	return ts
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func bkAdd(bk buckets, tgt, kind, val string) {
	if bk[tgt] == nil {
		bk[tgt] = map[string]map[string]bool{}
	}
	if bk[tgt][kind] == nil {
		bk[tgt][kind] = map[string]bool{}
	}
	bk[tgt][kind][val] = true
}

// parseShareLinks parses nodes.txt and groups VLESS Reality nodes in RealityAuto.
