package menubar

import (
	"context"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/egoist/mygo/ui"
)

// This drives the complete panel view and demo backend through pointer and
// keyboard events before any view implementation exists. Service integration
// is separately exercised against real Unix/gRPC fixtures in internal/desktop.
func TestPanelWorkflow(t *testing.T) {
	backend := NewDemo()
	var panel *Panel
	panel = NewPanel(backend, Actions{Demo: true, Dispatch: func(action func(context.Context) error) {
		if err := action(context.Background()); err != nil {
			t.Error(err)
		}
		panel.Refresh()
	}})
	tt := ui.NewTester(panel.View, PanelWidth, PanelHeight)
	if !tt.HasText("Demo session") || !tt.HasText("Network reachable") {
		t.Fatalf("demo identity or observed phase missing: %v", tt.Texts())
	}
	if err := tt.Click("Disconnect"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Disconnected") {
		t.Fatal("disconnect did not update observed phase")
	}
	if err := tt.Click("Connect"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Open at login") {
		t.Fatal("settings navigation failed")
	}
	if err := tt.Click("Back"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Search nodes"); err != nil {
		t.Fatal(err)
	}
	tt.Type("Osaka")
	if !tt.HasText("Demo Osaka") {
		t.Fatal("node search did not find matching row")
	}
	if err := tt.Click("Use Demo Osaka"); err != nil {
		t.Fatal(err)
	}
	if backend.Snapshot().Notice.Text != "Selected Demo Osaka" {
		t.Fatal("node selection was not dispatched")
	}
	if err := tt.Click("Test Demo Osaka"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Reachable 38 ms") {
		t.Fatal("latency result missing")
	}
	if err := tt.Click("Search nodes"); err != nil {
		t.Fatal(err)
	}
	modifier := ui.Cmd
	if runtime.GOOS != "darwin" {
		modifier = ui.Ctrl
	}
	tt.Key(modifier, ui.KeyA)
	tt.Type("no-match")
	if !tt.HasText("No matching nodes") {
		t.Fatal("empty search state missing")
	}
	if err := tt.Click("Clear search"); err != nil {
		t.Fatal(err)
	}
	tt.SetDark(true)
	if artifact := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR"); artifact != "" {
		if err := os.MkdirAll(artifact, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, dark := range []bool{false, true} {
			tt.SetDark(dark)
			tt.Frame()
			name := "panel-light.png"
			if dark {
				name = "panel-dark.png"
			}
			f, err := os.Create(filepath.Join(artifact, name))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		}
		receipt := map[string]any{"schema": 1, "passed": true, "scenarios": []string{"demo labeled", "connect and disconnect", "settings navigation", "node search", "manual selection", "latency result", "empty search", "light and dark rendering"}, "viewport": []int{PanelWidth, PanelHeight}, "usesRealTUN": false}
		raw, err := json.MarshalIndent(receipt, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(artifact, "panel-workflow.json"), append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
