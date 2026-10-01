// Package svc supervises sing-box as root and accepts VPN controls over a Unix socket.
package svc

import (
	"bufio"
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
	"github.com/pi-dal/sakamoto/internal/sysdns"
	"syscall"
	"time"
)

const SockName = "svc.sock"

func SockPath() string { return filepath.Join(config.DefaultDir(), SockName) }

// Server is the root supervisor process.
type Server struct {
	cfgPath      string
	workDir      string
	mu           sync.Mutex
	lifecycle    sync.Mutex
	child        *exec.Cmd
	childDone    chan struct{}
	dnsRun       sysdns.Runner
	dnsProbe     func(string) error
	dnsPorts     func(string) error
	vpnConnected func() bool
	command      func(string, string, string) *exec.Cmd
	dnsStatus    string
	dnsWait      time.Duration
	started      time.Time
	restarts     int
	logs         []string
}

func NewServer(cfgPath, workDir string) *Server {
	return &Server{cfgPath: cfgPath, workDir: workDir, dnsRun: sysdns.Networksetup, dnsProbe: sysdns.Probe, dnsPorts: sysdns.PortsFree, vpnConnected: shadowrocketVPNConnected, dnsWait: 25 * time.Second,
		command: func(binary, path, dir string) *exec.Cmd { return exec.Command(binary, "run", "-c", path, "-D", dir) },
	}
}

// Run starts the control socket but waits for an explicit connect command.
func (s *Server) Run() error {
	sock := filepath.Join(s.workDir, SockName)
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		return err
	}
	// If the supervisor itself restarted without a child, recover an orphaned
	// DNS snapshot only when no listener still owns port 53.
	if _, err := os.Lstat(s.dnsStatePath()); err == nil && s.dnsPorts("127.0.0.1:53") == nil {
		if err := s.restoreDNS(); err != nil {
			fmt.Println("daemon: DNS restoration pending:", err)
		}
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
	go s.monitorVPNConflict()
	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}
		go s.handle(conn)
	}
}

func (s *Server) monitorVPNConflict() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
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
	sc := bufio.NewScanner(c)
	if !sc.Scan() {
		return
	}
	cmd := strings.TrimSpace(sc.Text())
	var reply string
	switch cmd {
	case "connect":
		reply = s.start()
	case "disconnect":
		reply = s.stop()
	case "status":
		reply = s.status()
	case "capabilities":
		reply = "native-dns-v1"
	case "dns-restore":
		reply = s.restoreDNSCommand()
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
	out, err := exec.Command("/usr/sbin/scutil", "--nc", "list").Output()
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
	s.mu.Lock()
	if expected != nil && (s.child != expected || s.restarts != generation) {
		s.mu.Unlock()
		return "restart canceled"
	}
	if s.runningLocked() {
		s.mu.Unlock()
		return "already running"
	}
	s.mu.Unlock()
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
		if err := s.restoreDNS(); err != nil {
			return "start failed: " + err.Error()
		}
	}
	if guard {
		if err := s.dnsPorts("127.0.0.1:53"); err != nil {
			return "start failed: " + err.Error()
		}
	}
	cmd := s.command(singBoxExecutable(), s.cfgPath, s.workDir)
	cmd.Stdout, cmd.Stderr = logWriter{s}, logWriter{s}
	if err := cmd.Start(); err != nil {
		return "start failed: " + err.Error()
	}
	done := make(chan struct{})
	s.mu.Lock()
	s.child, s.childDone, s.started = cmd, done, time.Now()
	s.restarts++
	gen := s.restarts
	s.dnsStatus = "off"
	if guard {
		s.dnsStatus = "starting"
	}
	s.mu.Unlock()
	go s.waitLoop(cmd, gen, done)
	if guard {
		if err := s.protectDNS(dnsCfg, done); err != nil {
			// A core crash-restart retains loopback DNS (fail closed) while
			// the watcher repairs entries. Never fall back to DHCP implicitly.
			if hadState {
				s.mu.Lock()
				s.dnsStatus = "degraded"
				s.mu.Unlock()
				return fmt.Sprintf("connected (pid %d) dns=degraded; protected snapshot retained: %v", cmd.Process.Pid, err)
			}
			if restoreErr := s.restoreDNS(); restoreErr != nil {
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
	s.mu.Lock()
	dnsStatus := s.dnsStatus
	s.mu.Unlock()
	return fmt.Sprintf("connected (pid %d) dns=%s", cmd.Process.Pid, dnsStatus)
}

// singBoxExecutable supports Apple Silicon, Intel, and non-default Homebrew
// prefixes. The installer pins the resolved path in the launchd environment.
func singBoxExecutable() string {
	if binary := os.Getenv("SAKAMOTO_SING_BOX"); binary != "" {
		return binary
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
func (s *Server) waitLoop(cmd *exec.Cmd, gen int, done chan struct{}) {
	err := cmd.Wait()
	close(done)
	s.appendLog(fmt.Sprintf("sing-box exited: %v", err))
	time.Sleep(2 * time.Second)
	s.mu.Lock()
	if s.child == cmd && gen == s.restarts { // Restart only if this process generation is still current.
		s.mu.Unlock()
		s.appendLog("auto-restart")
		s.startGeneration(cmd, gen)
		return
	}
	s.mu.Unlock()
}

func (s *Server) stop() string {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	// Restore before killing the only loopback DNS listener. If restoration
	// fails, retain the core and snapshot rather than strand the machine.
	if err := s.restoreDNS(); err != nil {
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
