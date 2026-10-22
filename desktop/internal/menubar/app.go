package menubar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/pi-dal/sakamoto/internal/desktop"
)

type AppOptions struct {
	ExternalCore    bool
	ConfigPath      string
	Demo            bool
	Show            bool
	VerifyDirectory string
}

type application struct {
	ctx       context.Context
	cancel    context.CancelFunc
	backend   Backend
	panel     *Panel
	window    *mygo.Window
	tray      *mygo.Tray
	menuState string
	prefsPath string
	opts      AppOptions
}

// Run starts a menu bar client. It never installs services or auto-connects.
func Run(opts AppOptions) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &application{ctx: ctx, cancel: cancel, opts: opts}
	name := "Sakamoto"
	if opts.Demo {
		name = "Sakamoto Demo"
	}
	if opts.VerifyDirectory != "" {
		name = "Sakamoto Verification"
	}
	mygo.App.SetName(name)
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	if !mygo.App.RequestSingleInstanceLock() {
		if opts.VerifyDirectory != "" {
			return errors.New("another verification instance is already running")
		}
		return nil
	}
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnSecondInstance(func([]string, string) { a.show() })
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		cancel()
		if a.tray != nil {
			a.tray.Destroy()
		}
	})
	mygo.App.WhenReady(func() {
		if err := a.ready(); err != nil {
			mygo.Dialog.Error("Sakamoto could not start", err.Error())
			mygo.App.Quit()
		}
	})
	return mygo.App.Run()
}

func (a *application) ready() error {
	if a.opts.Demo {
		fixture := NewDemo()
		if a.opts.ExternalCore {
			_ = fixture.update(func(s *desktop.Snapshot) { s.ExternalCore = true })
		}
		a.backend = fixture
	} else {
		controller := desktop.New(a.opts.ConfigPath, desktop.Options{ExternalCore: a.opts.ExternalCore})
		a.backend = controller
		go controller.Run(a.ctx)
	}
	appearance := "System"
	login := false
	if !a.opts.Demo {
		dir, err := mygo.App.Path(mygo.PathUserData)
		if err != nil {
			return err
		}
		a.prefsPath = filepath.Join(dir, "appearance.json")
		var stored struct {
			Appearance string `json:"appearance"`
		}
		if raw, err := os.ReadFile(a.prefsPath); err == nil {
			if json.Unmarshal(raw, &stored) == nil && validAppearance(stored.Appearance) {
				appearance = stored.Appearance
			}
		}
		login = mygo.App.OpenAtLogin()
	}
	mygo.Theme.SetSource(mygo.ThemeSource(strings.ToLower(appearance)))
	var tui func() error
	if runtime.GOOS == "darwin" {
		tui = a.openTUI
	}
	a.panel = NewPanel(a.backend, Actions{
		Dispatch: a.dispatchAction, OpenTUI: tui, RevealConfig: a.revealConfig,
		SetAppearance: a.setAppearance, SetLogin: mygo.App.SetOpenAtLogin, Quit: mygo.App.Quit,
		Appearance: appearance, OpenAtLogin: login, Demo: a.opts.Demo,
	})
	var err error
	a.tray, err = mygo.NewTray(mygo.TrayOptions{ID: "sakamoto-connection", Icon: trayIcon(), IconIsTemplate: runtime.GOOS == "darwin", ToolTip: "sakamoto — connection controls"})
	if err != nil {
		a.tray = nil
	}
	if a.tray != nil {
		if runtime.GOOS == "linux" {
			a.syncMenu()
		} else {
			a.tray.OnClick(a.toggle)
			a.tray.OnRightClick(a.contextMenu)
		}
	}
	go a.observe()
	if a.opts.Show || a.opts.VerifyDirectory != "" || a.tray == nil || runtime.GOOS == "linux" {
		// Let AppKit attach the status item to its menu bar window before
		// anchoring the first popover (normal clicks happen after this point).
		go func() {
			time.Sleep(250 * time.Millisecond)
			mygo.RunOnMain(a.show)
			if a.opts.VerifyDirectory != "" {
				a.verify()
			}
		}()
	}
	return nil
}

