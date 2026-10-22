package svc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecoverySocketIsResponsiveDuringOrphanRestore(t *testing.T) {
	s, dir := recoveryFixture(t)
	recoveryOK(t, os.WriteFile(filepath.Join(dir, "dns-restore.json"), []byte(`{"service":"Test Wi-Fi","servers":[]}`), 0600))
	entered := make(chan struct{})
	s.dnsPorts = func(string) error { return nil }
	s.dnsActivateRun = func(ctx context.Context, _ ...string) (string, error) {
		close(entered)
		<-ctx.Done()
		return "", ctx.Err()
	}
	s.dnsRun = func(...string) (string, error) { return "There aren't any DNS Servers set on Test Wi-Fi.\n", nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.RunContext(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("orphan cleanup did not start")
	}
	before := time.Now()
	if reply, err := SendTo(filepath.Join(dir, SockName), "status"); err != nil || !strings.HasPrefix(reply, "disconnected") {
		t.Fatal("status unavailable during cleanup", reply, err)
	}
	if reply, err := SendTo(filepath.Join(dir, SockName), "disconnect"); err != nil || !strings.Contains(reply, "not running") {
		t.Fatal("stop could not cancel orphan cleanup", reply, err)
	}
	if time.Since(before) > time.Second {
		t.Fatal("orphan cleanup blocked control deadline")
	}
	writeRecoveryArtifact(t, map[string]any{"scenario": "orphaned-supervisor", "status_responsive": true, "cleanup_canceled": true, "control_ms": time.Since(before).Milliseconds()})
}

func TestRecoveryFailedDNSRestoreRetainsCoreCrashMonitoring(t *testing.T) {
	s, dir := recoveryFixture(t)
	if reply := s.start(); !strings.HasPrefix(reply, "connected") {
		t.Fatal(reply)
	}
	recoveryOK(t, os.WriteFile(filepath.Join(dir, "dns-restore.json"), []byte(`{"service":"Test Wi-Fi","servers":[]}`), 0600))
	s.dnsRun = func(...string) (string, error) { return "", errors.New("network service temporarily unavailable") }
	s.dnsActivateRun = func(context.Context, ...string) (string, error) {
		return "There aren't any DNS Servers set on Test Wi-Fi.\n", nil
	}
	t.Cleanup(func() {
		s.dnsRun = func(...string) (string, error) { return "There aren't any DNS Servers set on Test Wi-Fi.\n", nil }
	})
	if reply := s.stop(); !strings.Contains(reply, "failed") {
		t.Fatal("failed restoration reported success", reply)
	}
	s.mu.Lock()
	old := s.child.Process.Pid
	err := s.child.Process.Kill()
	s.mu.Unlock()
	recoveryOK(t, err)
	waitRecovery(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.runningLocked() && s.child.Process.Pid != old })
	writeRecoveryArtifact(t, map[string]any{"scenario": "failed-dns-restoration", "core_retained": true, "later_crash_still_recovered": true})
}
