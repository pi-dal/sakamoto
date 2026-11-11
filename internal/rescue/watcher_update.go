package rescue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"github.com/pi-dal/sakamoto/internal/recovery"
)

type WatcherUpdatePaths struct{ Runtime, Plist, Source, Install, Version string }

// CheckWatcherUpdate validates only the caller's existing canonical user agent.
// It performs no lifecycle or network operations.
func CheckWatcherUpdate(ctx context.Context, p WatcherUpdatePaths, sys System) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(p.Plist)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return errors.New("unsafe watcher launch agent")
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != sys.UID {
		return errors.New("watcher launch agent belongs to another user")
	}
	if sys.EUID == 0 || os.Geteuid() != sys.UID {
		return errors.New("watcher update must run as its non-root owner")
	}
	if !filepath.IsAbs(p.Source) || !filepath.IsAbs(p.Install) || p.Version == "" {
		return errors.New("watcher update requires an absolute executable and retained directory plus expected version")
	}
	source, err := os.Stat(p.Source)
	if err != nil || !source.Mode().IsRegular() || source.Mode().Perm()&0111 == 0 {
		return errors.New("replacement watcher executable is unavailable")
	}
	_, err = watcherPlist(ctx, p, sys)
	if err != nil {
		return err
	}
	found, err := inspectJob(ctx, sys, fmt.Sprintf("gui/%d/dev.sakamoto.watch", sys.UID), p.Runtime)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("installed watcher is not loaded")
	}
	return nil
}
func watcherPlist(ctx context.Context, p WatcherUpdatePaths, sys System) (map[string]any, error) {
	raw, err := sys.Run(ctx, "/usr/bin/plutil", "-convert", "json", "-o", "-", p.Plist)
	if err != nil {
		return nil, errors.New("cannot read watcher launch agent")
	}
	var plist map[string]any
	if err = json.Unmarshal([]byte(raw), &plist); err != nil {
		return nil, err
	}
	env, ok := plist["EnvironmentVariables"].(map[string]any)
	if !ok || env["SAKAMOTO_DIR"] != p.Runtime || plist["Label"] != "dev.sakamoto.watch" {
		return nil, errors.New("watcher belongs to another or unidentified runtime")
	}
	args, ok := plist["ProgramArguments"].([]any)
	if !ok || len(args) != 2 || args[1] != "watch" {
		return nil, errors.New("unsupported watcher arguments")
	}
	binary, ok := args[0].(string)
	if !ok || !filepath.IsAbs(binary) {
		return nil, errors.New("watcher executable is not absolute")
	}
	return plist, nil
}

// UpdateWatcher retains the replacement outside Homebrew/app bundles and
// restores the original plist/job if replacement loading fails. Call only after
// the old watcher's network restoration receipts have been cleared.
func UpdateWatcher(ctx context.Context, p WatcherUpdatePaths, sys System) (err error) {
	if err = CheckWatcherUpdate(ctx, p, sys); err != nil {
		return err
	}
	plist, err := watcherPlist(ctx, p, sys)
	if err != nil {
		return err
	}
	previous, err := os.ReadFile(p.Plist)
	if err != nil {
		return err
	}
	for _, name := range []string{"dns-restore.json", "proxy-restore.json"} {
		if _, e := os.Lstat(filepath.Join(p.Runtime, name)); !errors.Is(e, os.ErrNotExist) {
			return errors.New("network restoration must finish before updating watcher")
		}
	}
	if err = os.MkdirAll(p.Install, 0700); err != nil {
		return err
	}
	release, err := os.MkdirTemp(p.Install, "watcher-")
	if err != nil {
		return err
	}
	replacement := filepath.Join(release, "sakamoto")
	if err = copyServiceExecutable(p.Source, replacement); err != nil {
		return err
	}
	plist["ProgramArguments"] = []string{replacement, "watch"}
	raw, err := json.Marshal(plist)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(p.Plist), ".watcher-*.plist")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	_, writeErr := file.Write(raw)
	closeErr := file.Close()
	if err = errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	if _, err = sys.Run(ctx, "/usr/bin/plutil", "-convert", "xml1", temporary); err != nil {
		return err
	}
	candidate, err := os.ReadFile(temporary)
	if err != nil {
		return err
	}
	target := fmt.Sprintf("gui/%d/dev.sakamoto.watch", sys.UID)
	// Recheck ownership and concurrent edits just before changing the loaded job.
	if err = CheckWatcherUpdate(ctx, p, sys); err != nil {
		return err
	}
	actual, err := os.ReadFile(p.Plist)
	if err != nil {
		return err
	}
	if !bytes.Equal(previous, actual) {
		return errors.New("watcher launch agent changed during update")
	}
	defer func() {
		if err == nil {
			return
		}
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if present, e := inspectJob(rollbackCtx, sys, target, p.Runtime); e != nil {
			err = errors.Join(err, e)
			return
		} else if present {
			if e = stopJob(rollbackCtx, sys, target); e != nil {
				err = errors.Join(err, e)
				return
			}
		}
		if e := recovery.Atomic(p.Plist, previous, 0600); e != nil {
			err = errors.Join(err, e)
			return
		}
		_, e := sys.Run(rollbackCtx, "/bin/launchctl", "bootstrap", fmt.Sprintf("gui/%d", sys.UID), p.Plist)
		err = errors.Join(err, e)
	}()
	if err = stopJob(ctx, sys, target); err != nil {
		return err
	}
	if err = recovery.Atomic(p.Plist, candidate, 0600); err != nil {
		return err
	}
	if _, err = sys.Run(ctx, "/bin/launchctl", "bootstrap", fmt.Sprintf("gui/%d", sys.UID), p.Plist); err != nil {
		return err
	}
	readyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		loaded, e := sys.Run(readyCtx, "/bin/launchctl", "print", target)
		pid := regexp.MustCompile(`(?m)^\s*pid = ([0-9]+)\s*$`).FindStringSubmatch(loaded)
		program := regexp.MustCompile(`(?m)^\s*program = (.*?)\s*$`).FindStringSubmatch(loaded)
		if e == nil && len(pid) == 2 && len(program) == 2 && program[1] == replacement {
			expectedPID, _ := strconv.Atoi(pid[1])
			raw, e := socketControl(readyCtx, filepath.Join(p.Runtime, "watch.sock"), "info")
			var info struct {
				Version       string
				Protocol, PID int
			}
			if e == nil && json.Unmarshal([]byte(raw), &info) == nil && info.PID == expectedPID && expectedPID > 0 && info.Protocol >= 2 && info.Version == p.Version {
				return nil
			}
		}
		select {
		case <-readyCtx.Done():
			return errors.New("replacement watcher did not report the expected process, version and protocol")
		case <-time.After(100 * time.Millisecond):
		}
	}
}
