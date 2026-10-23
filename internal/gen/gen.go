package gen

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/experiment"
	"github.com/pi-dal/sakamoto/pkg/mobileconf"
)

// ---------------- Main generation pipeline ----------------

type Options struct {
	Context           context.Context
	RequireNodes      bool // recovery must not replace proxy routing with an empty placeholder
	ConfPath          string
	SRJSONPath        string
	NodesFile         string // optional share-link file
	AllowHosts        string // comma-separated manual-node host allowlist
	Cfg               *config.Config
	OutDir            string                                              // output directory (defaults to the active runtime)
	Quiet             bool                                                // suppress stdout so generation does not disrupt the TUI
	OmitLearningProbe bool                                                // mobile providers own their observation and rule lifecycle
	HTTPClient        *http.Client                                        // optional platform-specific fetching policy
	LookupProxyIPs    func(context.Context, string) ([]netip.Addr, error) // injectable bounded DNS lookup
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
	if err := cfg.ValidatePolicy(); err != nil {
		return err
	}
	if err := cfg.ValidateDNSGuard(); err != nil {
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

	ctx := o.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	client := *hc
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = contextTransport{ctx, transport}
	hc = &client

	p, err := parseConf(o.ConfPath, hc)
	if err != nil {
		return err
	}
	if err := applyPolicyRules(p, cfg); err != nil {
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
	if o.RequireNodes && len(nodes) == 0 {
		return fmt.Errorf("no supported proxy nodes; restore source files before regenerating")
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
	dns := buildDNS(p, cfg, nodes, exitTag, ts)

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
	lookup := o.LookupProxyIPs
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	resolveContext, cancelResolve := context.WithTimeout(ctx, 3*time.Second)
	serverRoutes, unresolved := mobileconf.ResolveProxyServerRoutes(resolveContext, outbounds, lookup)
	cancelResolve()
	bypass = append(bypass, serverRoutes...)
	if len(unresolved) > 0 {
		reportf("  warning: %d proxy hostnames could not be resolved for TUN exclusions; regenerate when DNS is available (outbound interface binding remains active)\n", len(unresolved))
	}
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

	if cfg.DNSGuard.Enabled {
		inbounds = append(inbounds, map[string]any{"type": "direct", "tag": "protected-dns", "listen": "127.0.0.1", "listen_port": 53})
		routeRules = append([]map[string]any{{"inbound": []string{"protected-dns"}, "action": "hijack-dns"}}, routeRules...)
	}

	finalTag := "direct"
	if cfg.Experiment.Mode == "on" {
		finalTag = exitTag
	}
	if cfg.Experiment.Mode == "auto" || cfg.Experiment.CFRegionBlock {
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
	endpoint, err := url.Parse(cfg.API.URL)
	if err != nil {
		return err
	}
	apiPort, err := strconv.Atoi(endpoint.Port())
	if err != nil || apiPort < 1 || apiPort > 65535 {
		return fmt.Errorf("api.url must use a valid TCP port")
	}
	config := map[string]any{
		"log": map[string]any{"level": cfg.LogLevel, "timestamp": true},
		"dns": map[string]any{
			"servers": dns.servers,
			"rules":   dns.rules,
			"final":   dns.final, "strategy": "ipv4_only",
		},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route": map[string]any{
			"rules": routeRules,
			"final": finalTag, "auto_detect_interface": true, "rule_set": srsEntries,
			"default_domain_resolver": map[string]any{"server": dns.ordinaryResolver, "strategy": "ipv4_only"},
		},
		"services": []map[string]any{{
			"type": "api", "listen": endpoint.Hostname(), "listen_port": apiPort,
			"secret": cfg.API.Secret, "dashboard": map[string]any{"enabled": false},
		}},
	}
	b, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sing-box config: %w", err)
	}
	if !o.OmitLearningProbe && (cfg.Experiment.Mode == "auto" || cfg.Experiment.CFRegionBlock) {
		b, err = experiment.PrepareProbe(b)
		if err != nil {
			return fmt.Errorf("prepare learning verification: %w", err)
		}
		learned, loadErr := experiment.Load(out)
		if loadErr != nil {
			return loadErr
		}
		b, err = experiment.PrepareLiveRules(b, out, learned.Domains)
		if err != nil {
			return fmt.Errorf("prepare live learning: %w", err)
		}
		if err = experiment.WriteLiveRules(out, learned.Domains); err != nil {
			return err
		}
	}
	cfgPath := filepath.Join(out, "config.json")
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
		return err
	}

	chain, err := writeTemplate(out, cfg, mainMembers)
	if err != nil {
		return err
	}
	reportGeneration(p, cfgPath, finalTag, exitTag, chain)
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
