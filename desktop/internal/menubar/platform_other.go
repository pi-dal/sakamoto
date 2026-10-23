//go:build windows || linux

package menubar

import "errors"

// No native CLI bundle exists on these platforms yet. The shared view omits
// this action and offers the configuration directory instead.
func (a *application) openTUI() error { return errors.New("the TUI is not bundled on this platform") }

func (a *application) openRecovery() error {
	return errors.New("the external core service owns recovery")
}

func (a *application) updateService() error {
	return errors.New("the external service manages its own updates")
}
func (a *application) verify() { a.verifyContent() }
