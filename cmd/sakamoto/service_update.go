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
	if flags.NArg() != 0 || modes > 1 {
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
	fmt.Println("Update this runtime's background service and retain its previous executable outside Homebrew.")
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
	stageCommand := exec.CommandContext(ctx, "/usr/bin/sudo", "-n", binary, "--config", absolute, "service-update", "--stage-rescue")
	stageCommand.Stdin, stageCommand.Stdout, stageCommand.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err = stageCommand.Run(); err != nil {
		return fmt.Errorf("independent rescue could not be prepared; service update canceled: %w", err)
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
	command := exec.CommandContext(ctx, "/usr/bin/sudo", "-n", binary, "--config", absolute, "service-update", "--apply")
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err = command.Run(); err != nil {
		return fmt.Errorf("service update incomplete; inspect the retained service and maintenance state: %w", err)
	}
	if recovery.Held(paths.Runtime) {
		return errors.New("service updated; maintenance remains held, resume deliberately before connecting")
	}
	if live {
		result, e := svc.RepairAt(ctx, absolute, svc.RepairOptions{})
		if e != nil {
			return fmt.Errorf("background service updated; core recovery is incomplete (%s): %w", result.Problem, e)
		}
		fmt.Println("Background service updated. Core API and protected DNS verified.")
	} else {
		fmt.Println("Background service updated. The VPN remains disconnected.")
	}
	return nil
}
