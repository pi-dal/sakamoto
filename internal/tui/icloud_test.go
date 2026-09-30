package tui

import (
	"strings"
	"testing"
)

func cloudSettingIndex(t *testing.T, m *model, label string) int {
	t.Helper()
	for i, row := range m.cfgRows {
		if row.label == label {
			return i
		}
	}
	t.Fatalf("missing iCloud control %s", label)
	return -1
}
func TestCloudAndConfUploadRequireConfirmation(t *testing.T) {
	m := testModel(t)
	index := cloudSettingIndex(t, m, "Sync sources to iCloud")
	m.toggleSetting(index)
	if m.cfg.ICloud.Enabled || !strings.Contains(m.notice, "Click again") {
		t.Fatal("cloud upload enabled without confirmation")
	}
	m.toggleSetting(index)
	if !m.cfg.ICloud.Enabled || !strings.Contains(m.notice, "no VPN reconnect") {
		t.Fatal("cloud setting not applied without network restart")
	}
	m.cfg.ICloud.IncludeConf = false
	index = cloudSettingIndex(t, m, "Include conf and rule includes")
	m.toggleSetting(index)
	if m.cfg.ICloud.IncludeConf || !strings.Contains(m.notice, "Click again") {
		t.Fatal("additional conf upload did not request consent")
	}
	m.toggleSetting(index)
	if !m.cfg.ICloud.IncludeConf {
		t.Fatal("conf discovery not enabled after confirmation")
	}
}
func TestCloudPathsAllowSourcesButRejectNestedGeneratedState(t *testing.T) {
	m := testModel(t)
	index := cloudSettingIndex(t, m, "Additional source paths")
	edit := m.cfgRows[index].edit
	if err := edit("nodes.txt,sources/current/extra.conf"); err != nil {
		t.Fatal(err)
	}
	for _, unsafe := range []string{"../secrets", "nested/config.json", "sources/auth.json", "rules/ad.srs"} {
		if err := edit(unsafe); err == nil {
			t.Fatalf("unsafe cloud source accepted: %s", unsafe)
		}
	}
}
