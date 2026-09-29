package svc

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Send sends one command to the supervisor socket and returns its reply.
func Send(cmd string) (string, error) {
	conn, err := net.DialTimeout("unix", SockPath(), 3*time.Second)
	if err != nil {
		return "", fmt.Errorf("supervisor is not running (start its LaunchDaemon first): %w", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
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

var statusPID = regexp.MustCompile(`\bpid=(\d+)`)

// Reconnect waits for the old sing-box process to release its TUN and listener
// before asking the root daemon to start the checked on-disk config.
func Reconnect() error {
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
