// Package rescue provides an offline escape independent of the application,
// watcher, native API, and running privileged supervisor.
package rescue

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/pi-dal/sakamoto/internal/recovery"
	"github.com/pi-dal/sakamoto/internal/sysdns"
	"github.com/pi-dal/sakamoto/internal/sysproxy"
	"gopkg.in/yaml.v3"
)

type System struct {
	EUID, UID int
	Home      string
	Run       func(context.Context, string, ...string) (string, error)
}
type Outcome struct {
	Maintenance   bool   `json:"maintenance"`
	JobsStopped   bool   `json:"jobs_stopped"`
	DNSRestored   bool   `json:"dns_restored"`
	ProxyRestored bool   `json:"proxy_restored"`
	Evidence      string `json:"evidence,omitempty"`
}

func NativeSystem(dir string) (System, error) {
	if runtime.GOOS != "darwin" {
		return System{}, errors.New("offline network rescue supports macOS only")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return System{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return System{}, errors.New("runtime owner is unavailable")
	}
	if st.Uid == 0 {
		return System{}, errors.New("runtime must belong to its designated non-root user; refusing to guess the watcher owner")
	}
	return System{EUID: os.Geteuid(), UID: int(st.Uid), Run: run}, nil
}
func run(ctx context.Context, command string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = append(CleanEnvironment(os.Environ()), "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if len(out) > 65536 {
		return "", errors.New("OS command response exceeded limit")
	}
	return string(out), err
}
func CleanEnvironment(env []string) []string {
	result := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToLower(key) {
		case "http_proxy", "https_proxy", "all_proxy":
			continue
		}
		result = append(result, entry)
	}
	return result
}
func socketControl(ctx context.Context, path, command string) (string, error) {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return "", err
		}
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if _, err = fmt.Fprintln(conn, command); err != nil {
		return "", err
	}
	line, err := bufio.NewReaderSize(io.LimitReader(conn, 16385), 16384).ReadString('\n')
	if len(line) > 16384 {
		return "", errors.New("control reply exceeded limit")
	}
	return strings.TrimSpace(line), err
}

func control(ctx context.Context, dir, command string) (string, error) {
	return socketControl(ctx, filepath.Join(dir, "svc.sock"), command)
}

var directoryLine = regexp.MustCompile(`(?m)^\s*SAKAMOTO_DIR\s*=>\s*(.*?)\s*$`)

func missingJob(out string) bool {
	return strings.Contains(out, "Could not find service") || strings.Contains(out, "Could not find domain")
}
func inspectJob(ctx context.Context, sys System, target, dir string) (bool, error) {
	out, err := sys.Run(ctx, "/bin/launchctl", "print", target)
	if err != nil {
		if missingJob(out) {
			return false, nil
		}
		return false, fmt.Errorf("cannot inspect %s", target)
	}
	match := directoryLine.FindStringSubmatch(out)
	if len(match) != 2 || filepath.Clean(match[1]) != dir {
		return false, fmt.Errorf("%s belongs to another or unidentified runtime; refusing to unload it", target)
	}
	return true, nil
}
func stopJob(ctx context.Context, sys System, target string) error {
	if out, err := sys.Run(ctx, "/bin/launchctl", "bootout", target); err != nil && !missingJob(out) {
		return fmt.Errorf("cannot unload %s", target)
	}
	deadline := time.NewTimer(40 * time.Second)
	defer deadline.Stop()
	for {
		out, err := sys.Run(ctx, "/bin/launchctl", "print", target)
		if err != nil && missingJob(out) {
			return nil
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-deadline.C:
			timer.Stop()
			return fmt.Errorf("%s has not stopped", target)
		case <-timer.C:
		}
	}
}
func legacyTarget(dir string) *sysproxy.Proxy {
	raw, err := os.ReadFile(filepath.Join(dir, "sakamoto.yaml"))
	if err != nil {
		return nil
	}
	var cfg struct {
		Mixed struct {
			Port int `yaml:"port"`
		} `yaml:"mixed_inbound"`
	}
	if yaml.Unmarshal(raw, &cfg) != nil {
		return nil
	}
	port := cfg.Mixed.Port
	if port == 0 {
		port = 2334
	}
	if port < 1 || port > 65535 {
		return nil
	}
	return &sysproxy.Proxy{Enabled: true, Server: "127.0.0.1", Port: port}
}

