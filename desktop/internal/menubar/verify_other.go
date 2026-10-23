//go:build windows || linux

package menubar

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/egoist/mygo"
)

func (a *application) verifyContent() {
	receipt := map[string]any{"schema": 1, "passed": false, "platform": runtime.GOOS, "arch": runtime.GOARCH, "demo": true, "usesRealTUN": false, "captureMethod": "mygo-content-snapshot"}
	defer func() {
		raw, err := json.MarshalIndent(receipt, "", "  ")
		if err == nil {
			_ = os.WriteFile(filepath.Join(a.opts.VerifyDirectory, "native-app.json"), append(raw, '\n'), 0o644)
		}
		mygo.App.Quit()
	}()
	for _, shot := range []struct {
		name     string
		theme    mygo.ThemeSource
		settings bool
		recovery bool
	}{{"desktop-light.png", mygo.ThemeLight, false, false}, {"desktop-dark.png", mygo.ThemeDark, false, false}, {"desktop-settings.png", mygo.ThemeLight, true, false}, {"desktop-repair-light.png", mygo.ThemeLight, false, true}, {"desktop-repair-dark.png", mygo.ThemeDark, false, true}} {
		var window *mygo.Window
		mygo.RunOnMain(func() {
			if a.window != nil {
				a.window.Close()
			}
			mygo.Theme.SetSource(shot.theme)
			a.panel.settings = shot.settings
			a.panel.recovery = shot.recovery
			a.show()
			window = a.window
		})
		time.Sleep(300 * time.Millisecond)
		if window == nil || window.NativeHandle() == 0 {
			receipt["error"] = "native window was not created"
			return
		}
		raw, err := window.CapturePage()
		if err == nil {
			raw, err = readableSnapshot(raw, shot.theme == mygo.ThemeDark)
		}
		if err != nil {
			receipt["error"] = "content capture failed"
			return
		}
		if err := os.WriteFile(filepath.Join(a.opts.VerifyDirectory, shot.name), raw, 0o644); err != nil {
			receipt["error"] = "capture could not be saved"
			return
		}
	}
	var survived, reopened bool
	mygo.RunOnMain(func() {
		if a.window != nil {
			a.window.Close()
		}
		survived = a.window == nil
		a.panel.settings = false
		a.panel.recovery = false
		a.show()
		reopened = a.window != nil
	})
	receipt["closeKeepsAppRunning"] = survived
	receipt["panelReopens"] = reopened
	receipt["passed"] = survived && reopened
}
