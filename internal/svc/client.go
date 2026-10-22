package svc

import (
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
)

// Send sends one command to the supervisor socket and returns its reply.
func Send(cmd string) (string, error) {
	return SendTo(SockPath(), cmd)
}

func SendTo(socketPath, cmd string) (string, error) {
	return SendToContext(context.Background(), socketPath, cmd)
}

func SendToContext(ctx context.Context, socketPath, cmd string) (string, error) {
	dialer := net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return "", fmt.Errorf("supervisor is not running (start its LaunchDaemon first): %w", err)
	}
	defer func() { _ = conn.Close() }()
	timeout := 5 * time.Second
	verb, _, _ := strings.Cut(cmd, " ")
	if verb == "state" || verb == "urltest" || verb == "select" || verb == "reload" {
		timeout = 15 * time.Second
	}
	if cmd == "connect" || verb == "repair" {
		timeout = 70 * time.Second
	}
	if cmd == "disconnect" || cmd == "dns-restore" {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := conn.SetDeadline(deadline); err != nil {
		return "", err
	}
	if _, err := fmt.Fprintln(conn, cmd); err != nil {
		return "", err
	}
	buf, err := io.ReadAll(io.LimitReader(conn, (1<<20)+1))
	if err != nil {
		return "", err
	}
	if len(buf) > 1<<20 {
		return "", fmt.Errorf("supervisor reply exceeds 1 MiB")
	}
	return string(buf), nil
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
func commandReplyError(command, reply string, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %q: %w", command, strings.TrimSpace(reply), err)
	}
	return fmt.Errorf("%s: %q", command, strings.TrimSpace(reply))
}

func Reconnect() error {
	return ReconnectAt(config.DefaultPath())
}

// ReconnectAt keeps custom runtimes and integration fixtures on their own socket.
func ReconnectAt(cfgPath string) error {
	send := func(command string) (string, error) {
		return SendTo(filepath.Join(filepath.Dir(cfgPath), SockName), command)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if cfg.DNSGuard.Enabled {
		reply, err := send("capabilities")
		if err != nil {
			return err
		}
		if strings.TrimSpace(reply) != "native-dns-v1" {
			return fmt.Errorf("root daemon lacks native DNS lifecycle support; upgrade it while disconnected")
		}
	}
	status, err := send("status")
	if err != nil {
		return err
	}
	if strings.HasPrefix(status, "connected") {
		old := statusPID.FindStringSubmatch(status)
		reply, e := send("disconnect")
		if e != nil || !strings.HasPrefix(reply, "disconnected") {
			return commandReplyError("disconnect", reply, e)
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
	reply, err := send("connect")
	if err != nil || !strings.HasPrefix(reply, "connected") {
		return commandReplyError("connect", reply, err)
	}
	return nil
}
