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
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/recovery"
)

// This subprocess stands in for the external native core. No live launchd job,
// system DNS, proxy setting, or real VPN is touched by this suite.
func TestRecoveryCoreProcess(t *testing.T) {
	if os.Getenv("SAKAMOTO_RECOVERY_TEST_CHILD") != "1" {
		return
	}
	args := os.Args
	mode := args[len(args)-1]
	if mode == "crash" {
		os.Exit(23)
	}
	for {
		time.Sleep(time.Second)
	}
}

func recoveryOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func recoveryFixture(t *testing.T) (*Server, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "svc-recovery-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	raw := `{"services":[{"type":"api","listen":"127.0.0.1","listen_port":9090,"secret":"offline-recovery-integration-key-0123456789abcdef"}]}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sakamoto.yaml"), []byte("dns_guard:\n  enabled: false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewServer(filepath.Join(dir, "config.json"), dir)
	s.vpnConnected = func() bool { return false }
	s.retryDelay = 15 * time.Millisecond
	s.stablePeriod = time.Hour
	s.epoch = func(context.Context) (time.Time, error) { return time.Now(), nil }
	s.command = func(_, _, _ string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRecoveryCoreProcess$", "--", "wait")
		cmd.Env = append(os.Environ(), "SAKAMOTO_RECOVERY_TEST_CHILD=1")
		return cmd
	}
	t.Cleanup(func() { s.stop() })
	return s, dir
}

func waitRecovery(t *testing.T, predicate func() bool) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("recovery transition deadline exceeded")
}

func TestRecoveryCrashBudgetSurvivesSupervisorReplacement(t *testing.T) {
	s, dir := recoveryFixture(t)
	s.command = func(_, _, _ string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRecoveryCoreProcess$", "--", "crash")
		cmd.Env = append(os.Environ(), "SAKAMOTO_RECOVERY_TEST_CHILD=1")
		return cmd
	}
	s.start()
	waitRecovery(t, func() bool { return strings.Contains(s.status(), "blocked") })
	s.mu.Lock()
	before := s.restarts
	s.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	s.mu.Lock()
	after := s.restarts
	s.mu.Unlock()
	if after != before {
		t.Fatal("blocked core kept restarting")
	}
	replacement := NewServer(filepath.Join(dir, "config.json"), dir)
	if reply := replacement.start(); !strings.Contains(reply, "blocked") {
		t.Fatalf("replacement bypassed persisted circuit: %s", reply)
	}
	if reply := s.resume(); !strings.Contains(reply, "resumed") {
		t.Fatal(reply)
	}
	if recovery.Held(dir) {
		t.Fatal("resume retained maintenance hold")
	}
	artifact := map[string]any{"scenario": "crash-budget", "starts": before, "persists_across_supervisor": true, "blocked": true}
	writeRecoveryArtifact(t, artifact)
}

func TestRecoveryManualStopCancelsDNSStartupAndKeepsStatusResponsive(t *testing.T) {
	s, dir := recoveryFixture(t)
	cfg := `{"services":[{"type":"api","listen":"127.0.0.1","secret":"offline-recovery-integration-key-0123456789abcdef"}],"inbounds":[{"type":"direct","tag":"protected-dns","listen":"127.0.0.1","listen_port":53}],"dns":{"final":"safe","servers":[{"tag":"safe","type":"https","detour":"MainProxy"}]},"route":{"rules":[{"inbound":["protected-dns"],"action":"hijack-dns"}]}}`
	recoveryOK(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0600))
	recoveryOK(t, os.WriteFile(filepath.Join(dir, "sakamoto.yaml"), []byte("dns_guard:\n  enabled: true\n  service: Wi-Fi\n"), 0600))
	entered := make(chan struct{})
	s.dnsPorts = func(string) error { return nil }
	s.dnsProbeContext = func(ctx context.Context, _ string) error { close(entered); <-ctx.Done(); return ctx.Err() }
	done := make(chan string, 1)
	go func() { done <- s.start() }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("startup did not reach DNS readiness")
	}
	client, server := net.Pipe()
	go s.handle(server)
	recoveryOK(t, client.SetDeadline(time.Now().Add(300*time.Millisecond)))
	if _, err := fmt.Fprintln(client, "status"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 512)
	if _, err := client.Read(buf); err != nil {
		t.Fatalf("status blocked behind startup: %v", err)
	}
	recoveryOK(t, client.Close())
	start := time.Now()
	reply := s.stop()
	stopElapsed := time.Since(start)
	if stopElapsed > time.Second || strings.Contains(reply, "failed") {
		t.Fatalf("manual stop did not preempt readiness: %s", reply)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled startup remained blocked")
	}
	if !strings.Contains(s.status(), "disconnected") {
		t.Fatal(s.status())
	}
	time.Sleep(100 * time.Millisecond)
	if !strings.Contains(s.status(), "disconnected") {
		t.Fatal("stale retry undid manual stop")
	}
	writeRecoveryArtifact(t, map[string]any{"scenario": "cancel-startup", "manual_stop_ms": stopElapsed.Milliseconds(), "status_responsive": true})
}

