package svc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/recovery"
)

// Real native process + Unix control socket, loopback only; never creates a TUN
// or changes the owner's network. Failures include crash, retired update path,
// live API failure, invalid candidate and exhausted recovery without a bundle.
func TestRepairNativeCoreCrashUpdateAndRollback(t *testing.T) {
	native, err := exec.LookPath(singBoxExecutable())
	recoveryOK(t, err)
	dir, err := os.MkdirTemp("/tmp", "repair-e2e-")
	recoveryOK(t, err)
	defer func() { _ = os.RemoveAll(dir) }()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	recoveryOK(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	recoveryOK(t, listener.Close())
	raw := []byte(fmt.Sprintf(`{"log":{"disabled":true},"services":[{"type":"api","listen":"127.0.0.1","listen_port":%d,"secret":"%s"}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`, port, strings.Repeat("r", 48)))
	cfgPath := filepath.Join(dir, "sakamoto.yaml")
	recoveryOK(t, os.WriteFile(cfgPath, []byte("dns_guard:\n  enabled: false\n"), 0600))
	recoveryOK(t, os.WriteFile(filepath.Join(dir, "config.json"), raw, 0600))
	t.Setenv("SAKAMOTO_SING_BOX", native)
	s := NewServer(filepath.Join(dir, "config.json"), dir)
	s.vpnConnected = func() bool { return false }
	s.stablePeriod = 150 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.RunContext(ctx) }()
	defer func() { cancel(); <-done }()
	waitRecovery(t, func() bool { _, err := os.Stat(filepath.Join(dir, SockName)); return err == nil })
	check, err := RepairAt(ctx, cfgPath, RepairOptions{CheckOnly: true})
	if err == nil || check.Verified {
		t.Fatal("stopped core reported healthy", check, err)
	}
	first, err := RepairAt(ctx, cfgPath, RepairOptions{})
	recoveryOK(t, err)
	if !first.Verified || first.PID == 0 {
		t.Fatal("repair did not start and verify native core", first)
	}
	again, err := RepairAt(ctx, cfgPath, RepairOptions{})
	recoveryOK(t, err)
	if again.PID != first.PID || again.Action != "none" {
		t.Fatal("healthy repair restarted core", first, again)
	}
	waitRecovery(t, func() bool { _, err := os.Stat(filepath.Join(dir, "recovery", "last-good.json")); return err == nil })
	s.mu.Lock()
	s.stablePeriod = time.Hour
	s.mu.Unlock()
	crash := func() {
		s.lifecycle.Lock()
		recoveryOK(t, s.saveCircuitLocked(recovery.Circuit{Blocked: true}))
		s.mu.Lock()
		err := s.child.Process.Kill()
		s.mu.Unlock()
		s.lifecycle.Unlock()
		recoveryOK(t, err)
		waitRecovery(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.runningLocked() })
	}
	// An update removed the configured Cellar executable. Find the installed core
	// through its stable link/PATH rather than retrying the retired path forever.
	t.Setenv("SAKAMOTO_SING_BOX", filepath.Join(dir, "removed-keg", "sing-box"))
	crash()
	recovered, err := RepairAt(ctx, cfgPath, RepairOptions{})
	recoveryOK(t, err)
	if !recovered.Verified || recovered.PID == first.PID {
		t.Fatal("crashed/update core not recovered", recovered)
	}
	var changed map[string]any
	recoveryOK(t, json.Unmarshal(raw, &changed))
	changed["services"].([]any)[0].(map[string]any)["secret"] = strings.Repeat("s", 48)
	staged, _ := json.Marshal(changed)
	recoveryOK(t, os.WriteFile(s.cfgPath, staged, 0600))
	degraded, err := RepairAt(ctx, cfgPath, RepairOptions{})
	if err == nil || degraded.State != "degraded" || degraded.PID != recovered.PID {
		t.Fatal("API-only failure restarted live core", degraded, err)
	}
	restarted, err := RepairAt(ctx, cfgPath, RepairOptions{Restart: true})
	recoveryOK(t, err)
	if !restarted.Verified || restarted.PID == recovered.PID {
		t.Fatal("explicit restart did not repair API", restarted)
	}
	s.mu.Lock()
	paused := s.child
	s.mu.Unlock()
	recoveryOK(t, paused.Process.Signal(syscall.SIGSTOP))
	hung, err := RepairAt(ctx, cfgPath, RepairOptions{Restart: true})
	recoveryOK(t, err)
	if !hung.Verified || hung.PID == restarted.PID {
		t.Fatal("owned hung core was not replaced", hung)
	}
	failingUpdate := filepath.Join(dir, "updated-core")
	recoveryOK(t, os.WriteFile(failingUpdate, []byte("#!/bin/sh\nexit 23\n"), 0700))
	t.Setenv("SAKAMOTO_SING_BOX", failingUpdate)
	crash()
	early, err := RepairAt(ctx, cfgPath, RepairOptions{})
	recoveryOK(t, err)
	if !early.Verified || early.Action != "rolled_back" {
		t.Fatal("new engine exited before API readiness without verified rollback", early)
	}
	crash()
	recoveryOK(t, os.WriteFile(s.cfgPath, []byte("invalid updated configuration"), 0600))
	rolled, err := RepairAt(ctx, cfgPath, RepairOptions{})
	recoveryOK(t, err)
	if !rolled.Verified || rolled.Action != "rolled_back" {
		t.Fatal("bad update did not use known-working bundle", rolled)
	}
	crash()
	recoveryOK(t, os.Remove(filepath.Join(dir, "recovery", "last-good.json")))
	recoveryOK(t, os.WriteFile(s.cfgPath, []byte("invalid without fallback"), 0600))
	failed, err := RepairAt(ctx, cfgPath, RepairOptions{})
	if err == nil || failed.Verified {
		t.Fatal("failed repair returned success", failed, err)
	}
	s.mu.Lock()
	blocked := s.circuit.Blocked
	s.mu.Unlock()
	if !blocked {
		t.Fatal("failed repair left automatic retry enabled")
	}
	writeRecoveryArtifact(t, map[string]any{"scenario": "quick-core-repair", "real_native_core": true, "healthy_pid_retained": true, "retired_update_path_recovered": true, "api_outage_retained_live_core": true, "explicit_restart_verified": true, "owned_hung_process_replaced": true, "early_engine_exit_rolled_back": true, "bad_update_rolled_back": true, "unrecoverable_stopped": true})
}
