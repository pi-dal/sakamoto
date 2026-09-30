package gen

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
)

func TestProtectedDNSUsesNativeListenerAndEncryptedBootstrap(t *testing.T) {
	dir := t.TempDir()
	conf, nodes := filepath.Join(dir, "test.conf"), filepath.Join(dir, "nodes.txt")
	if err := os.WriteFile(conf, []byte("[General]\ndns-server=10.8.8.8,https://dns.google/dns-query\nprivate-ip-answer=true\n[Rule]\nDOMAIN,private.example,DIRECT\nDOMAIN,proxy.example,PROXY\nDOMAIN,ads.example,REJECT\nFINAL,DIRECT\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nodes, []byte("hysteria2://synthetic@node.example:443#synthetic\nsocks://203.0.113.10:9000#Exit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.TailscaleOptimize = false
	cfg.DNSGuard.Enabled = true
	if err := Run(Options{ConfPath: conf, NodesFile: nodes, Cfg: cfg, OutDir: dir, Quiet: true}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		DNS struct {
			Final   string
			Servers []map[string]any
			Rules   []map[string]any
		}
		Inbounds  []map[string]any
		Outbounds []map[string]any
		Route     struct {
			Rules    []map[string]any
			Resolver map[string]any `json:"default_domain_resolver"`
		}
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	listener := false
	for _, i := range c.Inbounds {
		if i["tag"] == "protected-dns" {
			listener = i["type"] == "direct" && i["listen"] == "127.0.0.1" && i["listen_port"] == float64(53)
		}
	}
	if !listener || c.Route.Rules[0]["action"] != "hijack-dns" {
		t.Fatal("native DNS interception missing")
	}
	if c.Route.Resolver["server"] != c.DNS.Final {
		t.Fatal("ordinary destination resolution still uses local UDP")
	}
	bootstrap, proxied := false, false
	for _, s := range c.DNS.Servers {
		if s["tag"] == "dns-bootstrap" {
			bootstrap = s["type"] == "https" && s["server"] == "1.12.12.12" && s["detour"] == nil && s["tls"].(map[string]any)["server_name"] == "doh.pub"
		}
		if s["tag"] == c.DNS.Final {
			proxied = s["detour"] != nil && s["domain_resolver"] == "dns-bootstrap"
		}
	}
	if !bootstrap || !proxied {
		t.Fatal("proxy DNS or encrypted direct bootstrap missing")
	}
	for _, r := range c.DNS.Rules {
		if r["rule_set"] != nil || r["clash_mode"] == "Direct" {
			t.Fatal("public DNS can still fall back to local resolver", r)
		}
	}
	for _, n := range c.Outbounds {
		if n["server"] != nil && n["domain_resolver"] != "dns-bootstrap" {
			t.Fatal("proxy node bootstrap is not independent and encrypted")
		}
	}
	if _, err := exec.LookPath("sing-box"); err == nil {
		if out, err := exec.Command("sing-box", "check", "-D", dir, "-c", filepath.Join(dir, "config.json")).CombinedOutput(); err != nil {
			t.Fatalf("native DNS config invalid: %v %s", err, out)
		}
	}
}