func preflightMissingState(dir string, runner func(...string) (string, error)) error {
	raw, readErr := os.ReadFile(filepath.Join(dir, "sakamoto.yaml"))
	var settings struct {
		DNS struct {
			Enabled bool   `yaml:"enabled"`
			Service string `yaml:"service"`
		} `yaml:"dns_guard"`
		Proxy struct {
			Enabled bool   `yaml:"enabled"`
			Service string `yaml:"service"`
		} `yaml:"system_proxy"`
	}
	if readErr == nil {
		readErr = yaml.Unmarshal(raw, &settings)
	}
	if readErr != nil {
		if _, err := os.Lstat(filepath.Join(dir, sysdns.StateName)); err == nil {
			return nil
		}
		receipt, err := recovery.ReadPrivate(filepath.Join(dir, "rescue-complete.json"), 4096)
		var prior struct {
			Runtime  string `json:"runtime"`
			Complete bool   `json:"complete"`
		}
		if err == nil && json.Unmarshal(receipt, &prior) == nil && prior.Runtime == dir && prior.Complete {
			return nil
		}
		return errors.New("sidecar unavailable and no original DNS snapshot; inspect locally before stopping the resolver")
	}
	if settings.DNS.Enabled {
		service := settings.DNS.Service
		if service == "" {
			service = "Wi-Fi"
		}
		if err := sysdns.CheckMissingState(filepath.Join(dir, sysdns.StateName), service, runner); err != nil {
			return err
		}
	}
	if settings.Proxy.Enabled {
		service := settings.Proxy.Service
		if service == "" {
			service = "Wi-Fi"
		}
		if err := sysproxy.CheckMissingState(filepath.Join(dir, "proxy-restore.json"), service, runner); err != nil {
			return err
		}
	}
	return nil
}

// Exit requires explicit local administrator invocation. It never runs from an
// automatic health probe, and ordinary networking is an intentional owner choice.
func Exit(ctx context.Context, dir string, sys System) (Outcome, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	result := Outcome{}
	if sys.EUID != 0 {
		return result, errors.New("run the installed rescue tool with sudo for explicit network restoration")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return result, err
	}
	dir = absolute
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return result, errors.New("runtime must be an existing real directory")
	}
	jobs := []string{fmt.Sprintf("gui/%d/dev.sakamoto.watch", sys.UID), fmt.Sprintf("gui/%d/dev.pi-dal.sakamoto-watch", sys.UID), "system/dev.sakamoto.daemon", "system/dev.pi-dal.sing-box"}
	present := make([]bool, len(jobs))
	for i, job := range jobs {
		present[i], err = inspectJob(ctx, sys, job, dir)
		if err != nil {
			return result, err
		}
	}
	if err = recovery.Hold(dir, "explicit offline network restoration"); err != nil {
		return result, err
	}
	result.Maintenance = true
	evidence, err := os.MkdirTemp(dir, "rescue-evidence-")
	if err != nil {
		return result, err
	}
	result.Evidence = evidence
	for _, name := range []string{"dns-restore.json", "proxy-restore.json", "crash-recovery.json", "config.json", "sakamoto.yaml"} {
		path := filepath.Join(dir, name)
		raw, err := recovery.ReadPrivate(path, 64<<20)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return result, fmt.Errorf("cannot preserve %s: %w", name, err)
		}
		if err = recovery.Atomic(filepath.Join(evidence, name), raw, 0600); err != nil {
			return result, err
		}
	}
	if err = sysdns.ValidateState(filepath.Join(dir, sysdns.StateName)); err != nil {
		return result, err
	}
	if err = sysproxy.ValidateState(filepath.Join(dir, "proxy-restore.json")); err != nil {
		return result, err
	}
	runner := func(args ...string) (string, error) { return sys.Run(ctx, "/usr/sbin/networksetup", args...) }
	if err = preflightMissingState(dir, runner); err != nil {
		return result, err
	}
	// Ask a new supervisor to cancel and restore cooperatively first. If it is
	// old, absent or stuck, the scoped launchd fallback remains independent.
	cooperative, cancel := context.WithTimeout(ctx, time.Second)
	_, _ = control(cooperative, dir, "maintenance")
	cancel()
	for i, job := range jobs {
		if present[i] {
			if err = stopJob(ctx, sys, job); err != nil {
				return result, err
			}
		}
	}
	result.JobsStopped = true
	if err = sysdns.Restore(filepath.Join(dir, sysdns.StateName), runner); err != nil {
		return result, err
	}
	result.DNSRestored = true
	if err = sysproxy.Restore(filepath.Join(dir, "proxy-restore.json"), legacyTarget(dir), runner); err != nil {
		return result, err
	}
	result.ProxyRestored = true
	receipt, _ := json.Marshal(result)
	completed, _ := json.Marshal(map[string]any{"runtime": dir, "complete": true})
	if err = recovery.Atomic(filepath.Join(dir, "rescue-complete.json"), completed, 0600); err != nil {
		return result, err
	}
	if err = recovery.Atomic(filepath.Join(evidence, "outcome.json"), receipt, 0600); err != nil {
		return result, err
	}
	return result, nil
}

