package gen

import (
	"bytes"
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/pkg/mobilecore"
	"github.com/pi-dal/sakamoto/pkg/regionaldns"
)

func TestRegionalDNSGenerationAndRecovery(t *testing.T) {
	dir := t.TempDir()
	conf, nodes := filepath.Join(dir, "rules.conf"), filepath.Join(dir, "nodes.txt")
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	rules := "[General]\ndns-server=https://fixture.example/dns-query\n[Rule]\nDOMAIN,voice.example,DIRECT\nDOMAIN,ads.example,REJECT\nDOMAIN,proxy.example,PROXY\nFINAL,DIRECT\n"
	write(conf, rules)
	relay := func(server string) string { return "trojan://fixture@" + server + ":443?sni=relay.example#Approved\n" }
	write(nodes, relay("192.0.2.30"))
	cfg := config.Default()
	cfg.NodesFile = nodes
	cfg.TailscaleOptimize = false
	cfg.ChainEnabled = false
	cfg.DNSGuard.Enabled = true
	count := 0
	opts := Options{Cfg: cfg, ConfPath: conf, NodesFile: nodes, OutDir: dir, Quiet: true, LookupProxyIPs: func(context.Context, string) ([]netip.Addr, error) { count++; return nil, nil }}
	generate := func() []byte {
		t.Helper()
		if err := Run(opts); err != nil {
			t.Fatal(err)
		}
		b, e := os.ReadFile(filepath.Join(dir, "config.json"))
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	base := generate()
	write(conf, strings.Replace(rules, "FINAL,DIRECT", "FINAL,PROXY", 1))
	if regionaldns.CheckRecovery(generate(), base) == nil {
		t.Fatal("accepted a direct fallback after tightening FINAL to PROXY")
	}
	write(conf, rules)
	pin, err := regionaldns.RelayFingerprint(base, "Approved")
	if err != nil {
		t.Fatal(err)
	}
	cfg.RegionalDNS = regionaldns.Profile{Domains: "voice.example", ResolverIP: "192.0.2.31", TLSName: "resolver.example", Relay: "Approved", RelayFingerprint: pin}
	approved := generate()
	for _, tc := range []struct{ name, domains, link string }{
		{"configured URL", "voice.example", "https://VOICE.example.:8443/check"},
		{"empty URL", "www.gstatic.com", ""},
		{"single-node built-in URL", "www.gstatic.com", "https://control.example/check"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldProfile, oldURL := cfg.RegionalDNS, cfg.URLTest.URL
			defer func() { cfg.RegionalDNS, cfg.URLTest.URL = oldProfile, oldURL }()
			cfg.RegionalDNS.Domains, cfg.URLTest.URL = tc.domains, tc.link
			if Run(opts) == nil {
				t.Error("generation accepted a protected latency target")
			}
		})
	}
	for _, extra := range []map[string]any{
		{"type": "wireguard", "tag": "wg", "peers": []any{map[string]any{"address": "VOICE.example.", "port": 51820}}},
		{"type": "hysteria2", "tag": "aux", "realm": map[string]any{"server_url": "https://control.example", "stun_servers": []string{"VOICE.example.:3478"}}},
		{"type": "urltest", "tag": "extra", "outbounds": []string{"Approved"}, "url": "https://voice.example./check"},
		{"type": "trojan", "tag": "extra", "server": "VOICE.example.", "server_port": 443, "password": "fixture"},
	} {
		add := func(raw []byte) []byte {
			t.Helper()
			var core map[string]any
			if err := json.Unmarshal(raw, &core); err != nil {
				t.Fatal(err)
			}
			if extra["type"] == "wireguard" {
				core["endpoints"] = []any{extra}
			} else {
				core["outbounds"] = append(core["outbounds"].([]any), extra)
			}
			b, err := json.Marshal(core)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
		p := cfg.RegionalDNS
		if _, err := mobilecore.ApplyRegionalDNS(string(add(base)), p.Domains, p.ResolverIP, p.TLSName, p.Relay, p.RelayFingerprint); err == nil {
			t.Error("mobile import accepted a protected target", extra["type"])
		}
		if regionaldns.Verify(add(approved), p, nil) == nil {
			t.Error("startup accepted a protected target", extra["type"])
		}
	}
	for _, endpoint := range []string{"https://VOICE.example./dns-query", "tls://voice.example", "https://voice.example:443/dns-query"} {
		write(conf, strings.Replace(rules, "https://fixture.example/dns-query", endpoint, 1))
		if Run(opts) == nil {
			t.Errorf("accepted conflicting resolver during generation")
		}
		profile := cfg.RegionalDNS
		cfg.RegionalDNS = regionaldns.Profile{}
		opts.RegionalPreflight = &profile
		if Run(opts) == nil {
			t.Errorf("accepted conflicting resolver during mobile preflight")
		}
		opts.RegionalPreflight = nil
		cfg.RegionalDNS = profile
	}
	write(conf, rules)
	stale := bytes.ReplaceAll(approved, []byte(`"server": "fixture.example"`), []byte(`"server": "Voice.example."`))
	if bytes.Equal(stale, approved) || regionaldns.Verify(stale, cfg.RegionalDNS, nil) == nil {
		t.Error("startup accepted stale resolver endpoint")
	}
	if regionaldns.Verify(approved, regionaldns.Profile{}, nil) == nil {
		t.Error("revoked relay survived pair check")
	}
	if err := regionaldns.Verify(base, regionaldns.Profile{}, nil); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2][]byte{{approved, base}, {base, approved}} {
		if regionaldns.CheckRecovery(pair[0], pair[1]) == nil {
			t.Error("accepted incompatible recovery")
		}
	}
	if err := regionaldns.CheckRecovery(approved, approved); err != nil {
		t.Fatal(err)
	}
	cfg.RegionalDNS.Domains = "another.example"
	changed := generate()
	if regionaldns.CheckRecovery(changed, approved) == nil {
		t.Error("restored an older domain list")
	}
	cfg.RegionalDNS.Domains = "voice.example"
	write(nodes, relay("voice.example"))
	if Run(opts) == nil || count != 0 {
		t.Fatalf("changed relay admitted or caused %d host lookups", count)
	}
	write(nodes, relay("192.0.2.30")+"trojan://fixture@VOICE.example.:443?sni=other.example#Other\n")
	if Run(opts) == nil || count != 0 {
		t.Fatalf("protected bootstrap name admitted or caused %d host lookups", count)
	}
	t.Logf("protected bootstrap lookups=%d; revoked, changed and missing policies rejected", count)
}
