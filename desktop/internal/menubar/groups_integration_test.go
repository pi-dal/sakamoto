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

func TestAutomaticGroupsPanelEndToEnd(t *testing.T) {
	backend := &repairViewFixture{Demo: NewDemo()}
	var panel *Panel
	panel = NewPanel(backend, Actions{Dispatch: func(action func(context.Context) error) {
		if err := action(context.Background()); err != nil {
			t.Error(err)
		}
		panel.Refresh()
	}})
	tt := ui.NewTester(panel.View, PanelWidth, PanelHeight)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(label, err, tt.Texts())
		}
	}
	capture := func(stage string) {
		t.Helper()
		if dir := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR"); dir != "" {
			for _, dark := range []bool{false, true} {
				tt.SetDark(dark)
				tt.Frame()
				name := "light"
				if dark {
					name = "dark"
				}
				f, err := os.Create(filepath.Join(dir, "automatic-groups-"+stage+"-"+name+".png"))
				if err != nil {
					t.Fatal(err)
				}
				err = png.Encode(f, tt.Image())
				closeErr := f.Close()
				if err != nil || closeErr != nil {
					t.Fatal(err, closeErr)
				}
			}
			tt.SetDark(false)
		}
	}
	click("Settings")
	click("Proxy groups")
	if !tt.HasText("RealityAuto") || !tt.HasText("OthersAuto") {
		t.Fatal("group list missing", tt.Texts())
	}
	capture("list")
	click("Edit group RealityAuto")
	click("Include Demo Tokyo")
	if !tt.HasText("1 included, 2 excluded") {
		t.Fatal("draft counts missing", tt.Texts())
	}
	capture("editor")
	click("Cancel edit")
	groups, err := backend.AutomaticGroups()
	if err != nil {
		t.Fatal(err)
	}
	if groups.Groups[0].Nodes[0].Excluded {
		t.Fatal("cancel saved changes")
	}
	click("Edit group RealityAuto")
	click("Include Demo Tokyo")
	click("Save group")
	groups, err = backend.AutomaticGroups()
	if err != nil {
		t.Fatal(err)
	}
	if !groups.Groups[0].Nodes[0].Excluded {
		t.Fatal("exclusion not saved")
	}
	if backend.regenerations != 0 {
		t.Fatal("saving applied configuration")
	}
	click("Edit group RealityAuto")
	click("Include Demo Osaka")
	if !tt.HasText("Keep at least one available node included.") {
		t.Fatal("empty group warning missing", tt.Texts())
	}
	capture("empty-validation")
	click("Cancel edit")
	click("Edit group RealityAuto")
	click("Search group nodes")
	tt.Type("missing node")
	if !tt.HasText("No matching nodes") {
		t.Fatal("empty search missing", tt.Texts())
	}
	click("Cancel edit")
	click("Edit group RealityAuto")
	if !tt.HasText("Currently absent") {
		t.Fatal("absent exclusion missing", tt.Texts())
	}
	click("Include Demo Tokyo")
	click("Save group")
	groups, err = backend.AutomaticGroups()
	if err != nil {
		t.Fatal(err)
	}
	if groups.Groups[0].Nodes[0].Excluded {
		t.Fatal("reinclude not saved")
	}
	click("Apply saved configuration…")
	if backend.regenerations != 0 {
		t.Fatal("apply skipped confirmation")
	}
	click("Cancel apply")
	click("Apply saved configuration…")
	click("Regenerate and reconnect")
	if backend.regenerations != 1 {
		t.Fatal("explicit apply missing")
	}
	click("Back")
	click("Edit configuration")
	click("Proxy groups")
	if !tt.HasText("RealityAuto") {
		t.Fatal("configuration link missing")
	}
	if dir := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR"); dir != "" {
		raw, _ := json.MarshalIndent(map[string]any{"passed": true, "cancelPreservesSavedPolicy": true, "excludeAndReinclude": true, "emptyGroupGuard": true, "search": true, "absentExclusionVisible": true, "saveDoesNotApply": true, "confirmedApply": true, "settingsAndConfigurationEntrypoints": true}, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "automatic-groups-view-e2e.json"), append(raw, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
