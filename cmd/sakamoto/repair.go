package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/pi-dal/sakamoto/internal/rescue"
	"github.com/pi-dal/sakamoto/internal/svc"
)

func runRepair(ctx context.Context, path string, options svc.RepairOptions) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	result, repairErr := svc.RepairAt(ctx, absolute, options)
	if errors.Is(repairErr, svc.ErrSupervisorUnavailable) && !options.CheckOnly && ctx.Err() == nil {
		sys, err := rescue.NativeSystem(filepath.Dir(absolute))
		if err == nil {
			original := sys.Run
			sys.Run = func(ctx context.Context, command string, args ...string) (string, error) {
				if command != "/bin/launchctl" || len(args) == 0 || (args[0] != "kickstart" && args[0] != "bootstrap") {
					return original(ctx, command, args...)
				}
				argv := []string{}
				if !stdinTerminal() {
					argv = append(argv, "-n")
				}
				argv = append(argv, command)
				argv = append(argv, args...)
				cmd := exec.CommandContext(ctx, "/usr/bin/sudo", argv...)
				cmd.Stdin = os.Stdin
				cmd.Stderr = os.Stderr
				out, err := cmd.Output()
				if err != nil {
					return string(out), fmt.Errorf("starting the supervisor needs local administrator authorization or a repaired installed service: %w", err)
				}
				return string(out), nil
			}
			err = rescue.StartSupervisor(ctx, filepath.Dir(absolute), sys)
		}
		if err != nil {
			result.Detail = err.Error()
			repairErr = err
		} else {
			deadline := time.NewTimer(10 * time.Second)
			retry := time.NewTicker(200 * time.Millisecond)
			wait := true
			for wait {
				select {
				case <-ctx.Done():
					repairErr = ctx.Err()
					wait = false
				case <-deadline.C:
					repairErr = errors.New("supervisor did not expose its local socket; inspect its installed executable path")
					wait = false
				case <-retry.C:
					result, repairErr = svc.RepairAt(ctx, absolute, options)
					if !errors.Is(repairErr, svc.ErrSupervisorUnavailable) {
						wait = false
					}
				}
			}
			deadline.Stop()
			retry.Stop()
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return err
	}
	return repairErr
}
