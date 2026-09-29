package watch

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sagernet/sing-box/daemon"
)

const recoverySocketName = "watch.sock"
const recoveryLockName = "watch.lock"
const recoveryCooldown = 90 * time.Second

type recoveryRequest struct {
	ctx   context.Context
	reply chan string
}

func recoveryPath(cfgPath string) string {
	return filepath.Join(filepath.Dir(cfgPath), recoverySocketName)
}

func recoveryDecision(cfgEnabled bool, chain []string, snap *daemon.Groups, settling bool, last, now time.Time) string {
	if !cfgEnabled || len(chain) < 2 {
		return "disabled"
	}
	if snap == nil {
		return "unavailable"
	}
	main := findGroup(snap, "MainProxy")
	if main == nil {
		return "unavailable"
	}
	if indexOf(chain, main.Selected) < 0 {
		return "manual"
	}
	for _, tag := range chain {
		if findGroup(snap, tag) == nil {
			return "unavailable"
		}
	}
	if settling {
		return "busy"
	}
	if !last.IsZero() && now.Sub(last) < recoveryCooldown {
		return "cooldown"
	}
	return "queued"
}

// RequestRecovery asks the unprivileged watcher to run fresh group URL tests.
// It does not contact the root supervisor, reveal the API secret or switch a
// selector itself. The short reply reports acceptance, not network recovery.
func RequestRecovery(cfgPath string) (string, error) {
	conn, err := net.DialTimeout("unix", recoveryPath(cfgPath), 2*time.Second)
	if err != nil {
		return "", fmt.Errorf("sakamoto watcher is unavailable: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(4 * time.Second)); err != nil {
		return "", err
	}
	if _, err := fmt.Fprintln(conn, "recover"); err != nil {
		return "", err
	}
	line, err := bufio.NewReaderSize(conn, 128).ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read watcher response: %w", err)
	}
	status := strings.TrimSpace(line)
	switch status {
	case "queued", "busy", "cooldown", "manual", "disabled", "unavailable":
		return status, nil
	default:
		return "", fmt.Errorf("unrecognized watcher response %q", status)
	}
}

// serveRecovery owns the user-only socket for the lifetime of the watcher.
// Readiness is mandatory: a second watcher must not run another fallback loop.
func (w *Watcher) serveRecovery(ctx context.Context, ready chan<- error) {
	started := false
	defer func() {
		if !started {
			ready <- fmt.Errorf("cannot acquire the single-watcher recovery socket")
		}
	}()
	dir := filepath.Dir(w.cfgPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		w.emit("error", "recovery socket directory: %v", err)
		return
	}
	lock, err := os.OpenFile(filepath.Join(dir, recoveryLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		w.emit("error", "recovery watcher lock: %v", err)
		return
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		w.emit("error", "another recovery watcher owns the lock: %v", err)
		return
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	path := recoveryPath(w.cfgPath)
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			w.emit("error", "refusing to remove non-socket %s", path)
			return
		}
		if probe, e := net.DialTimeout("unix", path, 150*time.Millisecond); e == nil {
			_ = probe.Close()
			w.emit("error", "recovery watcher already listening")
			return
		}
		if err := os.Remove(path); err != nil {
			w.emit("error", "remove stale recovery socket: %v", err)
			return
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		w.emit("error", "inspect recovery socket: %v", err)
		return
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		w.emit("error", "listen recovery socket: %v", err)
		return
	}
	defer func() { _ = listener.Close(); _ = os.Remove(path) }()
	if err := os.Chmod(path, 0o600); err != nil {
		w.emit("error", "protect recovery socket: %v", err)
		return
	}
	go func() { <-ctx.Done(); _ = listener.Close() }()
	ready <- nil
	started = true
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			w.emit("error", "accept recovery request: %v", err)
			continue
		}
		go w.handleRecovery(ctx, conn)
	}
}

func (w *Watcher) handleRecovery(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64), 64)
	if !scanner.Scan() || scanner.Text() != "recover" {
		_, _ = fmt.Fprintln(conn, "unavailable")
		return
	}
	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req := recoveryRequest{ctx: requestCtx, reply: make(chan string, 1)}
	select {
	case w.recoveryCh <- req:
	case <-requestCtx.Done():
		_, _ = fmt.Fprintln(conn, "unavailable")
		return
	default:
		_, _ = fmt.Fprintln(conn, "busy")
		return
	}
	select {
	case response := <-req.reply:
		_, _ = fmt.Fprintln(conn, response)
	case <-requestCtx.Done():
		_, _ = fmt.Fprintln(conn, "unavailable")
	}
}