func TestRecoveryMaintenanceSurvivesNewServerAndNeedsExplicitResume(t *testing.T) {
	s, dir := recoveryFixture(t)
	if err := recovery.Hold(dir, "offline rescue"); err != nil {
		t.Fatal(err)
	}
	for _, server := range []*Server{s, NewServer(filepath.Join(dir, "config.json"), dir)} {
		if reply := server.start(); !strings.Contains(reply, "maintenance") {
			t.Fatalf("maintenance takeover: %s", reply)
		}
	}
	if reply := s.resume(); !strings.Contains(reply, "resumed") {
		t.Fatal(reply)
	}
	if reply := s.start(); !strings.HasPrefix(reply, "connected") {
		t.Fatal(reply)
	}
}

func TestRecoveryKnownWorkingBundleSurvivesMissingRuleAndRollsBackOnce(t *testing.T) {
	s, dir := recoveryFixture(t)
	rule := filepath.Join(dir, "test-rule.json")
	recoveryOK(t, os.WriteFile(rule, []byte(`{"version":3,"rules":[]}`), 0600))
	var cfg map[string]any
	raw, _ := os.ReadFile(s.cfgPath)
	recoveryOK(t, json.Unmarshal(raw, &cfg))
	cfg["route"] = map[string]any{"rule_set": []any{map[string]any{"type": "local", "tag": "test", "format": "source", "path": rule}}}
	raw, _ = json.Marshal(cfg)
	recoveryOK(t, os.WriteFile(s.cfgPath, raw, 0600))
	s.stablePeriod = 60 * time.Millisecond
	epoch := time.Now()
	s.epoch = func(context.Context) (time.Time, error) { return epoch, nil }
	if reply := s.start(); !strings.HasPrefix(reply, "connected") {
		t.Fatal(reply)
	}
	waitRecovery(t, func() bool { _, err := os.Stat(filepath.Join(dir, "recovery", "last-good.json")); return err == nil })
	s.stop()
	recoveryOK(t, os.Remove(rule))
	recoveryOK(t, os.WriteFile(s.cfgPath, []byte("invalid candidate"), 0600))
	if reply := s.rollback(); !strings.Contains(reply, "restored") {
		t.Fatal(reply)
	}
	restored, _ := os.ReadFile(s.cfgPath)
	if !json.Valid(restored) {
		t.Fatal("rollback did not restore valid configuration")
	}
	var restoredCfg struct {
		Route struct {
			RuleSets []struct{ Path string } `json:"rule_set"`
		} `json:"route"`
	}
	recoveryOK(t, json.Unmarshal(restored, &restoredCfg))
	if _, err := os.Stat(restoredCfg.Route.RuleSets[0].Path); err != nil {
		t.Fatal("rollback lost referenced rule:", err)
	}
	if reply := s.rollback(); !strings.Contains(reply, "already") {
		t.Fatalf("repeated rollback allowed: %s", reply)
	}
	writeRecoveryArtifact(t, map[string]any{"scenario": "known-working-rollback", "referenced_rule_restored": true, "one_rollback": true})
}

func writeRecoveryArtifact(t *testing.T, data map[string]any) {
	t.Helper()
	if dir := os.Getenv("SAKAMOTO_RECOVERY_ARTIFACT_DIR"); dir != "" {
		recoveryOK(t, os.MkdirAll(dir, 0700))
		raw, _ := json.MarshalIndent(data, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, t.Name()+".json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
