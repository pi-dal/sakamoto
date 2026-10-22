package menubar

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/pi-dal/sakamoto/internal/desktop"
	"github.com/sagernet/sing-box/daemon"
)

// This drives the shared view through real input. These are content renders on
// the test host, not native window captures of another operating system.
func TestSharedDesktopExternalWorkflow(t *testing.T) {
	backend := NewDemo()
	const longName = "Demo \u4e1c\u4eac — Tokyo Premium Route With A Long Descriptive Name"
	_ = backend.update(func(s *desktop.Snapshot) {
		s.ExternalCore = true
		for _, g := range s.Groups {
			if g.Tag != "MainProxy" {
				g.Items = append(g.Items, &daemon.GroupItem{Tag: longName, Type: "test"})
			}
		}
	})
	var panel *Panel
	panel = NewPanel(backend, Actions{Demo: true, Dispatch: func(action func(context.Context) error) {
		if err := action(context.Background()); err != nil {
			t.Error(err)
		}
		panel.Refresh()
	}})
	tt := ui.NewTester(panel.View, PanelWidth, PanelHeight)
	if !tt.HasText("Managed externally") || tt.HasText("Disconnect") {
		t.Fatalf("external lifecycle is misleading: %v", tt.Texts())
	}
	if !tt.HasText("Core online") || tt.HasText("Tunnel running") {
		t.Fatal("API availability was presented as tunnel ownership")
	}
	if tt.HasText("Open TUI") {
		t.Fatal("unsupported TUI action shown")
	}
	if err := tt.Click("Search nodes"); err != nil {
		t.Fatal(err)
	}
	tt.Type("\u4e1c\u4eac")
	if !tt.HasText(longName) {
		t.Fatal("Unicode search failed")
	}
	if err := tt.Click("Use " + longName); err != nil {
		t.Fatal(err)
	}
	if backend.Snapshot().Notice.Text != "Selected "+longName {
		t.Fatal("long node action did not reach backend")
	}
	if err := tt.Click("Clear search"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Open configuration folder") {
		t.Fatal("portable configuration entry missing")
	}
	if err := tt.Click("Back"); err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR"); dir != "" {
		for _, dark := range []bool{false, true} {
			tt.SetDark(dark)
			tt.Frame()
			name := "shared-external-light.png"
			if dark {
				name = "shared-external-dark.png"
			}
			file, err := os.Create(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(file, tt.Image()); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}
