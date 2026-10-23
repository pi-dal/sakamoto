package rescue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pi-dal/sakamoto/internal/recovery"
)

// Only the canonical, already installed supervisor is replaced. Legacy label
// migration and installation of a missing service remain separate operations.
type ServiceUpdatePaths struct{ Runtime, Plist, Install, Source, RescueSource string }
type ServiceUpdateResult struct {
	Updated    bool   `json:"updated"`
	Backup     string `json:"backup,omitempty"`
	RolledBack bool   `json:"rolled_back,omitempty"`
}

func servicePlist(ctx context.Context, p ServiceUpdatePaths, sys System) (map[string]any, error) {
	info, err := os.Lstat(p.Plist)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > 1<<20 {
		return nil, errors.New("installed supervisor plist is not protected")
	}
	if os.Geteuid() == 0 {
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != 0 {
			return nil, errors.New("installed supervisor plist is not root-owned")
		}
	}
	raw, err := sys.Run(ctx, "/usr/bin/plutil", "-convert", "json", "-o", "-", p.Plist)
	if err != nil {
		return nil, errors.New("cannot read installed supervisor plist")
	}
	var plist map[string]any
	if err = json.Unmarshal([]byte(raw), &plist); err != nil {
		return nil, err
	}
	env, ok := plist["EnvironmentVariables"].(map[string]any)
	if !ok || env["SAKAMOTO_DIR"] != p.Runtime || plist["Label"] != "dev.sakamoto.daemon" {
		return nil, errors.New("installed supervisor plist belongs to another or unidentified runtime")
	}
	args, ok := plist["ProgramArguments"].([]any)
	if !ok || len(args) != 2 || args[1] != "daemon" {
		return nil, errors.New("installed supervisor uses unsupported arguments")
	}
	path, ok := args[0].(string)
	if !ok || !filepath.IsAbs(path) {
		return nil, errors.New("installed supervisor has no absolute executable")
	}
	old, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("previous service executable is unavailable: %w", err)
	}
	oldInfo, statErr := old.Stat()
	closeErr := old.Close()
	if err = errors.Join(statErr, closeErr); err != nil {
		return nil, err
	}
	if !oldInfo.Mode().IsRegular() || oldInfo.Size() == 0 || oldInfo.Mode().Perm()&0111 == 0 {
		return nil, errors.New("previous service executable is unsafe")
	}
	return plist, nil
}
func CheckServiceUpdate(ctx context.Context, p ServiceUpdatePaths, sys System) error {
	if !filepath.IsAbs(p.Runtime) || filepath.Clean(p.Runtime) != p.Runtime {
		return errors.New("service update requires an absolute canonical runtime directory")
	}
	if recovery.Held(p.Runtime) {
		return errors.New("maintenance is held; resume deliberately before updating services")
	}
	found, err := inspectJob(ctx, sys, "system/dev.sakamoto.daemon", p.Runtime)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("no canonical running supervisor found; use setup or the legacy migration procedure")
	}
	legacy, err := inspectJob(ctx, sys, "system/dev.pi-dal.sing-box", p.Runtime)
	if err != nil {
		return err
	}
	if legacy {
		return errors.New("duplicate supervisor jobs; inspect before updating")
	}
	if _, err = servicePlist(ctx, p, sys); err != nil {
		return err
	}
	return nil
}
func protectedServiceDirectory(path string) error {
	if err := os.Mkdir(path, 0755); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return errors.New("unsafe service installation directory")
	}
	if os.Geteuid() == 0 {
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != 0 {
			return errors.New("service installation directory is not root-owned")
		}
	}
	return nil
}
func copyServiceExecutable(source, dest string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	before, err := input.Stat()
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0111 == 0 || before.Size() == 0 || before.Size() > 512<<20 {
		return errors.New("invalid service executable")
	}
	output, err := os.CreateTemp(filepath.Dir(dest), ".sakamoto-executable-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(output.Name()) }()
	if err := output.Chmod(0755); err != nil {
		_ = output.Close()
		return err
	}
	_, copyErr := io.Copy(output, io.LimitReader(input, (512<<20)+1))
	syncErr := output.Sync()
	closeErr := output.Close()
	after, statErr := input.Stat()
	if statErr != nil {
		return statErr
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return errors.New("service source changed during copy")
	}
	if err = errors.Join(copyErr, syncErr, closeErr); err != nil {
		return err
	}
	if err = os.Link(output.Name(), dest); err != nil {
		return err
	}
	return recovery.SyncDir(filepath.Dir(dest))
}

// StageServiceRescue installs the independent local helper before the caller
// disconnects the VPN. It performs no socket, launchd or network operations.
func StageServiceRescue(p ServiceUpdatePaths, sys System) error {
	if sys.EUID != 0 {
		return errors.New("staging independent rescue requires administrator authorization")
	}
	if err := protectedServiceDirectory(p.Install); err != nil {
		return err
	}
	rescueDir := filepath.Join(p.Install, "rescue")
	if err := protectedServiceDirectory(rescueDir); err != nil {
		return err
	}
	path := filepath.Join(rescueDir, "sakamoto-rescue")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return copyServiceExecutable(p.RescueSource, path)
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Mode().Perm()&0111 == 0 || info.Mode().Perm()&0022 != 0 {
		return errors.New("existing independent rescue is unsafe")
	}
	if os.Geteuid() == 0 {
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != 0 {
			return errors.New("independent rescue is not root-owned")
		}
	}
	return nil
}

func writeServicePlist(ctx context.Context, path string, plist map[string]any, sys System) error {
	raw, err := json.Marshal(plist)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".sakamoto-service-*.plist")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if _, err = file.Write(raw); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if _, err = sys.Run(ctx, "/usr/bin/plutil", "-convert", "xml1", temporary); err != nil {
		return errors.New("updated supervisor plist could not be encoded")
	}
	if err = os.Chmod(temporary, 0644); err != nil {
		return err
	}
	// LaunchDaemon plists always belong to root, even when an earlier package
	// manager executable was owned by the user.
	if os.Geteuid() == 0 {
		if err = os.Chown(temporary, 0, 0); err != nil {
			return err
		}
	}
	handle, err := os.Open(temporary)
	if err != nil {
		return err
	}
	syncErr := handle.Sync()
	closeErr := handle.Close()
	if err = errors.Join(syncErr, closeErr); err != nil {
		return err
	}
	if err = os.Rename(temporary, path); err != nil {
		return err
	}
	return recovery.SyncDir(filepath.Dir(path))
}

