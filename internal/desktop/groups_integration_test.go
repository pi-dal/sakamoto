package desktop_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/desktop"
	"github.com/pi-dal/sakamoto/internal/gen"
)

// Drive saved policy through the controller and real generator, without a VPN.
// Failures: stale YAML/runtime, empty groups, invalid tags, lost absent exclusions,
// accidental runtime writes, hidden SOCKS candidates, chain exits and aliasing.
func TestAutomaticGroupsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sakamoto.yaml")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.ConfPath = filepath.Join(dir, "source.conf")
	cfg.NodesFile = filepath.Join(dir, "nodes.txt")
	cfg.TailscaleOptimize = false
	cfg.ChainExit = "Landing"
	cfg.AutomaticGroupExclusions = map[string][]string{"RealityAuto": {"Reality Absent"}}
	cfg.PolicyRules = []config.PolicyRule{{Match: "keep.example", Action: "direct"}}
	nodes := "vless://00000000-0000-4000-8000-000000000001@127.0.0.1:1001#Reality%20A\nvless://00000000-0000-4000-8000-000000000002@127.0.0.1:1002#Reality%20B\nvless://00000000-0000-4000-8000-000000000003@127.0.0.1:1003#Other\nvless://00000000-0000-4000-8000-000000000004@127.0.0.1:1004#Landing\n"
	must(os.WriteFile(cfg.ConfPath, []byte("[General]\ndns-server=https://1.1.1.1/dns-query\n[Rule]\nFINAL,DIRECT\n"), 0600))
	must(os.WriteFile(cfg.NodesFile, []byte(nodes), 0600))
	must(cfg.Save(path))
	generate := func() {
		t.Helper()
		saved, err := config.Load(path)
		must(err)
		must(gen.Run(gen.Options{ConfPath: saved.ConfPath, NodesFile: saved.NodesFile, Cfg: saved, OutDir: dir, Quiet: true}))
	}
	generate()
	controller := desktop.New(path, desktop.Options{ExternalCore: true})
	load := func() desktop.AutomaticGroups {
		t.Helper()
		v, err := controller.AutomaticGroups()
		must(err)
		return v
	}
	before, err := os.ReadFile(filepath.Join(dir, "config.json"))
	must(err)
	draft := load()
	if !draft.Generated || len(draft.Groups) != 2 {
		t.Fatal("groups unavailable", draft)
	}
	if len(draft.Groups[0].Nodes) != 3 || draft.Groups[0].Nodes[2].Present || !draft.Groups[0].Nodes[2].Excluded {
		t.Fatal("absent exclusion lost", draft)
	}
	if len(draft.Groups[1].Nodes) != 1 || draft.Groups[1].Nodes[0].Tag != "Other" {
		t.Fatal("chain exit offered as candidate", draft)
	}
	must(controller.SaveAutomaticGroup(context.Background(), draft, "RealityAuto", []string{"Reality A", "Reality Absent"}))
	saved, err := config.Load(path)
	must(err)
	if !slices.Equal(saved.AutomaticGroupExclusions["RealityAuto"], []string{"Reality A", "Reality Absent"}) || saved.API.Secret != cfg.API.Secret || len(saved.PolicyRules) != 1 {
		t.Fatal("save lost policy or credentials")
	}
	after, err := os.ReadFile(filepath.Join(dir, "config.json"))
	must(err)
	if string(after) != string(before) {
		t.Fatal("save changed runtime")
	}
	stable, err := os.ReadFile(path)
	must(err)
	reject := func(v desktop.AutomaticGroups, tag string, excluded []string) {
		t.Helper()
		if controller.SaveAutomaticGroup(context.Background(), v, tag, excluded) == nil {
			t.Fatal("invalid/stale edit accepted", tag, excluded)
		}
		raw, e := os.ReadFile(path)
		must(e)
		if string(raw) != string(stable) {
			t.Fatal("rejection replaced sidecar")
		}
	}
	reject(draft, "RealityAuto", nil)
	fresh := load()
	reject(fresh, "RealityAuto", []string{"Reality A", "Reality B", "Reality Absent"})
	reject(fresh, "OthersAuto", []string{"Other"})
	reject(fresh, "ManualPick", nil)
	reject(fresh, "RealityAuto", []string{"Unknown"})
	reject(fresh, "RealityAuto", []string{"Reality A", "Reality A"})
	must(os.WriteFile(filepath.Join(dir, "config.json"), append(before, '\n'), 0600))
	reject(fresh, "RealityAuto", nil)
	must(os.WriteFile(filepath.Join(dir, "config.json"), before, 0600))
	generate()
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	must(err)
	var runtime struct {
		Outbounds []struct {
			Tag     string   `json:"tag"`
			Members []string `json:"outbounds"`
		} `json:"outbounds"`
	}
	must(json.Unmarshal(raw, &runtime))
	checkMembers := func(tag string, included, excluded []string) {
		t.Helper()
		for _, group := range runtime.Outbounds {
			if group.Tag == tag {
				for _, node := range included {
					if !slices.Contains(group.Members, node) {
						t.Fatal("missing member", tag, node)
					}
				}
				for _, node := range excluded {
					if slices.Contains(group.Members, node) {
						t.Fatal("excluded member returned", tag, node)
					}
				}
				return
			}
		}
		t.Fatal("missing group", tag)
	}
	checkMembers("RealityAuto", []string{"Reality B"}, []string{"Reality A"})
	checkMembers("ManualPick", []string{"Reality A", "Reality B", "Other"}, []string{"Landing"})
	nodes += "vless://00000000-0000-4000-8000-000000000005@127.0.0.1:1005#Reality%20New\nvless://00000000-0000-4000-8000-000000000006@127.0.0.1:1006#Reality%20Absent\n"
	must(os.WriteFile(cfg.NodesFile, []byte(nodes), 0600))
	generate()
	raw, err = os.ReadFile(filepath.Join(dir, "config.json"))
	must(err)
	must(json.Unmarshal(raw, &runtime))
	// A restored tag still obeys its saved exclusion.
	checkMembers("RealityAuto", []string{"Reality New"}, []string{"Reality A", "Reality Absent"})
	current := load()
	must(controller.SaveAutomaticGroup(context.Background(), current, "RealityAuto", nil))
	generate()
	raw, err = os.ReadFile(filepath.Join(dir, "config.json"))
	must(err)
	must(json.Unmarshal(raw, &runtime))
	checkMembers("RealityAuto", []string{"Reality A", "Reality B", "Reality New"}, nil)
	// SOCKS nodes participate when chaining is disabled, even though the legacy
	// TUI classification helper omits them.
	saved, err = config.Load(path)
	must(err)
	saved.ChainEnabled = false
	must(saved.Save(path))
	must(os.WriteFile(cfg.NodesFile, []byte(nodes+"socks://fixture:fixture@127.0.0.1:1080#Socks\n"), 0600))
	generate()
	current = load()
	found := false
	for _, node := range current.Groups[1].Nodes {
		found = found || node.Tag == "Socks"
	}
	if !found {
		t.Fatal("unchained SOCKS node missing")
	}
	must(os.Remove(filepath.Join(dir, "config.json")))
	if load().Generated {
		t.Fatal("missing generation claimed ready")
	}
	if out := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR"); out != "" {
		must(os.MkdirAll(out, 0755))
		must(os.WriteFile(filepath.Join(out, "automatic-groups-e2e.json"), []byte(`{"passed":true,"realGenerator":true,"staleEditsRejected":true,"emptyGroupsRejected":true,"absentExclusionsPreserved":true,"newNodesJoin":true,"manualSelectionPreserved":true,"saveLeavesRuntimeUntouched":true,"chainExitOmitted":true,"unchainedSocksIncluded":true,"usesRealTUN":false}`+"\n"), 0644))
	}
}
