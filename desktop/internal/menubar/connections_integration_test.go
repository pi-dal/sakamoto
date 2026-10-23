package menubar

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/pi-dal/sakamoto/internal/desktop"
)

func TestConnectionsPanelEndToEnd(t *testing.T) {
	backend := NewDemo()
	panel := NewPanel(backend, Actions{Demo: true})
	tt := ui.NewTester(panel.View, PanelWidth, PanelHeight)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(label, err, tt.Texts())
		}
	}
	click("Connections")
	if !tt.HasText("example.com") || !tt.HasText("Safari") {
		t.Fatal("connection page missing targets or application", tt.Texts())
	}
	click("Search connections")
	tt.Type("Safari")
	if !tt.HasText("example.com") || tt.HasText("updates.example.org") {
		t.Fatal("application filter failed", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	if panel.connectionQuery != "" || !tt.HasText("updates.example.org") {
		t.Fatal("Escape did not clear connection search")
	}
	click("Search connections")
	tt.Type("no-match")
	if !tt.HasText("No matching connections") {
		t.Fatal("empty search state missing")
	}
	click("Clear search")
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		if dir := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR"); dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			name := "connections-light.png"
			if dark {
				name = "connections-dark.png"
			}
			file, err := os.Create(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(file, tt.Image()); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	click("Back")
	if !tt.HasText("Nodes") || panel.connections {
		t.Fatal("cannot return home")
	}
	click("Connections")
	// Large lists and long names must preserve navigation and virtualize rows.
	_ = backend.update(func(s *desktop.Snapshot) {
		s.Connections = nil
		for i := 0; i < 2000; i++ {
			s.Connections = append(s.Connections, desktop.Connection{ID: fmt.Sprint(i), Target: "\u9577\u3044\u63a5\u7d9a\u5148\u306e\u540d\u524d\u3068Unicode.example.com", Process: "Very long application name", Outbound: "Demo Tokyo", Network: "tcp", DownlinkTotal: 1024})
		}
	})
	panel.Refresh()
	tt.Frame()
	if !tt.HasText("2,000 active") {
		t.Fatal("large-list count missing", tt.Texts())
	}
	if len(tt.Texts()) > 150 {
		t.Fatal("connection rows were not virtualized")
	}
	for state, message := range map[desktop.ConnectionState]string{desktop.ConnectionsRetrying: "Reconnecting…", desktop.ConnectionsUnsupported: "Connection tracking unavailable", desktop.ConnectionsLoading: "Loading connections…"} {
		_ = backend.update(func(s *desktop.Snapshot) { s.Connections = nil; s.ConnectionsState = state })
		panel.Refresh()
		tt.Frame()
		if !tt.HasText(message) {
			t.Fatal("missing connection state", state, tt.Texts())
		}
	}
	_ = backend.update(func(s *desktop.Snapshot) { s.ConnectionsState = desktop.ConnectionsLive })
	panel.Refresh()
	tt.Frame()
	if !tt.HasText("No active connections") {
		t.Fatal("empty active state missing")
	}
	click("Check & repair")
	if panel.connections {
		t.Fatal("repair navigation left connections view active")
	}
}
