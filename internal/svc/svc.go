// Package svc — root 常驻监督进程：unix socket 接受 connect/disconnect/status，
// 管理 sing-box 子进程（等价于 SR 的 VPN 开关，且免每次 sudo）。
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
	"syscall"
	"time"
)

const SockName = "svc.sock"

func SockPath() string { return filepath.Join(config.DefaultDir(), SockName) }

// Server 是 root 监督进程本体。
type Server struct {
	cfgPath  string
	workDir  string
	mu       sync.Mutex
	child    *exec.Cmd
	started  time.Time
	restarts int
	logs     []string
}

func NewServer(cfgPath, workDir string) *Server {
	return &Server{cfgPath: cfgPath, workDir: workDir}
}

// Run 阻塞：起 socket 服务；不自动起 sing-box（首次 connect 才拉起）。
func (s *Server) Run() error {
	sock := filepath.Join(s.workDir, SockName)
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
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
	// 只授权指定用户。绝不能让其他本机账户控制 root 进程。
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
	// Shadowrocket 在 sing-box 之后重新连接时，优先保留 Shadowrocket，撤销我们的 TUN。
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
		if !shadowrocketVPNConnected() {
			continue
		}
		s.mu.Lock()
		running := s.child != nil && s.child.ProcessState == nil
		s.mu.Unlock()
		if running {
			s.appendLog("Shadowrocket VPN 已连接，停止 sing-box 以避免双 TUN 冲突")
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
func (s *Server) start() string {
	if shadowrocketVPNConnected() {
		return "start failed: 请先断开 Shadowrocket VPN（不能同时运行两个 TUN）"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.child != nil && s.child.ProcessState == nil {
		return "already running"
	}
	if err := validateAPIService(s.cfgPath); err != nil {
		return "start failed: " + err.Error()
	}
	cmd := exec.Command(singBoxExecutable(), "run",
		"-c", s.cfgPath, "-D", s.workDir)
	cmd.Stdout = logWriter{s}
	cmd.Stderr = logWriter{s}
	if err := cmd.Start(); err != nil {
		return "start failed: " + err.Error()
	}
	s.child = cmd
	s.started = time.Now()
	s.restarts++
	go s.waitLoop(cmd, s.restarts)
	return fmt.Sprintf("connected (pid %d)", cmd.Process.Pid)
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

// waitLoop 子进程退出后自动重启（KeepAlive 兜底，退避防抖）。
func (s *Server) waitLoop(cmd *exec.Cmd, gen int) {
	err := cmd.Wait()
	s.appendLog(fmt.Sprintf("sing-box exited: %v", err))
	time.Sleep(2 * time.Second)
	s.mu.Lock()
	if s.child == cmd && gen == s.restarts { // 仍是当前代，没被 disconnect 换掉
		s.mu.Unlock()
		s.appendLog("auto-restart")
		s.start()
		return
	}
	s.mu.Unlock()
}

func (s *Server) stop() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.child == nil || s.child.ProcessState != nil {
		return "not running"
	}
	s.restarts++ // 让 waitLoop 判定代际失效，不再自动重启
	pid := s.child.Process.Pid
	// SIGTERM 让 sing-box 撤销 TUN 路由、释放 DNS，而不是 SIGKILL 硬切断。
	if err := s.child.Process.Signal(syscall.SIGTERM); err != nil {
		return "disconnect failed: " + err.Error()
	}
	s.child = nil
	return fmt.Sprintf("disconnected (pid %d)", pid)
}

func (s *Server) status() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.child != nil && s.child.ProcessState == nil {
		return fmt.Sprintf("connected pid=%d up=%s restarts=%d",
			s.child.Process.Pid, time.Since(s.started).Round(time.Second), s.restarts)
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
