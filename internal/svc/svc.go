// Package svc supervises sing-box as root and accepts VPN controls over a Unix socket.
package svc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/recovery"
	"github.com/pi-dal/sakamoto/internal/sysdns"
	"syscall"
	"time"
)

const SockName = "svc.sock"

func SockPath() string { return filepath.Join(config.DefaultDir(), SockName) }

// Server is the root supervisor process.
type Server struct {
	cfgPath          string
	workDir          string
	mu               sync.Mutex
	lifecycle        sync.Mutex
	repairMu         sync.Mutex
	repairing        bool
	child            *exec.Cmd
	childDone        chan struct{}
	dnsRun           sysdns.Runner
	dnsActivateRun   func(context.Context, ...string) (string, error)
	dnsProbeContext  func(context.Context, string) error
	dnsPorts         func(string) error
	vpnConnected     func() bool
	command          func(string, string, string) *exec.Cmd
	dnsStatus        string
	dnsWait          time.Duration
	started          time.Time
	restarts         int
	logs             []string
	cancelStart      context.CancelFunc
	retryDelay       time.Duration
	stablePeriod     time.Duration
	circuit          recovery.Circuit
	stateErr         error
	version          string
	epoch            func(context.Context) (time.Time, error)
	retryGen         int
	activeConfigHash string
	stopRequested    bool
	retryOwner       *exec.Cmd
	retryOwnerGen    int
	retryActive      bool
}

func NewServer(cfgPath, workDir string) *Server {
	s := &Server{cfgPath: cfgPath, workDir: workDir, dnsRun: sysdns.Networksetup, dnsActivateRun: sysdns.NetworksetupContext, dnsProbeContext: sysdns.ProbeContext, dnsPorts: sysdns.PortsFree, vpnConnected: shadowrocketVPNConnected, dnsWait: 25 * time.Second,
		retryDelay: 2 * time.Second, stablePeriod: recovery.StableInterval, version: "development",
		command: func(binary, path, dir string) *exec.Cmd {
			cmd := exec.Command(binary, "run", "-c", path, "-D", dir)
			// Keep the child cwd aligned with -D: generated config.json uses
			// runtime-relative paths (rules/*.srs), which must not resolve
			// against whatever directory the daemon happened to start in.
			cmd.Dir = dir
			return cmd
		},
	}
	s.circuit, s.stateErr = recovery.LoadCircuit(workDir)
	s.epoch = s.coreEpoch
	return s
}

// Run starts the control socket but waits for an explicit connect command.
func (s *Server) Run() error { return s.RunContext(context.Background()) }

func (s *Server) RunContext(ctx context.Context) error {
	sock := filepath.Join(s.workDir, SockName)
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(s.workDir, "svc.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another supervisor owns this runtime: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	if info, err := os.Lstat(sock); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("refusing to remove a non-socket control path")
		}
		if probe, err := net.DialTimeout("unix", sock, 200*time.Millisecond); err == nil {
			_ = probe.Close()
			return fmt.Errorf("a supervisor is already listening")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(sock); err != nil && !os.IsNotExist(err) {
		return err
	}
	l, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	defer func() { _ = l.Close() }()
	// Permit only the designated user to control this root process.
	if name := os.Getenv("SAKAMOTO_USER"); name != "" {
		u, err := user.Lookup(name)
		if err != nil {
			return err
		}
		uid, err := strconv.Atoi(u.Uid)
		if err != nil {
			return err
		}
		gid, err := strconv.Atoi(u.Gid)
		if err != nil {
			return err
		}
		if err := os.Chown(sock, uid, gid); err != nil {
			return err
		}
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		return err
	}
	fmt.Println("daemon: listening", sock)
	// If Shadowrocket connects afterward, keep it and release our TUN.
	go s.monitorVPNConflict(ctx)
	// Serialize orphan cleanup before accepting a new activation, while the
	// independent status socket is already usable and stop can cancel cleanup.
	s.lifecycle.Lock()
	orphanCtx, cancelOrphan := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancelStart = cancelOrphan
	s.mu.Unlock()
	go func() {
		defer s.lifecycle.Unlock()
		defer cancelOrphan()
		if recovery.Held(s.workDir) {
			return
		}
		if _, err := os.Lstat(s.dnsStatePath()); err == nil && s.dnsPorts("127.0.0.1:53") == nil {
			if err := s.restoreDNSContext(orphanCtx); err != nil {
				s.appendLog("orphaned DNS restoration pending: " + err.Error())
			}
		}
	}()
	go func() { <-ctx.Done(); _ = l.Close() }()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				s.stop()
				return ctx.Err()
			}
			return err
		}
		go s.handle(conn)
	}
}

