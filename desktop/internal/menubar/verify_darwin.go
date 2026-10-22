//go:build darwin

package menubar

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
)

// verify captures the actual packaged NSPopover, using only the demo backend.
// View event sequences are covered separately by the panel workflow test.
func (a *application) verify() {
	receipt := map[string]any{"schema": 1, "passed": false, "demo": true, "usesRealTUN": false, "arch": runtime.GOARCH, "goVersion": runtime.Version(), "captures": []string{}}
	if err := os.MkdirAll(a.opts.VerifyDirectory, 0o755); err != nil {
		mygo.App.Quit()
		return
	}
	defer func() {
		raw, err := json.MarshalIndent(receipt, "", "  ")
		if err == nil {
			_ = os.WriteFile(filepath.Join(a.opts.VerifyDirectory, "native-app.json"), append(raw, '\n'), 0o644)
		}
		mygo.App.Quit()
	}()
	for _, capture := range []struct {
		name     string
		theme    mygo.ThemeSource
		settings bool
	}{
		{"macos-light.png", mygo.ThemeLight, false},
		{"macos-dark.png", mygo.ThemeDark, false},
		{"macos-settings.png", mygo.ThemeLight, true},
	} {
		mygo.RunOnMain(func() {
			if a.window != nil {
				a.window.Close()
			}
			mygo.Theme.SetSource(capture.theme)
			a.panel.settings = capture.settings
			a.show()
		})
		time.Sleep(400 * time.Millisecond)
		var number uintptr
		mygo.RunOnMain(func() {
			receipt["trayBounds"] = a.tray.Bounds()
			receipt["windowExists"] = a.window != nil
			if a.window != nil {
				receipt["windowVisible"] = a.window.IsVisible()
				receipt["nativeHandleExists"] = a.window.NativeHandle() != 0
				number = uintptr(objc.ID(a.window.NativeHandle()).Send(objc.RegisterName("windowNumber")))
			}
		})
		if number == 0 {
			receipt["error"] = "native popover did not produce a window"
			return
		}
		target := filepath.Join(a.opts.VerifyDirectory, capture.name)
		if output, err := exec.Command("/usr/sbin/screencapture", "-x", "-o", "-l", strconv.FormatUint(uint64(number), 10), target).CombinedOutput(); err != nil {
			// Screen capture can be unavailable in a locked/remote session.
			// Preserve its failure and clearly identify a content-only snapshot.
			receipt["systemCaptureError"] = string(output)
			receipt["captureMethod"] = "mygo-content-snapshot; composited over theme background; excludes system popover material"
			var window *mygo.Window
			mygo.RunOnMain(func() { window = a.window })
			raw, snapshotErr := window.CapturePage()
			if snapshotErr == nil {
				raw, snapshotErr = readableSnapshot(raw, capture.theme == mygo.ThemeDark)
			}
			if snapshotErr != nil {
				receipt["error"] = fmt.Sprintf("content capture failed: %v", snapshotErr)
				return
			}
			if err := os.WriteFile(target, raw, 0o644); err != nil {
				receipt["error"] = "could not save content snapshot"
				return
			}
		} else if receipt["captureMethod"] == nil {
			receipt["captureMethod"] = "macos-window-capture"
		}
		receipt["captures"] = append(receipt["captures"].([]string), capture.name)
	}
	mygo.RunOnMain(func() { a.window.Close() })
	time.Sleep(150 * time.Millisecond)
	// This is a short demo idle sample, not a live-runtime benchmark.
	var before, after syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &before); err == nil {
		started := time.Now()
		time.Sleep(2 * time.Second)
		if err := syscall.Getrusage(syscall.RUSAGE_SELF, &after); err == nil {
			cpu := after.Utime.Nano() + after.Stime.Nano() - before.Utime.Nano() - before.Stime.Nano()
			receipt["hiddenDemoCPUPercent"] = 100 * float64(cpu) / float64(time.Since(started))
			receipt["processPeakRSSBytes"] = after.Maxrss
			receipt["hiddenSampleSeconds"] = time.Since(started).Seconds()
		}
	}
	var hidden bool
	mygo.RunOnMain(func() { hidden = a.window == nil; a.panel.settings = false; a.show() })
	time.Sleep(200 * time.Millisecond)
	var reopened bool
	mygo.RunOnMain(func() { reopened = a.window != nil && a.window.IsVisible() })
	receipt["closeKeepsAppRunning"] = hidden
	receipt["panelReopens"] = reopened
	receipt["passed"] = hidden && reopened
}
