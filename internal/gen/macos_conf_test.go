package gen

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/experiment"
	"github.com/sagernet/sing-box/experimental/clashmode"
	"github.com/sagernet/sing-box/option"
)

func TestMacOSConfIncludesAdRulesAndHosts(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "macOS.conf")
	ad := filepath.Join(dir, "ad.conf")
	if err := os.WriteFile(main, []byte("[General]\ninclude=ad.conf\ntun-excluded-routes=100.64.0.0/10\n[Rule]\nDOMAIN-SUFFIX,arena.example,PROXY\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ad, []byte("[General]\ntun-excluded-routes=10.0.0.0/8\nprivate-ip-answer=true\n[Rule]\nDOMAIN-SUFFIX,ads.example,REJECT\nDOMAIN-SUFFIX,local.example,DIRECT\nFINAL,DIRECT\n[Host]\ndevice.example.ts.net=192.0.2.10\n"), 0600); err != nil {
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
	globalAt, directModeAt, rejectAt, splitDirectAt, proxyAt := -1, -1, -1, -1, -1
	for i, r := range generated.Route.Rules {
		if r["clash_mode"] == "Global" {
			globalAt = i
			if r["outbound"] == "direct" {
				t.Fatal("Global mode must use the proxy exit")
			}
		}
		if r["clash_mode"] == "Direct" {
			directModeAt = i
			if r["outbound"] != "direct" {
				t.Fatal("Direct mode must not use a proxy")
			}
		}
		if sets, ok := r["rule_set"].([]any); ok && len(sets) == 1 && sets[0] == "rs-reject" {
			rejectAt = i
		}
		if sets, ok := r["rule_set"].([]any); ok && len(sets) > 0 && sets[0] == "rs-direct" {
			splitDirectAt = i
		}
		if sets, ok := r["rule_set"].([]any); ok && len(sets) == 1 && sets[0] == "rs-proxy" {
			proxyAt = i
		}
	}
	if rejectAt < 0 || globalAt <= rejectAt || directModeAt <= globalAt || splitDirectAt <= directModeAt || proxyAt <= splitDirectAt {
		t.Fatalf("mode rules must override split routing but preserve reject safety: reject=%d global=%d direct=%d split=%d proxy=%d", rejectAt, globalAt, directModeAt, splitDirectAt, proxyAt)
	}
	globalDNSDetour := false
	for _, server := range generated.DNS.Servers {
		if server["tag"] == "mode-global-dns" {
			globalDNSDetour = server["detour"] != nil && server["detour"] != "direct"
		}
	}
	if !globalDNSDetour {
		t.Fatal("Global DNS must use the selected proxy chain")
	}
	modeDirectDNS, ruleDirectDNS := -1, -1
	globalEvaluate, responseReject, globalDNSRoute := -1, -1, -1
	for i, r := range generated.DNS.Rules {
		if r["clash_mode"] == "Direct" && r["server"] != nil {
			modeDirectDNS = i
		}
		if r["clash_mode"] == "Rule" && r["rule_set"] != nil {
			ruleDirectDNS = i
		}
		if r["clash_mode"] == "Global" && r["action"] == "evaluate" {
			globalEvaluate = i
		}
		if r["match_response"] == true && r["ip_is_private"] == true {
			responseReject = i
		}
		if r["clash_mode"] == "Global" && r["action"] == "route" {
			globalDNSRoute = i
		}
	}
	if globalEvaluate < 0 || responseReject <= globalEvaluate || globalDNSRoute <= responseReject {
		t.Fatal("Global DNS must preserve private-answer rejection", generated.DNS.Rules)
	}
	if modeDirectDNS < 0 || ruleDirectDNS <= modeDirectDNS {
		t.Fatalf("Direct DNS must precede Rule-only local DNS: %+v", generated.DNS.Rules)
	}
	// Parse native route rules without needing the DNS transport registry.
	parsed := option.Options{Route: &option.RouteOptions{}}
	for _, r := range generated.Route.Rules {
		encoded, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		var native option.Rule
		if err := native.UnmarshalJSONContext(context.Background(), encoded); err != nil {
			t.Fatal(err)
		}
		parsed.Route.Rules = append(parsed.Route.Rules, native)
	}
	modes := clashmode.CalculateModeList(parsed)
	for _, mode := range []string{"Global", "Direct"} {
		if !slices.Contains(modes, mode) {
			t.Fatalf("native API would not expose %s mode: %v", mode, modes)
		}
	}
	if len(generated.Route.Rules) < 4 || generated.Route.Rules[0]["action"] != "sniff" || generated.Route.Rules[1]["action"] != "hijack-dns" || generated.Route.Rules[2]["protocol"] != "stun" || generated.Route.Rules[2]["action"] != "reject" {
		t.Fatalf("block_stun must reject sniffed STUN before direct rules: %v", generated.Route.Rules[:min(4, len(generated.Route.Rules))])
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
	cfg.BlockSTUN = false
	out2 := filepath.Join(dir, "stun-allowed")
	if err := Run(Options{ConfPath: main, NodesFile: nodes, SRJSONPath: cfg.SRJSONPath, Cfg: cfg, OutDir: out2, Quiet: true}); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(out2, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var configNoSTUN struct {
		Route struct {
			Rules []map[string]any `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal(raw, &configNoSTUN); err != nil {
		t.Fatal(err)
	}
	for _, r := range configNoSTUN.Route.Rules {
		if r["protocol"] == "stun" {
			t.Fatal("block_stun=false should not reject STUN")
		}
	}
	cfg.Experiment.Mode = "on"
	out3 := filepath.Join(dir, "always-proxy")
	if err := Run(Options{ConfPath: main, NodesFile: nodes, SRJSONPath: cfg.SRJSONPath, Cfg: cfg, OutDir: out3, Quiet: true}); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(out3, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var always struct {
		Route struct {
			Final string           `json:"final"`
			Rules []map[string]any `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal(raw, &always); err != nil {
		t.Fatal(err)
	}
	if always.Route.Final == "direct" {
		t.Fatal("on must proxy unmatched domains")
	}
	cfg.Experiment.Mode = "auto"
	out4 := filepath.Join(dir, "auto")
	if err := os.MkdirAll(out4, 0700); err != nil {
		t.Fatal(err)
	}
	if err := experiment.Save(out4, experiment.State{Domains: []string{"learned.example"}}); err != nil {
		t.Fatal(err)
	}
	if err := Run(Options{ConfPath: main, NodesFile: nodes, SRJSONPath: cfg.SRJSONPath, Cfg: cfg, OutDir: out4, Quiet: true}); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(out4, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var auto struct {
		Route struct {
			Final string           `json:"final"`
			Rules []map[string]any `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal(raw, &auto); err != nil {
		t.Fatal(err)
	}
	if auto.Route.Final != "direct" {
		t.Fatal("auto must keep unmatched DIRECT until learned")
	}
	learnedAt, proxyAt, directAt := -1, -1, -1
	for i, r := range auto.Route.Rules {
		if r["domain"] != nil {
			learnedAt = i
		}
		if r["outbound"] == "direct" {
			directAt = i
		}
		if set, ok := r["rule_set"].([]any); ok && len(set) == 1 && set[0] == "rs-proxy" {
			proxyAt = i
		}
	}
	if directAt < 0 || learnedAt <= directAt || proxyAt <= learnedAt {
		t.Fatalf("auto rule order invalid: direct=%d learned=%d proxy=%d", directAt, learnedAt, proxyAt)
	}
	if _, err := exec.LookPath("sing-box"); err == nil {
		exit, err := experiment.ProxyOutbound(raw)
		if err != nil {
			t.Fatal(err)
		}
		undo, err := experiment.Stage(out4, "second.example", exit)
		if err != nil {
			t.Fatal(err)
		}
		learned, err := experiment.Load(out4)
		if err != nil || len(learned.Domains) != 2 {
			t.Fatal("promotion not persisted", learned, err)
		}
		if output, err := exec.Command("sing-box", "check", "-D", out4, "-c", filepath.Join(out4, "config.json")).CombinedOutput(); err != nil {
			t.Fatalf("promoted config invalid: %v %s", err, output)
		}
		if err := undo(); err != nil {
			t.Fatal(err)
		}
		learned, err = experiment.Load(out4)
		if err != nil || len(learned.Domains) != 1 {
			t.Fatal("rollback not persisted", learned, err)
		}
	}
}
