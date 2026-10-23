package menubar

import (
	"context"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Exercise the restyled primitives in the real panel, including keyboard
// selection, persisted preferences, input editing, both themes and idle frames.
func TestPanelControlsEndToEnd(t *testing.T) {
	backend := NewDemo()
	var panel *Panel
	var appearances []string
	var logins []bool
	panel = NewPanel(backend, Actions{
		Demo: true,
		Dispatch: func(action func(context.Context) error) {
			if err := action(context.Background()); err != nil {
				t.Error(err)
			}
			panel.Refresh()
		},
		SetAppearance: func(value string) error { appearances = append(appearances, value); return nil },
		SetLogin:      func(value bool) error { logins = append(logins, value); return nil },
	})
	tt := ui.NewTester(panel.View, PanelWidth, PanelHeight)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(label, err, tt.Texts())
		}
	}
	save := func(name string) {
		t.Helper()
		dir := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR")
		if dir == "" {
			return
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
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
	click("Global")
	if backend.Snapshot().Mode != "Global" {
		t.Fatal("routing segment did not change the observed mode")
	}
	tt.Key(0, ui.KeyLeft)
	if backend.Snapshot().Mode != "Rule" {
		t.Fatal("routing keyboard selection failed")
	}
	click("Node group")
	tt.Key(0, ui.KeyEnd)
	tt.Key(0, ui.KeyEnter)
	if panel.group != "ManualPick" {
		t.Fatal("node group keyboard choice failed", panel.group)
	}
	click("Search nodes")
	tt.Type("Osaka")
	_, tokyoSelectable := tt.Find("Use Demo Tokyo")
	if !tt.HasText("Demo Osaka") || tokyoSelectable {
		t.Fatal("search did not filter node actions", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	if panel.query != "" || !tt.HasText("Demo Tokyo") {
		t.Fatal("Escape did not clear search")
	}
	click("Settings")
	if backend.Snapshot().Mode != "Rule" || len(appearances) != 0 || len(logins) != 0 {
		t.Fatal("navigation changed settings")
	}
	click("System")
	save("settings-select-light.png")
	tt.Key(0, ui.KeyEnd)
	tt.Key(0, ui.KeyEnter)
	if len(appearances) != 1 || appearances[0] != "Dark" {
		t.Fatal("appearance selection was not persisted exactly once", appearances)
	}
	// Demo startup stays disabled even when a preference callback is present.
	click("Open at login")
	if len(logins) != 0 {
		t.Fatal("demo changed login preference")
	}
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		suffix := "light"
		if dark {
			suffix = "dark"
		}
		save("settings-" + suffix + ".png")
		click("Dark")
		save("settings-menu-" + suffix + ".png")
		tt.Key(0, ui.KeyEscape)
		if len(appearances) != 1 {
			t.Fatal("dismissing a menu saved a preference")
		}
	}
	click("Back")
	// Both an opaque window and a native material host must receive the Radix
	// content background. Check actual rendered pixels, not theme construction.
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.SetVibrancy(true)
		tt.Frame()
		pixel := tt.Image().RGBAAt(0, 0)
		want := ui.Hex("#fcfcfd")
		if dark {
			want = ui.Hex("#111113")
		}
		if pixel.R != want.R || pixel.G != want.G || pixel.B != want.B || pixel.A != 255 {
			t.Fatalf("host material changed content palette: dark=%v pixel=%v", dark, pixel)
		}
	}
	// A live preference changes only through its named switch. Callbacks remain
	// local fixtures; this does not change the machine's login configuration.
	panel.actions.Demo = false
	tt.Frame()
	click("Settings")
	click("Open at login")
	click("Open at login")
	if len(logins) != 2 || !logins[0] || logins[1] {
		t.Fatal("login switch did not persist both explicit choices", logins)
	}
	click("Back")
	tt.Move(0, 0)
	tt.Frame()
	if tt.FramePending() {
		t.Fatal("resting panel scheduled an animation")
	}
	if dir := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR"); dir != "" {
		raw, err := json.MarshalIndent(map[string]any{
			"schema": 1, "passed": true, "routingPointerAndKeyboard": true,
			"groupKeyboardSelection": true, "escapeClearsSearch": true,
			"appearanceWrites": appearances, "demoLoginWrites": 0, "loginWrites": logins,
			"radixBackgroundWithHostMaterial": true, "restingAnimationFrames": false,
		}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "controls-view-e2e.json"), append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
