//go:build darwin

package menubar

import (
	"errors"
	"fmt"
	"github.com/egoist/mygo"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func (a *application) openTUI() error       { return a.openTerminal() }
func (a *application) openRecovery() error  { return a.openTerminal("repair") }
func (a *application) updateService() error { return a.openTerminal("service-update", "--reconnect") }

func (a *application) openTerminal(arguments ...string) error {
	if a.opts.Demo {
		return nil
	}
	resources, _ := mygo.App.Path(mygo.PathResources)
	candidates := []string{filepath.Join(resources, "cli", "sakamoto"), "/opt/homebrew/bin/sakamoto", "/usr/local/bin/sakamoto"}
	if found, err := exec.LookPath("sakamoto"); err == nil {
		candidates = append(candidates, found)
	}
	binary := ""
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			binary = candidate
			break
		}
	}
	if binary == "" {
		return errors.New("install the sakamoto CLI or use a packaged app")
	}
	// Write a private command file so Terminal receives arguments as shell-
	// quoted literals. Neither paths nor config values enter an AppleScript.
	file, err := os.CreateTemp("", "sakamoto-tui-*.command")
	if err != nil {
		return err
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	command := quote(binary) + " --config " + quote(a.opts.ConfigPath)
	for _, argument := range arguments {
		command += " " + quote(argument)
	}
	body := fmt.Sprintf("#!/bin/sh\nrm -f -- %s\nexec %s\n", quote(file.Name()), command)
	if _, err := file.WriteString(body); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return err
	}
	if err := os.Chmod(file.Name(), 0o700); err != nil {
		_ = os.Remove(file.Name())
		return err
	}
	if err := exec.Command("/usr/bin/open", "-a", "Terminal", file.Name()).Run(); err != nil {
		_ = os.Remove(file.Name())
		return err
	}
	return nil
}