func (a *application) show() {
	if a.window != nil {
		a.window.Show()
		a.window.Focus()
		a.window.Invalidate()
		return
	}
	a.panel.Refresh()
	// MyGo list state belongs to one content engine. A dismissed popover
	// creates a new engine when reopened, so it needs fresh scroll state.
	a.panel.list = ui.ListState{}
	a.backend.SetVisible(true)
	if a.tray == nil {
		a.window = mygo.NewWindow(mygo.WindowOptions{Title: "Sakamoto", Width: PanelWidth, Height: PanelHeight, DisableResize: true, Content: ui.View(a.panel.View)})
	} else {
		a.window = mygo.NewFlyout(mygo.FlyoutOptions{
			Tray: a.tray, Placement: mygo.PlacementBottom, Width: PanelWidth, Height: PanelHeight,
			Focusable: true, Popover: runtime.GOOS == "darwin", Shadow: true, Content: ui.View(a.panel.View),
		})
	}
	a.window.OnClosed(func() {
		a.window = nil
		a.backend.SetVisible(false)
		if a.tray == nil && a.opts.VerifyDirectory == "" {
			mygo.App.Quit()
		}
	})
}
func (a *application) toggle() {
	if a.window != nil {
		a.window.Close()
	} else {
		a.show()
	}
}

func (a *application) observe() {
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-a.backend.Changes():
			mygo.RunOnMain(func() {
				if a.ctx.Err() != nil {
					return
				}
				a.panel.Refresh()
				s := a.backend.Snapshot()
				if a.tray != nil {
					status := string(s.Phase)
					if s.ExternalCore {
						status = "Core unavailable"
						if s.APIReady {
							status = "Core online"
						}
					}
					a.tray.SetToolTip("sakamoto — " + status)
					a.syncMenu()
				}
				if a.window != nil {
					a.window.Invalidate()
				}
			})
		}
	}
}
func (a *application) dispatchAction(action func(context.Context) error) {
	go func() {
		// Controller notices are safe projections; raw RPC errors can contain
		// private runtime information and are intentionally not logged here.
		_ = action(a.ctx)
		mygo.RunOnMain(func() {
			if a.ctx.Err() != nil {
				return
			}
			a.panel.Refresh()
			if a.window != nil {
				a.window.Invalidate()
			}
		})
	}()
}

func (a *application) syncMenu() {
	if runtime.GOOS != "linux" || a.tray == nil {
		return
	}
	s := a.backend.Snapshot()
	key := fmt.Sprint(s.Service, s.Busy, s.ConfigReady, s.Conflict, s.ExternalCore)
	if key != a.menuState {
		a.menuState = key
		a.tray.SetMenu(a.menu())
	}
}

func (a *application) menu() *mygo.Menu {
	s := a.backend.Snapshot()
	label := "Connect"
	action := a.backend.Connect
	if s.Service == "Running" {
		label = "Disconnect"
		action = a.backend.Disconnect
	}
	if s.ExternalCore {
		label = "Managed externally"
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Open Sakamoto", Click: func(*mygo.MenuItem, *mygo.Window) { a.show() }},
		{Label: label, Disabled: s.ExternalCore || s.Busy != "" || s.Service == "Unavailable" || (!s.ConfigReady && s.Service != "Running") || (s.Conflict && s.Service != "Running"), Click: func(*mygo.MenuItem, *mygo.Window) { a.dispatchAction(action) }},
		mygo.Separator(),
		{Label: "Settings…", Click: func(*mygo.MenuItem, *mygo.Window) { a.panel.settings = true; a.show() }},
		{Role: mygo.RoleQuit, Label: "Quit Sakamoto"},
	})
}

func (a *application) contextMenu() {
	if a.window != nil {
		a.window.Close()
	}
	menu := a.menu()
	a.tray.SetMenu(menu)
	defer a.tray.SetMenu(nil)
	a.tray.PopUpMenu()
}

func validAppearance(value string) bool {
	return value == "System" || value == "Light" || value == "Dark"
}
func (a *application) setAppearance(value string) error {
	if !validAppearance(value) {
		return errors.New("unknown appearance")
	}
	if !a.opts.Demo {
		raw, err := json.Marshal(struct {
			Appearance string `json:"appearance"`
		}{value})
		if err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(a.prefsPath), ".appearance-*")
		if err != nil {
			return err
		}
		defer func() { _ = os.Remove(tmp.Name()) }()
		if _, err := tmp.Write(raw); err != nil {
			_ = tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		if err := os.Rename(tmp.Name(), a.prefsPath); err != nil {
			return err
		}
	}
	mygo.Theme.SetSource(mygo.ThemeSource(strings.ToLower(value)))
	return nil
}

func (a *application) revealConfig() error {
	if a.opts.Demo {
		return nil
	}
	return mygo.Shell.OpenPath(filepath.Dir(a.opts.ConfigPath))
}
