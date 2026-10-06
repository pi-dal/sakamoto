package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/s3sync"
	"github.com/pi-dal/sakamoto/pkg/sourcesync"
)

func TestS3EditorFitsAndButtonsMatchVisibleCells(t *testing.T) {
	for _, size := range [][2]int{{110, 30}, {72, 20}, {64, 16}} {
		m := testModel(t)
		m.width, m.height = size[0], size[1]
		m.page = settingsPage
		for i, r := range m.cfgRows {
			if r.label == "S3 endpoint" {
				m.activateSetting(i)
				break
			}
		}
		view := m.View()
		lines := strings.Split(view, "\n")
		if len(lines) != m.height {
			t.Fatal("editor height overflow")
		}
		for _, line := range lines {
			if lipgloss.Width(line) > m.width {
				t.Fatal("editor width overflow")
			}
		}
		found := false
		for _, h := range m.hits {
			if h.action == "setting-cancel" {
				found = true
				if h.y >= len(lines) || !strings.Contains(lines[h.y], "[ Cancel ]") {
					t.Fatal("cancel hit is not on its rendered row")
				}
				m.click(h.x0, h.y)
				if m.editIndex != -1 {
					t.Fatal("cancel not clickable")
				}
			}
		}
		if !found {
			t.Fatal("cancel not visible")
		}
	}
}

func TestS3CredentialsMaskedAndNeverSerializedToSidecar(t *testing.T) {
	m := testModel(t)
	c := sourcesync.Credentials{AccessKey: "synthetic-access-key", SecretKey: "synthetic-secret-key"}
	if err := s3sync.SaveCredentials(filepath.Dir(m.cfgPath), c); err != nil {
		t.Fatal(err)
	}
	m.buildSettings()
	m.page = settingsPage
	index := -1
	for i, r := range m.cfgRows {
		if r.label == "S3 secret key" {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("missing S3 credential editor")
	}
	m.cfgCursor = index
	view := m.View()
	if strings.Contains(view, c.AccessKey) || strings.Contains(view, c.SecretKey) {
		t.Fatal("credentials displayed")
	}
	m.activateSetting(index)
	view = m.View()
	if strings.Contains(view, c.SecretKey) {
		t.Fatal("credential editor revealed secret")
	}
	raw, err := m.cfg.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), c.SecretKey) {
		t.Fatal("credential serialized into sidecar")
	}
	loaded, err := config.Load(m.cfgPath)
	if err != nil || loaded.S3.Enabled {
		t.Fatal("S3 must be opt-in", err)
	}
}
