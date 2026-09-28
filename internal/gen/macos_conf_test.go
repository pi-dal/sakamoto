package gen

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
)

func TestMacOSConfIncludesAdRulesAndHosts(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "macOS.conf")
	ad := filepath.Join(dir, "ad.conf")
	if err := os.WriteFile(main, []byte("[General]\ninclude=ad.conf\ntun-excluded-routes=100.64.0.0/10\n[Rule]\nDOMAIN-SUFFIX,arena.example,PROXY\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ad, []byte("[General]\ntun-excluded-routes=10.0.0.0/8\n[Rule]\nDOMAIN-SUFFIX,ads.example,REJECT\nDOMAIN-SUFFIX,local.example,DIRECT\nFINAL,DIRECT\n[Host]\ndevice.example.ts.net=192.0.2.10\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := parseConf(main, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	if p.final != "DIRECT" || p.hosts["device.example.ts.net"] != "192.0.2.10" {
		t.Fatal("final or hosts lost")
	}
	if !slices.Equal(splitCSV(p.general["tun-excluded-routes"]), []string{"100.64.0.0/10", "10.0.0.0/8"}) {
		t.Fatal("include exclusions not merged", p.general["tun-excluded-routes"])
	}
	if !p.bk["reject"]["domain_suffix"]["ads.example"] || !p.bk["proxy"]["domain_suffix"]["arena.example"] {
		t.Fatal("rule buckets missing")
	}
	nodes := filepath.Join(dir, "nodes.txt")
	links := "hysteria2://pass@203.0.113.11:443#hy2\n" + "socks://dXNlcjpwYXNzQDIwMy4wLjExMy4xMDo5MDAw?chain=MAINPROXY\n"
	if err := os.WriteFile(nodes, []byte(links), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.NodesFile = nodes
	cfg.SRJSONPath = filepath.Join(dir, "none.json")
	cfg.TailscaleOptimize = false
	out := filepath.Join(dir, "output")
	if err := Run(Options{ConfPath: main, NodesFile: nodes, SRJSONPath: cfg.SRJSONPath, Cfg: cfg, OutDir: out, Quiet: true}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(out, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var generated struct {
		Route struct {
			Final string           `json:"final"`
			Rules []map[string]any `json:"rules"`
		} `json:"route"`
		DNS struct {
			Servers []map[string]any `json:"servers"`
			Rules   []map[string]any `json:"rules"`
		} `json:"dns"`
		Inbounds []struct {
			Exclude []string `json:"route_exclude_address"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &generated); err != nil {
		t.Fatal(err)
	}
	if generated.Route.Final != "direct" {
		t.Fatalf("FINAL,DIRECT not preserved: %q", generated.Route.Final)
	}
	if !slices.Contains(generated.Inbounds[0].Exclude, "100.64.0.0/10") || !slices.Contains(generated.Inbounds[0].Exclude, "10.0.0.0/8") {
		t.Fatal("tun exclusions lost")
	}
	if len(generated.DNS.Servers) == 0 || generated.DNS.Servers[len(generated.DNS.Servers)-1]["type"] != "hosts" {
		t.Fatal("hosts server missing")
	}
	if len(generated.DNS.Rules) == 0 || !strings.Contains(string(raw), "device.example.ts.net") {
		t.Fatal("host mapping not used")
	}
	if _, err := exec.LookPath("sing-box"); err == nil {
		if output, err := exec.Command("sing-box", "check", "-c", filepath.Join(out, "config.json")).CombinedOutput(); err != nil {
			t.Fatalf("sing-box invalid: %v %s", err, output)
		}
	}
}
