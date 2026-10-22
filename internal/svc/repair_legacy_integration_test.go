package svc

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A real Unix legacy protocol fixture starts a real native API process. The
// new CLI must also repair installed supervisors that predate repair RPC.
func TestRepairLegacySupervisorStartsAndVerifiesNativeCore(t *testing.T) {
	native, err := exec.LookPath(singBoxExecutable())
	recoveryOK(t, err)
	dir, err := os.MkdirTemp("/tmp", "legacy-repair-")
	recoveryOK(t, err)
	defer func() { _ = os.RemoveAll(dir) }()
	portListener, err := net.Listen("tcp", "127.0.0.1:0")
	recoveryOK(t, err)
	port := portListener.Addr().(*net.TCPAddr).Port
	recoveryOK(t, portListener.Close())
	raw := fmt.Sprintf(`{"log":{"disabled":true},"services":[{"type":"api","listen":"127.0.0.1","listen_port":%d,"secret":"%s"}],"outbounds":[{"type":"direct","tag":"direct"}]}`, port, strings.Repeat("l", 48))
	path := filepath.Join(dir, "sakamoto.yaml")
	recoveryOK(t, os.WriteFile(path, []byte("dns_guard:\n  enabled: false\n"), 0600))
	recoveryOK(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte(raw), 0600))
	socket, err := net.Listen("unix", filepath.Join(dir, SockName))
	recoveryOK(t, err)
	defer func() { _ = socket.Close() }()
	var mu sync.Mutex
	var core *exec.Cmd
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		if core != nil {
			_ = core.Process.Kill()
			_ = core.Wait()
		}
	}()
	done := make(chan struct{})
	defer func() { _ = socket.Close(); <-done }()
	go func() {
		defer close(done)
		for {
			conn, err := socket.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(conn).ReadString('\n')
			command := strings.TrimSpace(line)
			mu.Lock()
			reply := "unknown: " + command
			switch command {
			case "status":
				reply = "disconnected"
				if core != nil {
					reply = fmt.Sprintf("connected pid=%d dns=off", core.Process.Pid)
				}
			case "connect":
				core = exec.Command(native, "run", "-D", dir, "-c", filepath.Join(dir, "config.json"))
				if err := core.Start(); err != nil {
					reply = "start failed: " + err.Error()
					core = nil
				} else {
					reply = "connected"
				}
			}
			mu.Unlock()
			_, _ = fmt.Fprintln(conn, reply)
			_ = conn.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := RepairAt(ctx, path, RepairOptions{})
	recoveryOK(t, err)
	again, err := RepairAt(ctx, path, RepairOptions{CheckOnly: true})
	recoveryOK(t, err)
	if !result.Verified || result.PID == 0 || again.PID != result.PID || again.Action != "none" {
		t.Fatal("legacy repair failed or restarted a healthy core", result, again)
	}
	writeRecoveryArtifact(t, map[string]any{"scenario": "quick-repair-legacy-supervisor", "real_unix_socket": true, "native_api_verified": true, "healthy_pid_retained": true})
}
