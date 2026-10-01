package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
)

// setup installs only the existing root supervisor and user watcher. It never
// issues a connect/disconnect command or promises boot-time VPN auto-connect.
type setupPaths struct {
	configPath, runtimeDir, home, daemonDir, binary string
}

type setupState struct {
	installed bool
	label     string
	watcher   string
	status    string
}

func runSetup(path string, checkOnly bool) error {
	if runtime.GOOS != "darwin" {
		return errors.New("setup supports macOS only")
	}
	binary, err := setupBinary()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if filepath.Base(absolute) != "sakamoto.yaml" {
		return errors.New("setup expects a runtime sakamoto.yaml; use --config /absolute/path/sakamoto.yaml")
	}
	paths := setupPaths{absolute, filepath.Dir(absolute), home, "/Library/LaunchDaemons", binary}
	state, err := inspectSetup(paths)
	if err != nil {
		return err
	}
	if state.installed {
		if err := checkLoadedSetupServices(state); err != nil {
			return err
		}
		fmt.Printf("Services already loaded (%s); supervisor %s. No changes made.\n", state.label, state.status)
		fmt.Println("Launchd starts the services, not the TUN or protected DNS; reconnect manually after reboot.")
		return nil
	}
	script, err := setupScript(binary)
	if err != nil {
		return err
	}
	if err := checkSetupConfig(paths); err != nil {
		return err
	}
	fmt.Println("Setup preflight passed. This installs launchd services only; it never starts or reconnects a VPN.")
	fmt.Println("After reboot, manually reconnect the TUN and verify DNS protection again.")
	if checkOnly {
		return nil
	}
	if !stdinTerminal() {
		return errors.New("run setup interactively to approve installing the root LaunchDaemon")
	}
	cmd := exec.Command("/bin/bash", script)
	cmd.Env = append(os.Environ(), "SAKAMOTO_BIN="+binary, "SAKAMOTO_DIR="+paths.runtimeDir)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install macOS services: %w", err)
	}
	return nil
}

// Prefer the stable PATH symlink when it names the same executable; a Homebrew
// keg path would become stale on upgrade if copied into the launchd plist.
func setupBinary() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	actual, err := os.Stat(executable)
	if err != nil {
		return "", err
	}
	if candidate, err := exec.LookPath(os.Args[0]); err == nil {
		if candidate, err = filepath.Abs(candidate); err == nil {
			if info, err := os.Stat(candidate); err == nil && os.SameFile(actual, info) {
				return candidate, nil
			}
		}
	}
	return executable, nil
}

func setupScript(binary string) (string, error) {
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(resolved)
	for _, root := range []string{
		filepath.Join(filepath.Dir(parent), "share", "sakamoto"), // Homebrew keg.
		parent, // Repository root for a source build.
	} {
		script := filepath.Join(root, "scripts", "install-macos.sh")
		if file, err := os.Stat(script); err == nil && file.Mode().IsRegular() {
			if _, err := os.Stat(filepath.Join(root, "launchd", "dev.sakamoto.daemon.plist.in")); err == nil {
				if _, err := os.Stat(filepath.Join(root, "launchd", "dev.sakamoto.watch.plist.in")); err == nil {
					return script, nil
				}
			}
		}
	}
	return "", errors.New("setup resources not found beside the executable; use the installed Homebrew package or build in the repository root")
}

func inspectSetup(paths setupPaths) (setupState, error) {
	pairs := []struct{ daemon, watcher, label string }{
		{"dev.sakamoto.daemon.plist", "dev.sakamoto.watch.plist", "dev.sakamoto.daemon"},
		{"dev.pi-dal.sing-box.plist", "dev.pi-dal.sakamoto-watch.plist", "dev.pi-dal.sing-box"},
	}
	var installed *setupState
	for _, pair := range pairs {
		daemon, err := setupPlistExists(filepath.Join(paths.daemonDir, pair.daemon))
		if err != nil {
			return setupState{}, err
		}
		watcher, err := setupPlistExists(filepath.Join(paths.home, "Library", "LaunchAgents", pair.watcher))
		if err != nil {
			return setupState{}, err
		}
		if !daemon && !watcher {
			continue
		}
		if !daemon || !watcher || installed != nil {
			return setupState{}, errors.New("incomplete or duplicate launchd services; inspect them manually before setup")
		}
		installed = &setupState{installed: true, label: pair.label, watcher: strings.TrimSuffix(pair.watcher, ".plist")}
	}
	if installed == nil {
		if _, err := os.Lstat(filepath.Join(paths.runtimeDir, "svc.sock")); err == nil {
			return setupState{}, errors.New("supervisor socket exists without an installed service; inspect the legacy daemon before setup")
		} else if !errors.Is(err, os.ErrNotExist) {
			return setupState{}, err
		}
		return setupState{}, nil
	}
	status, err := setupSupervisorStatus(filepath.Join(paths.runtimeDir, "svc.sock"))
	if err != nil {
		return setupState{}, fmt.Errorf("services are installed but the supervisor is unavailable; refusing to reinstall: %w", err)
	}
	installed.status = status
	return *installed, nil
}

func checkLoadedSetupServices(state setupState) error {
	for _, service := range []string{
		"system/" + state.label,
		fmt.Sprintf("gui/%d/%s", os.Getuid(), state.watcher),
	} {
		out, err := exec.Command("/bin/launchctl", "print", service).CombinedOutput()
		if err != nil {
			return fmt.Errorf("service %s could not be inspected; do not reinstall it: %w", service, err)
		}
		if !strings.Contains(string(out), "state = running") {
			return fmt.Errorf("service %s is not running; inspect launchd rather than reinstalling", service)
		}
	}
	return nil
}

func setupPlistExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("launchd plist is not a regular file: %s", path)
	}
	return true, nil
}

func setupSupervisorStatus(path string) (string, error) {
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return "", err
	}
	if _, err := fmt.Fprintln(conn, "status"); err != nil {
		return "", err
	}
	line, err := bufio.NewReaderSize(conn, 512).ReadString('\n')
	if err != nil {
		return "", err
	}
	status := strings.TrimSpace(line)
	if status != "disconnected" && !strings.HasPrefix(status, "connected ") {
		return "", fmt.Errorf("unexpected supervisor status %q", status)
	}
	return status, nil
}

func checkSetupConfig(paths setupPaths) error {
	if _, err := os.Stat(paths.configPath); err != nil {
		return fmt.Errorf("prepare %s before setup: %w", paths.configPath, err)
	}
	core := filepath.Join(paths.runtimeDir, "config.json")
	if _, err := os.Stat(core); err != nil {
		return fmt.Errorf("generate %s before setup: %w", core, err)
	}
	cfg, err := config.Load(paths.configPath)
	if err != nil {
		return err
	}
	if err := config.ValidateAPISecret(cfg.API.Secret); err != nil {
		return err
	}
	if err := cfg.ValidateAPIEndpoint(); err != nil {
		return err
	}
	if err := cfg.ValidateDNSGuard(); err != nil {
		return err
	}
	check := exec.Command("sing-box", "check", "-c", core)
	if out, err := check.CombinedOutput(); err != nil {
		return fmt.Errorf("sing-box config check failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func stdinTerminal() bool {
	// The installer also requires an interactive stdin; this catches the common
	// headless case before attempting any privileged work.
	tty, err := os.Open("/dev/tty")
	if err != nil {
		return false
	}
	_ = tty.Close()
	return true
}