func (s *Server) monitorVPNConflict(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !s.vpnConnected() {
			continue
		}
		s.mu.Lock()
		running := s.runningLocked()
		s.mu.Unlock()
		if running {
			s.appendLog("Shadowrocket VPN connected; stopping sing-box to avoid two active TUNs")
			s.stop()
		}
	}
}

func (s *Server) handle(c net.Conn) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	sc := bufio.NewScanner(c)
	if !sc.Scan() {
		return
	}
	command := strings.TrimSpace(sc.Text())
	_ = c.SetDeadline(time.Now().Add(75 * time.Second))
	cmd, arguments, _ := strings.Cut(command, " ")
	arguments = strings.TrimSpace(arguments)
	var reply string
	if arguments != "" && cmd != "urltest" && cmd != "select" && cmd != "repair" {
		_, _ = fmt.Fprintln(c, "error: command takes no arguments")
		return
	}
	switch cmd {
	case "repair":
		var options RepairOptions
		decoder := json.NewDecoder(strings.NewReader(arguments))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&options); err != nil {
			reply = "error: invalid repair options"
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
			reply = repairReply(s.repair(ctx, options))
			cancel()
		}
	case "connect":
		reply = s.start()
	case "disconnect":
		reply = s.stop()
	case "status":
		reply = s.status()
	case "capabilities":
		reply = "native-dns-v1"
	case "info":
		reply = s.info()
	case "resume":
		reply = s.resume()
	case "resume-installed":
		reply = s.resumeWithEngine(true)
	case "rollback":
		reply = s.rollback()
	case "maintenance":
		if err := recovery.Hold(s.workDir, "offline rescue"); err != nil {
			reply = "error: " + err.Error()
		} else {
			reply = s.stop()
		}
	case "dns-restore":
		reply = s.restoreDNSCommand()
	case "state", "urltest", "select", "reload":
		reply = s.control(cmd, arguments)
	case "logs":
		reply = strings.Join(s.lastLogs(15), "\n")
	default:
		reply = "unknown: " + cmd
	}
	_, _ = fmt.Fprintf(c, "%s\n", reply) // client may disconnect before receiving reply
}

func (s *Server) appendLog(l string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), l))
	if len(s.logs) > 200 {
		s.logs = s.logs[len(s.logs)-200:]
	}
}

func (s *Server) lastLogs(n int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.logs) < n {
		return append([]string{}, s.logs...)
	}
	return s.logs[len(s.logs)-n:]
}

func shadowrocketVPNConnected() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--nc", "list").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "(Connected)") && strings.Contains(line, "com.liguangming.Shadowrocket") {
			return true
		}
	}
	return false
}
func (s *Server) start() string { return s.startGeneration(nil, 0) }

