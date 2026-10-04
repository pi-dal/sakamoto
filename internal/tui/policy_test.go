package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/pi-dal/sakamoto/internal/config"
)

func policyIndex(t *testing.T, m *model) int {
	t.Helper()
	for i, row := range m.cfgRows {
		if row.label == "Unmatched policy" {
			return i
		}
	}
	t.Fatal("unmatched policy setting missing")
	return -1
}

func TestUnmatchedPolicyIsThreeStateSwitchNotTextEditor(t *testing.T) {
	m := testModel(t)
	index := policyIndex(t, m)
	m.page, m.cfgCursor = settingsPage, index
	m.cfg.Experiment.Mode = "off"
	for _, want := range []string{"on", "auto", "off"} {
		view := m.View()
		if !strings.Contains(view, "["+m.cfg.Experiment.Mode+"]") {
			t.Fatal("active state not indicated")
		}
		var target hit
		for _, h := range m.hits {
			if h.action == "setting" && h.index == index {
				target = h
				break
			}
		}
		if target.action == "" {
			t.Fatal("three-state switch is not clickable")
		}
		m.click(target.x0, target.y)
		if m.editIndex != -1 || m.cfg.Experiment.Mode != want {
			t.Fatalf("state switch opened editor or chose wrong state: %d %s", m.editIndex, m.cfg.Experiment.Mode)
		}
		loaded, err := config.Load(m.cfgPath)
		if err != nil || loaded.Experiment.Mode != want {
			t.Fatal("state switch was not saved", err)
		}
		if !strings.Contains(m.notice, "regenerate and reconnect") {
			t.Fatal("routing activation boundary is missing")
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.cfg.Experiment.Mode != "on" || m.editIndex != -1 {
		t.Fatal("Enter does not cycle the switch")
	}
}

func TestCFRegionExperimentIsVisibleInSettings(t *testing.T) {
	m := testModel(t)
	m.page, m.height = settingsPage, 24
	m.serviceState = "unavailable"
	m.View()
	if !strings.Contains(m.View(), "CF region auto-proxy") {
		t.Fatal("CF experiment toggle should be visible in Settings")
	}
	for i, row := range m.cfgRows {
		if row.label != "CF region auto-proxy" {
			continue
		}
		m.cfgCursor = i
		for _, h := range m.hits {
			if h.action == "setting" && h.index == i {
				m.click(h.x0, h.y)
				if m.cfg.Experiment.CFRegionBlock {
					return
				}
				t.Fatal("CF experiment toggle did not apply")
			}
		}
		t.Fatal("CF experiment toggle is not clickable")
	}
	t.Fatal("CF experiment toggle missing")
}

func TestCustomPolicyRulePane(t *testing.T) {
	m := testModel(t)
	m.page, m.height = configPage, 32
	m.View()
	var section hit
	for _, h := range m.hits {
		if h.action == "section" && h.index == policySection {
			section = h
			break
		}
	}
	if section.action == "" {
		t.Fatal("policy section is not visible")
	}
	m.click(section.x0, section.y)
	m.View()
	var add hit
	for _, h := range m.hits {
		if h.action == "policy-add" {
			add = h
			break
		}
	}
	if add.action == "" {
		t.Fatal("policy add action is not visible")
	}
	m.click(add.x0, add.y)
	if !m.policyEditing || !strings.Contains(m.View(), "Add policy rule") {
		t.Fatal("policy editor did not open")
	}
	m.policyMatch = "https://Example.com/login"
	m.policyField = 1
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.policyAction != "direct" {
		t.Fatalf("policy action was not selected: %s", m.policyAction)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.policyEditing || len(m.cfg.PolicyRules) != 1 {
		t.Fatal("policy rule was not saved")
	}
	loaded, err := config.Load(m.cfgPath)
	if err != nil || len(loaded.PolicyRules) != 1 || loaded.PolicyRules[0].Action != "direct" {
		t.Fatalf("policy rule was not persisted: %v", err)
	}
	if !strings.Contains(m.notice, "regenerate") {
		t.Fatal("policy activation boundary missing")
	}
}

func TestPolicySaveFailureRestoresTheOriginalState(t *testing.T) {
	m := testModel(t)
	index := policyIndex(t, m)
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	m.cfgPath = filepath.Join(file, "sakamoto.yaml")
	m.cfg.Experiment.Mode = "auto"
	m.activateSetting(index)
	if m.cfg.Experiment.Mode != "auto" || !strings.Contains(m.notice, "Save failed") {
		t.Fatal("failed switch must restore the exact prior state")
	}
}
