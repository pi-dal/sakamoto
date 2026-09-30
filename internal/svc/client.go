package svc

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
)

// Send sends one command to the supervisor socket and returns its reply.
func Send(cmd string) (string, error) {
	conn, err := net.DialTimeout("unix", SockPath(), 3*time.Second)
	if err != nil {
		return "", fmt.Errorf("supervisor is not running (start its LaunchDaemon first): %w", err)
	}
	defer func() { _ = conn.Close() }()
	timeout := 5 * time.Second
	if cmd == "connect" {
		timeout = 70 * time.Second
	}
	if cmd == "disconnect" || cmd == "dns-restore" {
		timeout = 30 * time.Second
	}
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return "", err
	}
	if _, err := fmt.Fprintln(conn, cmd); err != nil {
		return "", err
	}
	buf := make([]byte, 64<<10)
	n, err := conn.Read(buf)
	if err != nil && n == 0 {
		return "", err
	}
	return string(buf[:n]), nil
}

// RequireDNSLifecycle prevents a new client from mistaking an old root daemon
// (which cannot save/restore DNS) for a safe protected-DNS controller.
func RequireDNSLifecycle() error {
	reply, err := Send("capabilities")
	if err != nil {
		return err
	}
	if strings.TrimSpace(reply) != "native-dns-v1" {
		return fmt.Errorf("root daemon lacks native DNS lifecycle support; upgrade it while disconnected")
	}
	return nil
}

var statusPID = regexp.MustCompile(`\bpid=(\d+)`)

// Reconnect waits for the old sing-box process to release its TUN and listener
// before asking the root daemon to start the checked on-disk config.
func Reconnect() error {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return err
	}
	if cfg.DNSGuard.Enabled {
		if err := RequireDNSLifecycle(); err != nil {
			return err
		}
	}
	status, err := Send("status")
	if err != nil {
		return err
	}
	if strings.HasPrefix(status, "connected") {
		old := statusPID.FindStringSubmatch(status)
		reply, e := Send("disconnect")
		if e != nil || !strings.HasPrefix(reply, "disconnected") {
			return fmt.Errorf("disconnect: %q: %v", strings.TrimSpace(reply), e)
		}
		if len(old) == 2 {
			pid, _ := strconv.Atoi(old[1])
			stopped := false
			for i := 0; i < 100; i++ {
				if e := syscall.Kill(pid, 0); e == syscall.ESRCH {
					stopped = true
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if !stopped {
				return fmt.Errorf("old sing-box %d has not exited; refusing overlapping TUNs", pid)
			}
		}
	}
	reply, err := Send("connect")
	if err != nil || !strings.HasPrefix(reply, "connected") {
		return fmt.Errorf("connect: %q: %v", strings.TrimSpace(reply), err)
	}
	return nil
}