func (s *Server) startGeneration(expected *exec.Cmd, generation int) string {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if recovery.Held(s.workDir) {
		return "start failed: maintenance hold; run sakamoto ctl resume, then connect explicitly"
	}
	s.mu.Lock()
	if s.stateErr != nil || s.circuit.Blocked {
		s.mu.Unlock()
		return "start failed: recovery blocked; inspect sakamoto doctor, then ctl rollback or ctl resume"
	}
	if expected != nil && (s.child != expected || s.restarts != generation) {
		s.mu.Unlock()
		return "restart canceled"
	}
	if s.runningLocked() {
		s.mu.Unlock()
		return "already running"
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelStart = cancel
	s.stopRequested = false
	s.mu.Unlock()
	defer cancel()
	if s.vpnConnected() {
		return "start failed: disconnect Shadowrocket VPN first; two TUNs cannot run together"
	}
	if err := validateAPIService(s.cfgPath); err != nil {
		return "start failed: " + err.Error()
	}
	dnsCfg, guard, err := s.dnsPlan()
	if err != nil {
		return "start failed: " + err.Error()
	}
	hadState := false
	if _, e := os.Lstat(s.dnsStatePath()); e == nil {
		hadState = true
	}
	if !guard {
		if err := s.restoreDNSContext(ctx); err != nil {
			return "start failed: " + err.Error()
		}
	}
	if guard {
		if err := s.dnsPorts("127.0.0.1:53"); err != nil {
			return "start failed: " + err.Error()
		}
	}
	if ctx.Err() != nil || recovery.Held(s.workDir) {
		return "start canceled"
	}
	binary, err := s.selectedBinary()
	if err != nil {
		return "start failed: " + err.Error()
	}
	if expected != nil {
		if s.runtimeHash() != s.activeConfigHash {
			return "start failed: staged configuration differs from the applied runtime; explicit activation required"
		}
	}
	cmd := s.command(binary, s.cfgPath, s.workDir)
	candidate, captureErr := s.captureRuntime(cmd)
	if captureErr != nil {
		return "start failed: recovery snapshot: " + captureErr.Error()
	}
	defer func() {
		if candidate != nil {
			candidate.Close()
		}
	}()
	cmd.Stdout, cmd.Stderr = logWriter{s}, logWriter{s}
	if ctx.Err() != nil || recovery.Held(s.workDir) {
		return "start canceled"
	}
	if err := cmd.Start(); err != nil {
		return "start failed: " + err.Error()
	}
	done := make(chan struct{})
	s.mu.Lock()
	s.child, s.childDone, s.started = cmd, done, time.Now()
	s.activeConfigHash = candidate.Fingerprint()
	s.restarts++
	gen := s.restarts
	s.dnsStatus = "off"
	if guard {
		s.dnsStatus = "starting"
	}
	s.mu.Unlock()
	go s.waitLoop(cmd, done)
	if guard {
		if err := s.protectDNS(ctx, dnsCfg, done); err != nil {
			if ctx.Err() != nil {
				s.mu.Lock()
				s.dnsStatus = "degraded"
				s.mu.Unlock()
				return "start canceled; core retained until pending stop restores DNS"
			}
			// A core crash-restart retains loopback DNS (fail closed) while
			// the watcher repairs entries. Never fall back to DHCP implicitly.
			if hadState {
				s.mu.Lock()
				s.dnsStatus = "degraded"
				s.mu.Unlock()
				return fmt.Sprintf("connected (pid %d) dns=degraded; protected snapshot retained: %v", cmd.Process.Pid, err)
			}
			if restoreErr := s.restoreDNSContext(ctx); restoreErr != nil {
				s.mu.Lock()
				s.dnsStatus = "degraded"
				s.mu.Unlock()
				return fmt.Sprintf("connected (pid %d) dns=degraded; core retained: %v; restore: %v", cmd.Process.Pid, err, restoreErr)
			}
			s.mu.Lock()
			s.restarts++
			s.child = nil
			s.dnsStatus = "failed"
			s.mu.Unlock()
			_ = cmd.Process.Signal(syscall.SIGTERM)
			return "start failed: " + err.Error()
		}
		s.mu.Lock()
		s.dnsStatus = "protected"
		s.mu.Unlock()
	}
	if ctx.Err() == nil && !recovery.Held(s.workDir) {
		go s.observeRuntime(cmd, gen, done, candidate)
		candidate = nil
	}
	s.mu.Lock()
	dnsStatus := s.dnsStatus
	s.mu.Unlock()
	return fmt.Sprintf("connected (pid %d) dns=%s", cmd.Process.Pid, dnsStatus)
}

// singBoxExecutable supports Apple Silicon, Intel, and non-default Homebrew
// prefixes. The installer pins the resolved path in the launchd environment.
func singBoxExecutable() string {
	if binary := os.Getenv("SAKAMOTO_SING_BOX"); binary != "" {
		if info, err := os.Stat(binary); err == nil && !info.IsDir() && info.Mode().Perm()&0111 != 0 {
			return binary
		}
	}
	for _, path := range []string{"/opt/homebrew/bin/sing-box", "/usr/local/bin/sing-box"} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
			return path
		}
	}
	return "sing-box"
}

