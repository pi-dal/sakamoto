package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pi-dal/sakamoto/internal/recovery"
	"github.com/pi-dal/sakamoto/internal/rescue"
	"github.com/pi-dal/sakamoto/internal/svc"
)

func runServiceUpdate(ctx context.Context, path string, args []string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("service-update supports macOS only")
	}
	flags := flag.NewFlagSet("service-update", flag.ContinueOnError)
	check := flags.Bool("check", false, "inspect the installed service without changes")
	reconnect := flags.Bool("reconnect", false, "reconnect a live VPN after the service update")
	regenerate := flags.Bool("regenerate", false, "apply saved configuration after the approved service update")
	validate := flags.Bool("validate-config", false, "validate the configuration before an approved live reconnect")
	stage := flags.Bool("stage-rescue", false, "stage independent rescue as administrator")
	apply := flags.Bool("apply", false, "apply the approved update as administrator")
	if err := flags.Parse(args); err != nil {
		return err
	}
	modes := 0
	for _, enabled := range []bool{*check, *reconnect, *apply, *stage} {
		if enabled {
			modes++
		}
	}
	if flags.NArg() != 0 || modes > 1 || (*validate && !*stage) || (*regenerate && !*reconnect) {
		return errors.New("choose a single service-update mode")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if filepath.Base(absolute) != "sakamoto.yaml" {
		return errors.New("service update expects the runtime sakamoto.yaml")
	}
	binary, err := setupBinary()
	if err != nil {
		return err
	}
	paths := rescue.ServiceUpdatePaths{Runtime: filepath.Dir(absolute), Plist: "/Library/LaunchDaemons/dev.sakamoto.daemon.plist", Install: "/Library/Application Support/Sakamoto", Source: binary}
	sys, err := rescue.NativeSystem(paths.Runtime)
	if err != nil {
		return err
	}
	if err = rescue.CheckServiceUpdate(ctx, paths, sys); err != nil {
		return err
	}
	if *check {
		fmt.Println("Service update preflight passed. The installed runtime and launchd job match; no changes made.")
		return nil
	}
	script, err := setupScript(binary)
	if err != nil {
		return err
	}
	paths.RescueSource = filepath.Join(filepath.Dir(script), "sakamoto-rescue")
	if *stage {
		if err = rescue.StageServiceRescue(paths, sys); err != nil {
			return err
		}
		if *validate {
			if err = svc.ValidateConfigurationForUpdate(ctx, absolute); err != nil {
				return fmt.Errorf("current configuration cannot be safely reconnected; live core retained: %w", err)
			}
		}
		fmt.Println("Independent recovery helper is ready.")
		return nil
	}
	if *apply {
		ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		result, updateErr := rescue.UpdateService(ctx, paths, sys)
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err = encoder.Encode(result); err != nil {
			return err
		}
		return updateErr
	}
	if !stdinTerminal() {
		return errors.New("run service-update in a terminal for local administrator authorization")
	}
	socket := filepath.Join(paths.Runtime, svc.SockName)
	status, err := svc.SendToContext(ctx, socket, "status")
	if err != nil {
		return err
	}
	live := strings.HasPrefix(status, "connected")
	if live && !*reconnect {
		return errors.New("the core is running; use service-update --reconnect to approve its controlled reconnect")
	}
	pid := 0
	if live {
		for _, field := range strings.Fields(status) {
			if strings.HasPrefix(field, "pid=") {
				pid, _ = strconv.Atoi(strings.TrimPrefix(field, "pid="))
			}
		}
		if pid <= 0 {
			return errors.New("cannot confirm the old core identity; service update canceled")
		}
	}
	account, err := user.LookupId(strconv.Itoa(sys.UID))
	if err != nil || os.Geteuid() != sys.UID {
		return errors.New("run the update as the runtime owner; administrator authorization is requested separately")
	}
	watcher := rescue.WatcherUpdatePaths{Runtime: paths.Runtime, Plist: filepath.Join(account.HomeDir, "Library", "LaunchAgents", "dev.sakamoto.watch.plist"), Source: binary, Install: filepath.Join(account.HomeDir, ".local", "share", "sakamoto", "services"), Version: version}
	if err = rescue.CheckWatcherUpdate(ctx, watcher, sys); err != nil {
		return fmt.Errorf("watcher upgrade preflight failed; services unchanged: %w", err)
	}
	fmt.Println("Update this runtime's background supervisor and watcher; retain executables outside Homebrew and the app.")
	if *regenerate {
		fmt.Println("Saved configuration will be regenerated and applied after the service update.")
		if !live {
			fmt.Println("The VPN will connect after configuration validation succeeds.")
		}
	}
	if live {
		fmt.Println("The active VPN will disconnect and reconnect. Native API and protected DNS will be checked.")
	}
	fmt.Print("Continue? [y/N] ")
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(answer), "y") {
		return nil
	}
	// Authenticate before touching connectivity; no password passes through the app.
	authorize := exec.CommandContext(ctx, "/usr/bin/sudo", "-v")
	authorize.Stdin, authorize.Stdout, authorize.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err = authorize.Run(); err != nil {
		return err
	}
	stageArgs := []string{"-n", binary, "--config", absolute, "service-update", "--stage-rescue"}
	if live {
		stageArgs = append(stageArgs, "--validate-config")
	}
	stageCommand := exec.CommandContext(ctx, "/usr/bin/sudo", stageArgs...)
	stageCommand.Stdin, stageCommand.Stdout, stageCommand.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err = stageCommand.Run(); err != nil {
		return fmt.Errorf("service update preparation failed; live core was not disconnected: %w", err)
	}
	if live {
		reply, e := svc.SendToContext(ctx, socket, "disconnect")
		if e != nil {
			return e
		}
		if !strings.HasPrefix(reply, "disconnected") {
			return errors.New("supervisor could not safely disconnect; service update canceled")
		}
		waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		for syscall.Kill(pid, 0) != syscall.ESRCH {
			select {
			case <-waitCtx.Done():
				cancel()
				return errors.New("old core has not exited; service update canceled")
			case <-time.After(100 * time.Millisecond):
			}
		}
		cancel()
	}
	// The existing watcher owns HTTP/HTTPS restoration. Wait for its verified
	// receipt removal before unloading the supervisor or replacing the plist.
	restoreCtx, cancelRestore := context.WithTimeout(ctx, 30*time.Second)
	defer cancelRestore()
	for {
		pending := false
		for _, name := range []string{"dns-restore.json", "proxy-restore.json"} {
			if _, e := os.Lstat(filepath.Join(paths.Runtime, name)); !errors.Is(e, os.ErrNotExist) {
				pending = true
			}
		}
		if !pending {
			break
		}
		select {
		case <-restoreCtx.Done():
			return errors.New("network settings have not finished restoring; service update canceled")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err = rescue.UpdateWatcher(ctx, watcher, sys); err != nil {
		return fmt.Errorf("watcher update failed; supervisor was not replaced and VPN remains disconnected: %w", err)
	}
	command := exec.CommandContext(ctx, "/usr/bin/sudo", "-n", binary, "--config", absolute, "service-update", "--apply")
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err = command.Run(); err != nil {
		return fmt.Errorf("service update incomplete; inspect the retained service and maintenance state: %w", err)
	}
	if recovery.Held(paths.Runtime) {
		return errors.New("service updated; maintenance remains held, resume deliberately before connecting")
	}
	if live || *regenerate {
		result, e := svc.RepairAt(ctx, absolute, svc.RepairOptions{Regenerate: *regenerate, Restart: *regenerate})
		if e != nil {
			return fmt.Errorf("background service updated; core recovery is incomplete (%s): %w", result.Problem, e)
		}
		if *regenerate && result.Action != "regenerated" {
			return errors.New("services updated, but saved configuration was not applied; inspect repair status before retrying")
		}
		fmt.Println("Background supervisor and watcher updated. Core API and protected DNS verified.")
		if *regenerate {
			fmt.Println("Saved configuration regenerated and applied.")
		}
	} else {
		fmt.Println("Background supervisor and watcher updated. The VPN remains disconnected; apply saved configuration when connecting.")
	}
	return nil
}
