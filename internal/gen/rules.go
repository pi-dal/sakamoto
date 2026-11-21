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

func portableRulePath(rulesDir, path string) string {
	return filepath.ToSlash(filepath.Join(filepath.Base(rulesDir), filepath.Base(path)))
}

// applyPolicyRules adds user-owned overrides before compiling SRS. Direct rules
// are intentionally compiled into the direct set, which has priority over proxy.
func applyPolicyRules(p *parsedConf, cfg *config.Config) error {
	for i, rule := range cfg.PolicyRules {
		action := strings.ToLower(strings.TrimSpace(rule.Action))
		match, kind, err := normalizePolicyMatch(rule.Match)
		if err != nil {
			return fmt.Errorf("policy[%d]: %w", i, err)
		}
		p.addOverride(action, kind, match)
	}
	return nil
}

func (p *parsedConf) addOverride(action, kind, value string) {
	bkAdd(p.bk, action, kind, value)
	p.overrides = append(p.overrides, mobileconf.OrderedRule{Target: action, Kind: kind, Value: value})
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
			p.addOverride("direct", "domain_suffix", it[2:])
		case strings.Contains(it, "/"):
			p.addOverride("direct", "ip_cidr", it)
		case net.ParseIP(it) != nil:
			bits := 128
			if net.ParseIP(it).To4() != nil {
				bits = 32
			}
			p.addOverride("direct", "ip_cidr", fmt.Sprintf("%s/%d", it, bits))
		default:
			p.addOverride("direct", "domain", it) // Includes localhost and captive.apple.com.
		}
	}

	// always-real-ip controls real versus synthetic DNS answers. We already
	// return real addresses; it confers no permission to connect directly.
	if ts.Present {
		reportf("  + Tailscale detected: MagicDNS=%s\n", ts.MagicDNSSuffix)
		for _, cidr := range []string{"100.64.0.0/10", "fd7a:115c:a1e0::/48"} {
			p.addOverride("direct", "ip_cidr", cidr)
		}
	}

	p.prefix = orderedPrefix(p)
	// 1) Compile reject/proxy/direct buckets to SRS without spawning sing-box.
	var srsEntries []map[string]any
	for _, b := range []string{"reject", "proxy", "direct"} {
		m := p.bk[b]
		if len(m) == 0 {
			if b != "direct" {
				srsEntries = append(srsEntries, map[string]any{"type": "inline", "tag": "rs-" + b, "rules": []any{map[string]any{"domain_regex": []string{"a^"}}}})
			}
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
			"type": "local", "tag": "rs-" + b, "format": "binary", "path": portableRulePath(rulesDir, srsPath)})
		reportf("  rs-%s: %d entries\n", b, len(m["domain"])+len(m["domain_suffix"])+len(m["domain_keyword"])+len(m["ip_cidr"]))

		// DNS request matching must not send an earlier proxy domain to the
		// local resolver just because it also appears in the direct tail.
		excluded := option.DefaultHeadlessRule{Invert: true}
		if b == "direct" {
			for _, r := range p.prefix {
				if r.Target != "proxy" {
					continue
				}
				switch r.Kind {
				case "domain":
					excluded.Domain = append(excluded.Domain, r.Value)
				case "domain_suffix":
					excluded.DomainSuffix = append(excluded.DomainSuffix, r.Value)
				case "domain_keyword":
					excluded.DomainKeyword = append(excluded.DomainKeyword, r.Value)
				}
			}
		}
		if len(m["ip_cidr"]) > 0 || excluded.IsValid() {
			hrDom := option.DefaultHeadlessRule{
				Domain: hr.Domain, DomainSuffix: hr.DomainSuffix, DomainKeyword: hr.DomainKeyword}
			domainRule := option.HeadlessRule{Type: "default", DefaultOptions: hrDom}
			if excluded.IsValid() && hrDom.IsValid() {
				domainRule = option.HeadlessRule{Type: "logical", LogicalOptions: option.LogicalHeadlessRule{Mode: "and", Rules: []option.HeadlessRule{domainRule, {Type: "default", DefaultOptions: excluded}}}}
			}
			p2 := filepath.Join(rulesDir, b+"-domains.srs")
			f2, err := os.Create(p2)
			if err != nil {
				return nil, err
			}
			if b == "direct" {
				p.directDomainSet = "rs-direct-domains"
			}
			writeErr := srs.Write(f2, option.PlainRuleSet{
				Rules: []option.HeadlessRule{domainRule},
			}, C.RuleSetVersion3)
			closeErr := f2.Close()
			if writeErr != nil {
				return nil, fmt.Errorf("compile rs-%s-domains: %w", b, writeErr)
			}
			if closeErr != nil {
				return nil, fmt.Errorf("flush rs-%s-domains: %w", b, closeErr)
			}
			srsEntries = append(srsEntries, map[string]any{
				"type": "local", "tag": "rs-" + b + "-domains", "format": "binary", "path": portableRulePath(rulesDir, p2)})
		}
	}

	// 2) Cache GeoIP data as local .srs files.
	seenCountry := map[string]string{}
	for _, gp := range p.geoip {
		cc, tgt := gp[0], gp[1]
		if len(cc) != 2 || strings.Trim(cc, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
			return nil, fmt.Errorf("invalid GEOIP country code")
		}
		if previous, seen := seenCountry[cc]; seen {
			if previous != tgt {
				return nil, fmt.Errorf("conflicting GEOIP policies for %s", cc)
			}
			continue
		}
		seenCountry[cc] = tgt
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
			"format": "binary", "path": portableRulePath(rulesDir, srsPath)})
	}

	return srsEntries, nil
}
