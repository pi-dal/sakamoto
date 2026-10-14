package gen

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
)

func TestAutomaticExclusionsPersistAndNewNodesJoin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nodes.txt")
	cfg := config.Default()
	cfg.AutomaticGroupExclusions = map[string][]string{"RealityAuto": {"R2"}, "OthersAuto": {"O2"}}
	cfgPath := filepath.Join(dir, "sakamoto.yaml")
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	reality := func(tag string) string {
		return "vless://00000000-0000-4000-8000-000000000001@r.example:443?security=reality&sni=example.com&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&fp=chrome#" + tag
	}
	other := func(tag string) string { return "trojan://synthetic@o.example:443#" + tag }
	for _, returned := range []bool{false, true} {
		links := []string{reality("R1"), reality("R3"), other("O1"), other("O3")}
		if returned {
			links = append(links, reality("R2"), other("O2"))
		}
		if err := os.WriteFile(path, []byte(strings.Join(links, "\n")), 0600); err != nil {
			t.Fatal(err)
		}
		groups, nodes, _, _, err := buildOutbounds(Options{NodesFile: path}, cfg, &http.Client{})
		if err != nil {
			t.Fatal(err)
		}
		for _, group := range groups {
			tag, _ := group["tag"].(string)
			members, _ := group["outbounds"].([]string)
			switch tag {
			case "RealityAuto":
				if !slices.Equal(members, []string{"R1", "R3"}) {
					t.Fatalf("RealityAuto = %v", members)
				}
			case "OthersAuto":
				if !slices.Equal(members, []string{"O1", "O3"}) {
					t.Fatalf("OthersAuto = %v", members)
				}
			case "ManualPick":
				if len(members) != len(links) {
					t.Fatalf("manual nodes removed: %v", members)
				}
			}
		}
		if len(nodes) != len(links) || !slices.Equal(cfg.AutomaticGroupExclusions["RealityAuto"], []string{"R2"}) {
			t.Fatal("node or persisted exclusion was lost")
		}
	}
	cfg.AutomaticGroupExclusions["RealityAuto"] = nil
	groups, _, _, _, err := buildOutbounds(Options{NodesFile: path}, cfg, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if group["tag"] == "RealityAuto" && !slices.Contains(group["outbounds"].([]string), "R2") {
			t.Fatal("restoring an excluded node did not re-include it")
		}
	}
}

func TestAutomaticGroupCannotExcludeEveryAvailableNode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.txt")
	if err := os.WriteFile(path, []byte("trojan://synthetic@o.example:443#O1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.AutomaticGroupExclusions = map[string][]string{"OthersAuto": {"O1"}}
	if _, _, _, _, err := buildOutbounds(Options{NodesFile: path}, cfg, &http.Client{}); err == nil || !strings.Contains(err.Error(), "no available members") {
		t.Fatalf("empty group accepted: %v", err)
	}
	cfg.AutomaticGroupExclusions = map[string][]string{"RealityAuto": {"temporarily missing"}}
	if _, _, _, _, err := buildOutbounds(Options{NodesFile: path}, cfg, &http.Client{}); err != nil {
		t.Fatalf("an absent category must not block unrelated nodes: %v", err)
	}
}
