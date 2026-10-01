package gen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
)

// TestDNSGenerationGolden freezes the complete DNS rules/server order, the
// ordinary destination resolver and every proxy-node bootstrap resolver. It
// runs offline with synthetic data so refactoring cannot silently change DNS.
func TestDNSGenerationGolden(t *testing.T) {
	for _, tc := range []struct {
		name   string
		guard  bool
		digest string
	}{
		{"legacy", false, "112ffe5e026a0be346fce580c7b2c56d8952fc35dbca7fad358955e75585a870"},
		{"protected", true, "8f48e30909e4db1541563b887a460c006a59e8141c4833682f175d8cee942859"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			conf := filepath.Join(dir, "rules.conf")
			nodes := filepath.Join(dir, "nodes.txt")
			const rules = "[General]\ndns-server=10.8.8.8,https://dns.google/dns-query#proxy,tls://one.one.one.one#proxy\nfallback-dns-server=223.5.5.5\nprivate-ip-answer=true\n[Rule]\nDOMAIN,private.example,DIRECT\nDOMAIN,proxy.example,PROXY\nDOMAIN,ads.example,REJECT\nFINAL,DIRECT\n[Host]\nrouter.lan=192.0.2.1\n"
			if err := os.WriteFile(conf, []byte(rules), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(nodes, []byte("hysteria2://synthetic@node.example:443#synthetic\nsocks://203.0.113.10:9000#Exit\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := config.Default()
			cfg.API.Secret = "0123456789abcdef0123456789abcdef" // Synthetic test-only key.
			cfg.TailscaleOptimize = false
			cfg.DNSGuard.Enabled = tc.guard
			cfg.NodesFile = nodes
			cfg.SRJSONPath = filepath.Join(dir, "absent-export.json")
			if err := Run(Options{ConfPath: conf, NodesFile: nodes, SRJSONPath: cfg.SRJSONPath, Cfg: cfg, OutDir: dir, Quiet: true}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(dir, "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			var generated struct {
				DNS   json.RawMessage `json:"dns"`
				Route struct {
					Resolver json.RawMessage `json:"default_domain_resolver"`
				} `json:"route"`
				Outbounds []struct {
					Tag      string `json:"tag"`
					Server   string `json:"server"`
					Resolver string `json:"domain_resolver"`
				} `json:"outbounds"`
			}
			if err := json.Unmarshal(data, &generated); err != nil {
				t.Fatal(err)
			}
			nodesWithResolvers := make(map[string]string)
			for _, outbound := range generated.Outbounds {
				if outbound.Server != "" {
					nodesWithResolvers[outbound.Tag] = outbound.Resolver
				}
			}
			canonical, err := json.Marshal(struct {
				DNS      json.RawMessage   `json:"dns"`
				Resolver json.RawMessage   `json:"resolver"`
				Nodes    map[string]string `json:"nodes"`
			}{generated.DNS, generated.Route.Resolver, nodesWithResolvers})
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(canonical)
			got := hex.EncodeToString(sum[:])
			if got != tc.digest {
				t.Fatalf("DNS generation changed: got %s; want %s", got, tc.digest)
			}
		})
	}
}
