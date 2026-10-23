package menubar

import (
	"context"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/pi-dal/sakamoto/internal/desktop"
)

type repairViewFixture struct {
	*Demo
	checks, repairs, restarts, recoveries int
}

func (f *repairViewFixture) CheckHealth(ctx context.Context) error {
	f.checks++
	return f.Demo.CheckHealth(ctx)
}
func (f *repairViewFixture) Repair(ctx context.Context, restart bool) error {
	if restart {
		f.restarts++
	} else {
		f.repairs++
	}
	return f.Demo.Repair(ctx, restart)
}
func (f *repairViewFixture) RecoverRoute(ctx context.Context) error {
	f.recoveries++
	return f.Demo.RecoverRoute(ctx)
}

func TestPanelRepairEndToEnd(t *testing.T) {
	backend := &repairViewFixture{Demo: NewDemo()}
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
	click("Check & repair")
	if backend.checks+backend.repairs+backend.restarts+backend.recoveries != 0 {
		t.Fatal("opening repair page issued an action")
	}
	click("Check health")
	if backend.checks != 1 || !panel.snapshot.Health.Verified {
		t.Fatal("check did not reach backend")
	}
	click("Repair core")
	if backend.repairs != 1 {
		t.Fatal("repair did not reach backend")
	}
	click("Reconnect core…")
	if backend.restarts != 0 || !tt.HasText("Active connections will be interrupted.") {
		t.Fatal("reconnect was not confirmed")
	}
	click("Cancel reconnect")
	if backend.restarts != 0 {
		t.Fatal("cancel issued a reconnect")
	}
	click("Reconnect core…")
	click("Reconnect now")
	if backend.restarts != 1 {
		t.Fatal("confirmed reconnect missing")
	}
	click("Test automatic route")
	if backend.recoveries != 1 || !tt.HasText("Route tests requested. Recovery is not yet verified.") {
		t.Fatal("queued tests were presented as recovered", tt.Texts())
	}
	dir := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR")
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	save := func(name string, dark bool) {
		t.Helper()
		tt.SetDark(dark)
		tt.Frame()
		if dir == "" {
			return
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
	save("repair-light.png", false)
	save("repair-dark.png", true)
	click("Reconnect core…")
	save("repair-confirm-light.png", false)
	save("repair-confirm-dark.png", true)
	click("Cancel reconnect")
	_ = backend.update(func(s *desktop.Snapshot) {
		s.Health = desktop.HealthReport{State: "unavailable", CheckedAt: time.Now(), Summary: "Supervisor unavailable. Open service recovery to restore the background service."}
	})
	panel.Refresh()
	tt.Frame()
	if !tt.HasText("Open service recovery") {
		t.Fatal("service recovery fallback is missing")
	}
	save("repair-service-unavailable.png", false)
	if dir != "" {
		raw, _ := json.MarshalIndent(map[string]any{"schema": 1, "passed": true, "checkActions": backend.checks, "repairActions": backend.repairs, "confirmedRestarts": backend.restarts, "queuedRecoveryAcknowledged": true, "cancelPreservesConnection": true, "serviceRecoveryFallbackVisible": true}, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "repair-view-e2e.json"), append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	click("Back")
	if !tt.HasText("Search nodes") {
		t.Fatal("cannot return to connection panel")
	}
}
