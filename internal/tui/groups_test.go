package tui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/pi-dal/sakamoto/internal/config"
)

func groupModel(t *testing.T) *model {
	t.Helper()
	m := testModel(t)
	path := filepath.Join(filepath.Dir(m.cfgPath), "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	rows := root["outbounds"].([]any)
	for _, value := range rows {
		node := value.(map[string]any)
		if node["tag"] == "Realm" {
			node["tls"] = map[string]any{"reality": map[string]any{"enabled": true}}
		}
	}
	rows = append(rows, map[string]any{"tag": "R2", "type": "vless", "tls": map[string]any{"reality": map[string]any{"enabled": true}}})
	root["outbounds"] = rows
	raw, err = json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	m.page = configPage
	return m
}

func TestAutomaticGroupsKeyboardMouseSaveAndCancel(t *testing.T) {
	for _, size := range [][2]int{{110, 30}, {72, 20}} {
		m := groupModel(t)
		m.width, m.height = size[0], size[1]
		m.onKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
		view := m.View()
		if !strings.Contains(view, "Proxy groups") || !strings.Contains(view, "RealityAuto") {
			t.Fatal("groups shortcut did not open the member page")
		}
		var target hit
		for _, h := range m.hits {
			if h.action == "edit-auto-group" && h.index == 0 {
				target = h
			}
		}
		m.click(target.x0, target.y)
		if !m.configForm || m.formGroup != "RealityAuto" {
			t.Fatal("group click did not open editor")
		}
		m.onKey(tea.KeyMsg{Type: tea.KeyDown})
		m.onKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
		view = m.View()
		if !strings.Contains(view, "[Excluded] R2") || !strings.Contains(view, "[ Save ]") {
			t.Fatalf("member status or visible save action missing at %v:\n%s", size, view)
		}
		m.onKey(tea.KeyMsg{Type: tea.KeyCtrlS})
		cfg, err := config.Load(m.cfgPath)
		if err != nil || !slices.Equal(cfg.AutomaticGroupExclusions["RealityAuto"], []string{"R2"}) || m.configForm {
			t.Fatalf("exclusion did not persist: %v", err)
		}
		m.editAutomaticGroup(0)
		m.formSelect(1)
		m.onKey(tea.KeyMsg{Type: tea.KeyEsc})
		cfg, err = config.Load(m.cfgPath)
		if err != nil || !slices.Equal(cfg.AutomaticGroupExclusions["RealityAuto"], []string{"R2"}) {
			t.Fatal("cancel changed saved exclusions")
		}
		m.editAutomaticGroup(0)
		m.formSelect(1)
		m.saveConfigForm()
		cfg, err = config.Load(m.cfgPath)
		if err != nil || len(cfg.AutomaticGroupExclusions["RealityAuto"]) != 0 {
			t.Fatal("restore did not clear the exclusion")
		}
	}
}

func TestGroupEditorRejectsEmptyAndStaleSnapshots(t *testing.T) {
	m := groupModel(t)
	m.editAutomaticGroup(0)
	m.formSelect(0)
	m.formSelect(1)
	m.saveConfigForm()
	if !m.configForm || !strings.Contains(m.formNotice, "at least one") || len(m.cfg.AutomaticGroupExclusions) != 0 {
		t.Fatal("empty automatic group was saved")
	}
	m.formSelect(0)
	external, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	external.LogLevel = "debug"
	if err := external.Save(m.cfgPath); err != nil {
		t.Fatal(err)
	}
	m.saveConfigForm()
	if !strings.Contains(m.formNotice, "configuration changed") {
		t.Fatal("stale settings were overwritten")
	}
	m.closeConfigForm()
	m.editAutomaticGroup(0)
	m.formSelect(1)
	path := filepath.Join(filepath.Dir(m.cfgPath), "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	m.saveConfigForm()
	if !strings.Contains(m.formNotice, "configuration changed") {
		t.Fatal("a regenerated snapshot was not detected")
	}
}

func TestGroupExclusionsSurviveRegenerationAndFailedEmptyRefresh(t *testing.T) {
	m := groupModel(t)
	dir := filepath.Dir(m.cfgPath)
	m.cfg.TailscaleOptimize = false
	m.cfg.NodesFile = filepath.Join(dir, "nodes.txt")
	m.cfg.ConfPath = filepath.Join(dir, "local.conf")
	m.cfg.AutomaticGroupExclusions = map[string][]string{"RealityAuto": {"R2"}}
	links := "vless://00000000-0000-4000-8000-000000000001@r.example:443?security=reality&sni=example.com&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&fp=chrome#R1\n" +
		"vless://00000000-0000-4000-8000-000000000001@r.example:443?security=reality&sni=example.com&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&fp=chrome#R2\n"
	if err := os.WriteFile(m.cfg.NodesFile, []byte(links), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.cfg.ConfPath, []byte("[Rule]\nFINAL,DIRECT\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.cfg.Save(m.cfgPath); err != nil {
		t.Fatal(err)
	}
	// The native validation command is installed by the go:check task.
	if _, err := exec.LookPath("sing-box"); err != nil {
		t.Skip("native config validator unavailable")
	}
	if err := regenerate(m.cfgPath); err != nil {
		t.Fatal(err)
	}
	before, err := snapshotGenerated(dir)
	if err != nil {
		t.Fatal(err)
	}
	groups := offlineGroups(m.cfgPath)
	for _, group := range groups {
		if group.Tag == "RealityAuto" && (len(group.Items) != 1 || group.Items[0].Tag != "R1") {
			t.Fatal("excluded node entered generated automatic group")
		}
		if group.Tag == "ManualPick" && len(group.Items) != 2 {
			t.Fatal("manual selection lost a node")
		}
	}
	m.cfg.AutomaticGroupExclusions["RealityAuto"] = []string{"R1", "R2"}
	if err := m.cfg.Save(m.cfgPath); err != nil {
		t.Fatal(err)
	}
	if err := regenerate(m.cfgPath); err == nil {
		t.Fatal("empty available group was accepted during refresh")
	}
	after, err := snapshotGenerated(dir)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("failed refresh replaced working config or rule snapshots")
	}
}

func TestAbsentExclusionsRemainVisibleAndCancelable(t *testing.T) {
	m := groupModel(t)
	m.cfg.AutomaticGroupExclusions = map[string][]string{"RealityAuto": {"Missing"}}
	if err := m.cfg.Save(m.cfgPath); err != nil {
		t.Fatal(err)
	}
	m.editAutomaticGroup(0)
	if !strings.Contains(m.View(), "Missing (currently absent)") {
		t.Fatal("saved absent exclusion cannot be reviewed")
	}
	m.saveConfigForm()
	cfg, err := config.Load(m.cfgPath)
	if err != nil || !slices.Equal(cfg.AutomaticGroupExclusions["RealityAuto"], []string{"Missing"}) {
		t.Fatal("saving present members dropped an absent exclusion")
	}
}