// validateAPIService fails closed before a root child could expose an API with
// a placeholder secret, a LAN listener, or a browser dashboard.
func validateAPIService(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var c struct {
		Services []struct {
			Type      string `json:"type"`
			Listen    string `json:"listen"`
			Secret    string `json:"secret"`
			Dashboard struct {
				Enabled bool `json:"enabled"`
			} `json:"dashboard"`
		} `json:"services"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return err
	}
	count := 0
	for _, s := range c.Services {
		if s.Type != "api" {
			continue
		}
		count++
		if s.Listen != "127.0.0.1" || s.Dashboard.Enabled {
			return fmt.Errorf("API must listen on loopback without dashboard; run sakamoto rotate-api")
		}
		if err := config.ValidateAPISecret(s.Secret); err != nil {
			return err
		}
	}
	if count != 1 {
		return fmt.Errorf("exactly one authenticated API service required")
	}
	return nil
}

// waitLoop restarts unexpected exits with backoff; intentional stops are excluded.
func (s *Server) waitLoop(cmd *exec.Cmd, done chan struct{}) {
	err := cmd.Wait()
	close(done)
	s.appendLog(fmt.Sprintf("sing-box exited: %v", err))
	s.mu.Lock()
	gen := s.restarts
	eligible := s.child == cmd && !s.stopRequested && !s.repairing
	s.mu.Unlock()
	if eligible {
		s.retryCore(cmd, gen)
	}
}

func (s *Server) stop() string {
	// Cancel readiness and invalidate retries before waiting for lifecycle work.
	s.mu.Lock()
	s.restarts++
	s.stopRequested = true
	if s.cancelStart != nil {
		s.cancelStart()
	}
	s.mu.Unlock()
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	// Restore before killing the only loopback DNS listener. If restoration
	// fails, retain the core and snapshot rather than strand the machine.
	if err := s.restoreDNS(); err != nil {
		s.mu.Lock()
		s.stopRequested = false
		child, gen, alive := s.child, s.restarts, s.runningLocked()
		s.mu.Unlock()
		if child != nil && !alive && !recovery.Held(s.workDir) {
			go s.retryCore(child, gen)
		}
		return "disconnect failed: " + err.Error()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.runningLocked() {
		s.restarts++
		s.child = nil
		s.dnsStatus = "off"
		return "not running"
	}
	s.restarts++ // Invalidate waitLoop's generation to prevent auto-restart.
	pid := s.child.Process.Pid
	// SIGTERM lets sing-box release TUN routes and DNS cleanly.
	if err := s.child.Process.Signal(syscall.SIGTERM); err != nil {
		return "disconnect failed: " + err.Error()
	}
	s.child = nil
	s.dnsStatus = "off"
	return fmt.Sprintf("disconnected (pid %d)", pid)
}

func (s *Server) status() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runningLocked() {
		return fmt.Sprintf("connected pid=%d up=%s restarts=%d dns=%s",
			s.child.Process.Pid, time.Since(s.started).Round(time.Second), s.restarts, s.dnsStatus)
	}
	if s.circuit.Blocked || s.stateErr != nil {
		return "disconnected blocked"
	}
	if recovery.Held(s.workDir) {
		return "disconnected maintenance"
	}
	if s.retryGen == s.restarts && s.restarts != 0 {
		return fmt.Sprintf("disconnected backoff attempts=%d", s.circuit.Attempts)
	}
	return "disconnected"
}

type logWriter struct{ s *Server }

func (w logWriter) Write(p []byte) (int, error) {
	for _, l := range strings.Split(strings.TrimSpace(string(p)), "\n") {
		if l != "" {
			w.s.appendLog(l)
		}
	}
	return len(p), nil
}
