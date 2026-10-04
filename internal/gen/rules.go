package gen

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/pkg/mobileconf"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

// applyPolicyRules adds user-owned overrides before compiling SRS. Direct rules
// are intentionally compiled into the direct set, which has priority over proxy.
func applyPolicyRules(p *parsedConf, cfg *config.Config) error {
	for i, rule := range cfg.PolicyRules {
		action := strings.ToLower(strings.TrimSpace(rule.Action))
		match, kind, err := normalizePolicyMatch(rule.Match)
		if err != nil {
			return fmt.Errorf("policy[%d]: %w", i, err)
		}
		bkAdd(p.bk, action, kind, match)
	}
	return nil
}

// normalizePolicyMatch delegates to the shared implementation in
// pkg/mobileconf, which also backs the iOS Policy validation — both sides
// accept and fold matches identically.
func normalizePolicyMatch(raw string) (string, string, error) {
	return mobileconf.NormalizePolicyMatch(raw)
}

// buildRules resolves all direct overrides BEFORE compiling SRS so DNS and route sets agree.
func buildRules(p *parsedConf, rulesDir string, hc *http.Client, ts tailscaleInfo) ([]map[string]any, error) {
	// 0) Add skip-proxy domains and prefixes to direct before compiling SRS.
	for _, it := range splitCSV(p.general["skip-proxy"]) {
		switch {
		case strings.HasPrefix(it, "*."):
			bkAdd(p.bk, "direct", "domain_suffix", it[2:])
		case strings.Contains(it, "/"):
			bkAdd(p.bk, "direct", "ip_cidr", it)
		case net.ParseIP(it) != nil:
			bkAdd(p.bk, "direct", "ip_cidr", it+"/32")
		default:
			bkAdd(p.bk, "direct", "domain", it) // Includes localhost and captive.apple.com.
		}
	}

	for _, item := range splitCSV(p.general["always-real-ip"]) {
		if strings.HasPrefix(item, "*.") {
			bkAdd(p.bk, "direct", "domain_suffix", item[2:])
		} else {
			bkAdd(p.bk, "direct", "domain", item)
		}
	}
	if ts.Present {
		reportf("  + Tailscale detected: MagicDNS=%s\n", ts.MagicDNSSuffix)
		for _, cidr := range []string{"100.64.0.0/10", "fd7a:115c:a1e0::/48"} {
			bkAdd(p.bk, "direct", "ip_cidr", cidr)
		}
	}

	// 1) Compile reject/proxy/direct buckets to SRS without spawning sing-box.
	var srsEntries []map[string]any
	for _, b := range []string{"reject", "proxy", "direct"} {
		m := p.bk[b]
		if len(m) == 0 {
			continue
		}
		hr := option.DefaultHeadlessRule{}
		for _, k := range []string{"domain", "domain_suffix", "domain_keyword", "ip_cidr"} {
			if len(m[k]) == 0 {
				continue
			}
			items := make([]string, 0, len(m[k]))
			for it := range m[k] {
				items = append(items, it)
			}
			sort.Strings(items)
			switch k {
			case "domain":
				hr.Domain = items
			case "domain_suffix":
				hr.DomainSuffix = items
			case "domain_keyword":
				hr.DomainKeyword = items
			case "ip_cidr":
				hr.IPCIDR = items
			}
		}
		srsPath := filepath.Join(rulesDir, b+".srs")
		f, err := os.Create(srsPath)
		if err != nil {
			return nil, err
		}
		err = srs.Write(f, option.PlainRuleSet{
			Rules: []option.HeadlessRule{{Type: "default", DefaultOptions: hr}},
		}, C.RuleSetVersion3)
		closeErr := f.Close()
		if err != nil {
			return nil, fmt.Errorf("compile rs-%s: %w", b, err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("flush rs-%s: %w", b, closeErr)
		}
		srsEntries = append(srsEntries, map[string]any{
			"type": "local", "tag": "rs-" + b, "format": "binary", "path": srsPath})
		reportf("  rs-%s: %d entries\n", b, len(m["domain"])+len(m["domain_suffix"])+len(m["domain_keyword"])+len(m["ip_cidr"]))

		// Also compile a domain-only set for DNS request-time matching.
		if len(m["ip_cidr"]) > 0 {
			hrDom := option.DefaultHeadlessRule{
				Domain: hr.Domain, DomainSuffix: hr.DomainSuffix, DomainKeyword: hr.DomainKeyword}
			p2 := filepath.Join(rulesDir, b+"-domains.srs")
			f2, err := os.Create(p2)
			if err != nil {
				return nil, err
			}
			writeErr := srs.Write(f2, option.PlainRuleSet{
				Rules: []option.HeadlessRule{{Type: "default", DefaultOptions: hrDom}},
			}, C.RuleSetVersion3)
			closeErr := f2.Close()
			if writeErr != nil {
				return nil, fmt.Errorf("compile rs-%s-domains: %w", b, writeErr)
			}
			if closeErr != nil {
				return nil, fmt.Errorf("flush rs-%s-domains: %w", b, closeErr)
			}
			srsEntries = append(srsEntries, map[string]any{
				"type": "local", "tag": "rs-" + b + "-domains", "format": "binary", "path": p2})
		}
	}

	// 2) Cache GeoIP data as local .srs files.
	for _, gp := range p.geoip {
		cc, tgt := gp[0], gp[1]
		srsPath := filepath.Join(rulesDir, "geoip-"+strings.ToLower(cc)+".srs")
		if _, err := os.Stat(srsPath); os.IsNotExist(err) {
			address := fmt.Sprintf("https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geoip/%s.srs", strings.ToLower(cc))
			resp, err := hc.Get(address)
			if err != nil {
				return nil, fmt.Errorf("download geoip-%s: %w", cc, err)
			}
			if resp.StatusCode != http.StatusOK {
				_ = resp.Body.Close()
				return nil, fmt.Errorf("download geoip-%s: HTTP %d", cc, resp.StatusCode)
			}
			data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxConfBytes+1))
			closeErr := resp.Body.Close()
			if readErr != nil {
				return nil, fmt.Errorf("read geoip-%s: %w", cc, readErr)
			}
			if closeErr != nil {
				return nil, fmt.Errorf("close geoip-%s stream: %w", cc, closeErr)
			}
			if len(data) > maxConfBytes {
				return nil, fmt.Errorf("geoip-%s file exceeds the size limit", cc)
			}
			if err := os.WriteFile(srsPath, data, 0600); err != nil {
				return nil, fmt.Errorf("save geoip-%s: %w", cc, err)
			}
			reportf("  geoip-%s.srs downloaded\n", strings.ToLower(cc))
		} else if err != nil {
			return nil, fmt.Errorf("read geoip-%s cache: %w", cc, err)
		}
		srsEntries = append(srsEntries, map[string]any{
			"type": "local", "tag": "geoip-" + strings.ToLower(cc),
			"format": "binary", "path": srsPath})
		_ = tgt
	}

	return srsEntries, nil
}
