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

func TestManagementPanelEndToEnd(t *testing.T) {
	backend := NewDemo()
	var panel *Panel
	panel = NewPanel(backend, Actions{Demo: true, Dispatch: func(action func(context.Context) error) {
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
	capture := func(name string) {
		t.Helper()
		if dir := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR"); dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			f, err := os.Create(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if err = png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
			if err = f.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	click("Settings")
	click("Learning")
	if !tt.HasText("Recent checks") {
		t.Fatal("learning history missing")
	}
	click("Select failed.example")
	if !tt.HasText("1 selected") {
		t.Fatal("selection feedback missing")
	}
	capture("learning-selected.png")
	click("Add selected to Config")
	if !tt.HasText("0 selected") {
		t.Fatal("selection was not cleared after saving")
	}
	m, err := backend.Management()
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Policies) != 2 {
		t.Fatal("selected failure not persisted", m.Policies)
	}
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		name := "learning-light.png"
		if dark {
			name = "learning-dark.png"
		}
		capture(name)
	}
	tt.SetDark(false)
	click("Back")
	click("Nodes & sources")
	click("Add node")
	click("Node share link")
	tt.Type("socks://fixture:secret@127.0.0.1:1080#New")
	click("Save node")
	if !tt.HasText("New") {
		t.Fatal("node save did not return to list", tt.Texts())
	}
	click("Edit node New")
	if tt.HasText("Configuration saved. Regenerate to apply.") {
		t.Fatal("stale success notice in unsaved editor")
	}
	modifier := ui.Ctrl
	if runtime.GOOS == "darwin" {
		modifier = ui.Cmd
	}
	replace := func(label, value string) {
		t.Helper()
		click(label)
		tt.Key(modifier, ui.KeyA)
		tt.Type(value)
	}
	replace("Node remark", "Edited")
	replace("Node server", "::1")
	replace("Node port", "0")
	click("Save node")
	if !tt.HasText("Port must be 1–65535.") {
		t.Fatal("invalid port did not produce actionable error")
	}
	current, err := backend.Management()
	if err != nil {
		t.Fatal(err)
	}
	if current.Nodes[len(current.Nodes)-1].Tag != "New" {
		t.Fatal("invalid edit changed saved node")
	}
	replace("Node port", "1081")
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		name := "node-editor-validation-light.png"
		if dark {
			name = "node-editor-validation-dark.png"
		}
		capture(name)
	}
	tt.SetDark(false)
	click("Save node")
	if !tt.HasText("Edited") {
		t.Fatal("node field edit not saved", tt.Texts())
	}
	click("Edit node Edited")
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		name := "node-editor-light.png"
		if dark {
			name = "node-editor-dark.png"
		}
		capture(name)
	}
	tt.SetDark(false)
	replace("Node remark", "Canceled")
	click("Cancel edit")
	if tt.HasText("Canceled") {
		t.Fatal("cancel persisted draft")
	}
	click("Add subscription")
	click("Subscription name")
	tt.Type("Fixture")
	click("Subscription URL")
	tt.Type("https://example.org/sub")
	click("Save subscription")
	if !tt.HasText("Fixture") {
		t.Fatal("subscription missing")
	}
	click("Edit subscription Fixture")
	replace("Subscription name", "Updated subscription")
	click("Save subscription")
	if !tt.HasText("Updated subscription") {
		t.Fatal("subscription edit missing")
	}
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		name := "sources-light.png"
		if dark {
			name = "sources-dark.png"
		}
		capture(name)
	}
	tt.SetDark(false)
	click("Back")
	click("Routing policy")
	click("Add rule")
	click("Policy match")
	tt.Type("new.example")
	click("Save rule")
	if !tt.HasText("new.example") {
		t.Fatal("policy save missing")
	}
	click("Edit rule new.example")
	replace("Policy match", "edited.example")
	click("Save rule")
	if !tt.HasText("edited.example") {
		t.Fatal("policy edit missing")
	}
	click("Edit rule edited.example")
	click("Remove")
	if tt.HasText("edited.example") {
		t.Fatal("policy deletion missing")
	}
	click("Search saved sources")
	tt.Type("nothing matches")
	if !tt.HasText("No matching rules") {
		t.Fatal("empty search missing")
	}
	if dir := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR"); dir != "" {
		raw, _ := json.MarshalIndent(map[string]any{"passed": true, "learningSelection": true, "nodeAddAndEdit": true, "invalidPortRejected": true, "cancelPreservesSource": true, "subscriptionAddAndEdit": true, "policyAddEditRemove": true, "search": true, "usesRealTUN": false}, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "management-view-e2e.json"), raw, 0644); err != nil {
			t.Fatal(err)
		}
	}
}
