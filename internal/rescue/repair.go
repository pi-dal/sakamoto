package rescue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pi-dal/sakamoto/internal/recovery"
)

// StartSupervisor starts an identified, installed job without -k, bootout or
// network changes. A live job is never killed to fix an unresponsive socket.
func StartSupervisor(ctx context.Context, dir string, sys System) error {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	dir = absolute
	if recovery.Held(dir) {
		return errors.New("maintenance is held; resume deliberately before starting services")
	}
	jobs := []string{"system/dev.sakamoto.daemon", "system/dev.pi-dal.sing-box"}
	target := ""
	for _, job := range jobs {
		present, err := inspectJob(ctx, sys, job, dir)
		if err != nil {
			return err
		}
		if present {
			if target != "" {
				return errors.New("multiple supervisors identify this runtime; inspect before repair")
			}
			target = job
		}
	}
	if target != "" {
		if _, err := sys.Run(ctx, "/bin/launchctl", "kickstart", target); err != nil {
			return fmt.Errorf("cannot start identified supervisor; check administrator authorization and installed executable: %w", err)
		}
		return nil
	}
	// An unloaded existing job can be loaded offline. Never invent a plist or
	// guess which profile owns it, and never modify an installed plist in repair.
	plist := ""
	for _, job := range jobs {
		candidate := filepath.Join("/Library/LaunchDaemons", strings.TrimPrefix(job, "system/")+".plist")
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
			return errors.New("installed supervisor plist is not protected")
		}
		value, err := sys.Run(ctx, "/usr/bin/plutil", "-extract", "EnvironmentVariables.SAKAMOTO_DIR", "raw", "-o", "-", candidate)
		if err != nil || filepath.Clean(strings.TrimSpace(value)) != dir {
			return errors.New("installed supervisor plist belongs to another or unidentified runtime")
		}
		if plist != "" {
			return errors.New("multiple installed supervisor plists; inspect before repair")
		}
		plist = candidate
	}
	if plist == "" {
		return errors.New("no installed supervisor found; run sakamoto setup while healthy")
	}
	if _, err = sys.Run(ctx, "/bin/launchctl", "bootstrap", "system", plist); err != nil {
		return fmt.Errorf("cannot load installed supervisor: %w", err)
	}
	return nil
}