// UpdateService requires a disconnected core and local administrator approval
// from its caller. It retains both executable generations outside the app and
// Homebrew, stages independent rescue first, and never starts a VPN itself.
func UpdateService(ctx context.Context, p ServiceUpdatePaths, sys System) (out ServiceUpdateResult, err error) {
	if sys.EUID != 0 {
		return out, errors.New("service update requires local administrator authorization")
	}
	if err = CheckServiceUpdate(ctx, p, sys); err != nil {
		return out, err
	}
	status, err := control(ctx, p.Runtime, "status")
	if err != nil {
		return out, err
	}
	if !strings.HasPrefix(status, "disconnected") {
		return out, errors.New("disconnect the core before replacing its supervisor")
	}
	for _, name := range []string{"dns-restore.json", "proxy-restore.json"} {
		if _, err = os.Lstat(filepath.Join(p.Runtime, name)); !errors.Is(err, os.ErrNotExist) {
			return out, errors.New("network restoration must complete before updating the supervisor")
		}
	}
	plist, err := servicePlist(ctx, p, sys)
	if err != nil {
		return out, err
	}
	if err = protectedServiceDirectory(p.Install); err != nil {
		return out, err
	}
	lock, err := os.OpenFile(filepath.Join(p.Install, "service-update.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return out, err
	}
	defer func() { _ = lock.Close() }()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return out, errors.New("another service update is in progress")
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	if err = StageServiceRescue(p, sys); err != nil {
		return out, err
	}
	release, err := os.MkdirTemp(p.Install, "service-")
	if err != nil {
		return out, err
	}
	out.Backup = release
	if err = os.Chmod(release, 0755); err != nil {
		return out, err
	}
	args := plist["ProgramArguments"].([]any)
	previous := filepath.Join(release, "sakamoto.previous")
	if err = copyServiceExecutable(args[0].(string), previous); err != nil {
		return out, err
	}
	current := filepath.Join(release, "sakamoto")
	if err = copyServiceExecutable(p.Source, current); err != nil {
		return out, err
	}
	previousPlist := map[string]any{}
	for k, v := range plist {
		previousPlist[k] = v
	}
	previousPlist["ProgramArguments"] = []string{previous, "daemon"}
	previousPath := filepath.Join(release, "previous.plist")
	if err = writeServicePlist(ctx, previousPath, previousPlist, sys); err != nil {
		return out, err
	}
	plist["ProgramArguments"] = []string{current, "daemon"}
	// This hold prevents a new GUI/watcher from starting the replacement core
	// before installation is committed. An incomplete rollback retains the hold.
	hold := filepath.Join(p.Runtime, recovery.HoldName)
	holdRaw, _ := json.Marshal(map[string]string{"reason": "controlled service update", "generation": filepath.Base(release)})
	file, err := os.OpenFile(hold, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return out, err
	}
	_, writeErr := file.Write(holdRaw)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		return out, err
	}
	stopped := false
	defer func() {
		if err == nil || out.Updated {
			return
		}
		out.Updated = false
		if stopped {
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			present, scopeErr := inspectJob(rollbackCtx, sys, "system/dev.sakamoto.daemon", p.Runtime)
			if scopeErr != nil {
				err = errors.Join(err, scopeErr)
				return
			}
			if present {
				if stopErr := stopJob(rollbackCtx, sys, "system/dev.sakamoto.daemon"); stopErr != nil {
					err = errors.Join(err, stopErr)
					return
				}
			}
			restoreErr := writeServicePlist(rollbackCtx, p.Plist, previousPlist, sys)
			if restoreErr == nil {
				_, restoreErr = sys.Run(rollbackCtx, "/bin/launchctl", "bootstrap", "system", p.Plist)
			}
			out.RolledBack = restoreErr == nil
			err = errors.Join(err, restoreErr)
		}
		if !stopped || out.RolledBack {
			owned, e := recovery.ReadPrivate(hold, 4096)
			if e == nil && bytes.Equal(owned, holdRaw) {
				err = errors.Join(err, os.Remove(hold), recovery.SyncDir(p.Runtime))
			}
		}
	}()
	// Recheck immediately before unloading; old supervisors cannot honor holds.
	status, err = control(ctx, p.Runtime, "status")
	if err != nil {
		return out, err
	}
	if !strings.HasPrefix(status, "disconnected") {
		return out, errors.New("core started during service preparation; update canceled")
	}
	if present, scopeErr := inspectJob(ctx, sys, "system/dev.sakamoto.daemon", p.Runtime); scopeErr != nil || !present {
		return out, errors.New("supervisor identity changed during service preparation")
	}
	stopped = true
	if err = stopJob(ctx, sys, "system/dev.sakamoto.daemon"); err != nil {
		return out, err
	}
	if err = writeServicePlist(ctx, p.Plist, plist, sys); err != nil {
		return out, err
	}
	if _, err = sys.Run(ctx, "/bin/launchctl", "bootstrap", "system", p.Plist); err != nil {
		return out, fmt.Errorf("replacement supervisor could not start: %w", err)
	}
	readyCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	for {
		raw, e := control(readyCtx, p.Runtime, "info")
		var info struct {
			Capabilities []string `json:"capabilities"`
			Supervisor   struct {
				Executable string `json:"executable"`
				PID        int    `json:"pid"`
			} `json:"supervisor"`
		}
		if e == nil && json.Unmarshal([]byte(raw), &info) == nil && info.Supervisor.Executable == current && info.Supervisor.PID > 0 {
			for _, capability := range info.Capabilities {
				if capability == "configuration-repair-v1" {
					out.Updated = true
				}
			}
		}
		if out.Updated {
			break
		}
		select {
		case <-readyCtx.Done():
			return out, errors.New("replacement supervisor did not advertise configuration recovery")
		case <-time.After(100 * time.Millisecond):
		}
	}
	owned, e := recovery.ReadPrivate(hold, 4096)
	if e != nil || !bytes.Equal(owned, holdRaw) {
		out.Updated = false
		return out, errors.New("maintenance ownership changed during service update")
	}
	if err = os.Remove(hold); err != nil {
		out.Updated = false
		return out, err
	}
	err = recovery.SyncDir(p.Runtime)
	return out, err
}