// Diagnose only reads local metadata and bounded Unix-socket replies. It does
// not resolve names, probe websites, change settings or reveal API credentials.
func Diagnose(ctx context.Context, dir string) (map[string]any, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	dir = absolute
	report := map[string]any{"runtime": dir, "maintenance": recovery.Held(dir), "read_only": true}
	for _, command := range []string{"status", "info"} {
		probe, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		reply, err := control(probe, dir, command)
		cancel()
		if err != nil {
			report["supervisor_"+command] = map[string]any{"available": false}
			continue
		}
		if command == "info" {
			var value map[string]any
			if json.Unmarshal([]byte(reply), &value) == nil {
				report["supervisor_info"] = value
			} else {
				report["supervisor_info"] = map[string]any{"available": false, "upgrade_required": true}
			}
		} else {
			report["supervisor_status"] = reply
		}
	}
	probe, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	watcherReply, watcherErr := socketControl(probe, filepath.Join(dir, "watch.sock"), "info")
	cancel()
	var watcher map[string]any
	if watcherErr == nil && json.Unmarshal([]byte(watcherReply), &watcher) == nil {
		report["watcher_info"] = watcher
	} else {
		report["watcher_info"] = map[string]any{"available": false, "upgrade_or_start_required": true}
	}
	helper := "/Library/Application Support/Sakamoto/rescue/sakamoto-rescue"
	if info, err := os.Lstat(helper); err == nil {
		report["independent_rescue"] = map[string]any{"installed": info.Mode().IsRegular(), "path": helper}
	} else {
		report["independent_rescue"] = map[string]any{"installed": false}
	}
	snapshots := map[string]any{}
	for _, name := range []string{sysdns.StateName, "proxy-restore.json", recovery.CircuitName} {
		info, err := os.Lstat(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			snapshots[name] = map[string]any{"exists": false}
			continue
		}
		if err != nil {
			snapshots[name] = map[string]any{"error": "metadata unavailable"}
			continue
		}
		snapshots[name] = map[string]any{"exists": true, "mode": info.Mode().Perm().String(), "regular": info.Mode().IsRegular()}
	}
	report["saved_state"] = snapshots
	proxies := []map[string]any{}
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		switch strings.ToLower(name) {
		case "http_proxy", "https_proxy", "all_proxy":
			host := strings.Contains(value, "127.0.0.1:") || strings.Contains(value, "localhost:") || strings.Contains(value, "[::1]:")
			proxies = append(proxies, map[string]any{"variable": name, "loopback": host})
		}
	}
	report["proxy_environment"] = proxies
	report["rescue_command"] = "sudo '/Library/Application Support/Sakamoto/rescue/sakamoto-rescue' exit --dir '" + strings.ReplaceAll(dir, "'", "'\\''") + "' --restore-network"
	return report, nil
}
