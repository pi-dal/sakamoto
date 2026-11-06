package menubar

import (
	"context"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/pi-dal/sakamoto/internal/desktop"
)

func TestPolicyApplyWorkflow(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "current"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			backend := &repairViewFixture{Demo: NewDemo()}
			_ = backend.update(func(s *desktop.Snapshot) { s.Health.UpgradeRequired = legacy })
			updates := 0
			var panel *Panel
			panel = NewPanel(backend, Actions{UpdateService: func() error { updates++; return nil }, Dispatch: func(action func(context.Context) error) {
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
			click("Settings")
			click("Routing policy")
			click("Add rule")
			click("Policy match")
			tt.Type("*.mistral.ai")
			click("Save rule")
			if backend.regenerations != 0 || updates != 0 {
				t.Fatal("saving applied configuration")
			}
			if !tt.HasText("Saved changes need to be applied to the VPN.") {
				t.Fatal("missing activation explanation", tt.Texts())
			}
			capture := func(stage string) {
				t.Helper()
				for _, dark := range []bool{false, true} {
					tt.SetDark(dark)
					tt.Frame()
					if dir := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR"); dir != "" {
						if err := os.MkdirAll(dir, 0755); err != nil {
							t.Fatal(err)
						}
						theme := "light"
						if dark {
							theme = "dark"
						}
						file, err := os.Create(filepath.Join(dir, "policy-apply-"+name+"-"+stage+"-"+theme+".png"))
						if err != nil {
							t.Fatal(err)
						}
						encodeErr := png.Encode(file, tt.Image())
						closeErr := file.Close()
						if encodeErr != nil || closeErr != nil {
							t.Fatal(encodeErr, closeErr)
						}
					}
				}
				tt.SetDark(false)
			}
			capture("saved")
			if legacy {
				click("Update service and apply…")
				if updates != 1 || backend.regenerations != 0 {
					t.Fatal("legacy service bypassed upgrade")
				}
			} else {
				click("Apply saved configuration…")
				if backend.regenerations != 0 {
					t.Fatal("apply bypassed interruption confirmation")
				}
				capture("confirm")
				click("Cancel apply")
				if backend.regenerations != 0 {
					t.Fatal("cancel applied configuration")
				}
				click("Apply saved configuration…")
				click("Regenerate and reconnect")
				if backend.regenerations != 1 {
					t.Fatal("confirmed apply missing")
				}
			}
		})
	}
	if dir := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR"); dir != "" {
		raw, _ := json.MarshalIndent(map[string]any{"passed": true, "save_does_not_apply": true, "apply_requires_confirmation": true, "cancel_does_not_apply": true, "legacy_uses_upgrade_action": true, "uses_owner_runtime": false}, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "policy-apply-e2e.json"), append(raw, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
