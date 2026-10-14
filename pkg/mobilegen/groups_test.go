package mobilegen

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/pi-dal/sakamoto/pkg/sourcesync"
)

func TestSyncedGroupsGenerateMembersAndKeepAllNodesAndServerRoutes(t *testing.T) {
	files := map[string]string{"nodes.txt": "trojan://test@203.0.113.1:443#A\ntrojan://test@[2001:db8::2]:443#B\n", sourcesync.GroupsFile: `{"OthersAuto":["B","temporarily absent"]}`}
	for _, added := range []bool{false, true} {
		if added {
			files["nodes.txt"] += "trojan://test@203.0.113.3:443#C\n"
		}
		raw, err := GenerateProfileJSON(request(t, files, ""), "")
		if err != nil {
			t.Fatal(err)
		}
		var pkg struct {
			Config           string
			SourceBundleJSON string
		}
		if err := json.Unmarshal([]byte(raw), &pkg); err != nil {
			t.Fatal(err)
		}
		var root struct {
			Outbounds []struct {
				Tag     string
				Members []string `json:"outbounds"`
			}
			Inbounds []struct {
				Excluded []string `json:"route_exclude_address"`
			}
		}
		if err := json.Unmarshal([]byte(pkg.Config), &root); err != nil {
			t.Fatal(err)
		}
		auto, manual := []string{"A"}, []string{"A", "B"}
		if added {
			auto = append(auto, "C")
			manual = append(manual, "C")
		}
		for _, group := range root.Outbounds {
			if group.Tag == "OthersAuto" && !reflect.DeepEqual(group.Members, auto) {
				t.Fatal("synced exclusions ignored", group)
			}
			if group.Tag == "ManualPick" && !reflect.DeepEqual(group.Members, manual) {
				t.Fatal("manual node removed", group)
			}
		}
		if len(root.Inbounds[0].Excluded) != len(manual) {
			t.Fatal("an excluded node server lost TUN bypass", root.Inbounds)
		}
		var saved sourcesync.Bundle
		if err := json.Unmarshal([]byte(pkg.SourceBundleJSON), &saved); err != nil {
			t.Fatal(err)
		}
		if saved.Files[sourcesync.GroupsFile] != files[sourcesync.GroupsFile] {
			t.Fatal("group source bytes lost", saved)
		}
	}
	files[sourcesync.GroupsFile] = `{"OthersAuto":["A","B","C"]}`
	if _, err := GenerateProfileJSON(request(t, files, ""), ""); err == nil {
		t.Fatal("empty group accepted")
	}
	files[sourcesync.GroupsFile] = `{}`
	if _, err := GenerateProfileJSON(request(t, files, ""), ""); err != nil {
		t.Fatal("clear rejected", err)
	}
}
