package gen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pi-dal/sakamoto/internal/config"
)

// writeTemplate leaves an existing user-managed sidecar untouched.
func writeTemplate(out string, cfg *config.Config, mainMembers []string) ([]string, error) {
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
			return nil, fmt.Errorf("write template: %w", err)
		}
		reportf("→ %s (template)\n", sidecarPath)
	}
	return chain, nil
}

// reportGeneration lists unsupported source features without claiming parity.
func reportGeneration(p *parsedConf, cfgPath, finalTag, exitTag string, chain []string) {
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
}
