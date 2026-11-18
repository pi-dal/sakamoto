//go:build darwin

package menubar

import (
	"fmt"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// verifySettingsClick sends AppKit mouse and key events to each window's
// responder chain. WindowServer routing is outside this demo check.
func (a *application) verifySettingsClick() (map[string]bool, error) {
	receipt := map[string]bool{}
	lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil {
		return receipt, err
	}
	defer func() { _ = purego.Dlclose(lib) }()
	type point struct{ X, Y float64 }
	var mouseEvent func(objc.Class, objc.SEL, uint, point, uint, float64, int, objc.ID, int, int, float32) objc.ID
	var keyEvent func(objc.Class, objc.SEL, uint, point, uint, float64, int, objc.ID, objc.ID, objc.ID, bool, uint16) objc.ID
	var convertPoint func(objc.ID, objc.SEL, point, objc.ID) point
	purego.RegisterLibFunc(&mouseEvent, lib, "objc_msgSend")
	purego.RegisterLibFunc(&keyEvent, lib, "objc_msgSend")
	purego.RegisterLibFunc(&convertPoint, lib, "objc_msgSend")
	fixture := NewPanel(NewDemo(), Actions{Demo: true})
	tester := ui.NewTester(fixture.View, PanelWidth, PanelHeight)
	settings, _ := tester.Find("Settings")
	fixture.showPage("settings")
	tester.Frame()
	back, _ := tester.Find("Back")
	edit, _ := tester.Find("Edit configuration")

	for _, step := range []struct {
		name          string
		area          ui.Rect
		settingsHost  bool
		configuration bool
		replaceWindow bool
		shortcut      bool
	}{
		{name: "openSettingsWindow", area: settings, settingsHost: true, replaceWindow: true},
		{name: "editConfigurationInsideSettings", area: edit, settingsHost: true, configuration: true},
		{name: "backFromConfiguration", area: back, settingsHost: true},
		{name: "backToTray", area: back, replaceWindow: true},
		{name: "shortcutOpensSettings", settingsHost: true, replaceWindow: true, shortcut: true},
		{name: "backAfterShortcut", area: back, replaceWindow: true},
		{name: "reopenSettingsWindow", area: settings, settingsHost: true, replaceWindow: true},
	} {
		var previous *mygo.Window
		mygo.RunOnMain(func() { previous = a.window })
		if previous == nil || (!step.shortcut && step.area.W == 0) {
			return receipt, fmt.Errorf("missing window or control for %s", step.name)
		}
		if _, err := previous.CapturePage(); err != nil {
			return receipt, err
		}
		kinds := []uint{5, 1, 2} // Move, left button down, left button up.
		if step.shortcut {
			kinds = []uint{10, 11} // Key down, key up.
		}
		for _, kind := range kinds {
			mygo.RunOnMain(func() {
				if a.window != previous {
					return
				}
				win := objc.ID(previous.NativeHandle())
				if win == 0 {
					return
				}
				pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
				defer pool.Send(objc.RegisterName("drain"))
				surface := win.Send(objc.RegisterName("firstResponder"))
				if surface == 0 || surface.Send(objc.RegisterName("isKindOfClass:"), objc.GetClass("MyGoSurfaceView")) == 0 {
					return
				}
				number := int(win.Send(objc.RegisterName("windowNumber")))
				var event objc.ID
				if step.shortcut {
					str := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), ",")
					event = keyEvent(objc.GetClass("NSEvent"), objc.RegisterName("keyEventWithType:location:modifierFlags:timestamp:windowNumber:context:characters:charactersIgnoringModifiers:isARepeat:keyCode:"), kind, point{}, 1<<20, 0, number, 0, str, str, false, 0x2b)
				} else {
					// AppKit adds insets around a popover. Convert from the flipped content view.
					loc := convertPoint(surface, objc.RegisterName("convertPoint:toView:"), point{float64(step.area.X + step.area.W/2), float64(step.area.Y + step.area.H/2)}, 0)
					event = mouseEvent(objc.GetClass("NSEvent"), objc.RegisterName("mouseEventWithType:location:modifierFlags:timestamp:windowNumber:context:eventNumber:clickCount:pressure:"), kind, loc, 0, 0, number, 0, 0, 1, 1)
				}
				win.Send(objc.RegisterName("sendEvent:"), event)
				// Screen refresh may be suspended in a locked session.
				surface.Send(objc.RegisterName("updateLayer"))
			})
			time.Sleep(80 * time.Millisecond)
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			mygo.RunOnMain(func() {
				receipt[step.name] = a.window != nil && a.window.IsVisible() &&
					a.settingsHost == step.settingsHost && a.panel.configuration == step.configuration &&
					a.panel.settings == (step.settingsHost && !step.configuration) &&
					(previous != a.window) == step.replaceWindow
			})
			if receipt[step.name] {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		if !receipt[step.name] {
			return receipt, fmt.Errorf("AppKit navigation failed: %s", step.name)
		}
	}
	return receipt, nil
}
