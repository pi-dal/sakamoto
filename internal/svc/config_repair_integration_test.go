package svc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/recovery"
)

// Exercise real native validation, API processes and Unix repair requests.
// The launcher removes TUN inbounds from its isolated process copy only.
func TestRepairConfigurationFallbackAndRegeneration(t *testing.T) {
	native, err := exec.LookPath(singBoxExecutable())
	recoveryOK(t, err)
	dir, err := os.MkdirTemp("/tmp", "config-repair-")
	recoveryOK(t, err)
	defer func() { _ = os.RemoveAll(dir) }()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	recoveryOK(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	recoveryOK(t, l.Close())
	cfg := config.Default()
	cfg.API.URL = fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg.ConfPath = filepath.Join(dir, "source.conf")
	cfg.NodesFile = filepath.Join(dir, "nodes.txt")
	cfg.TailscaleOptimize = false
	path := filepath.Join(dir, "sakamoto.yaml")
	recoveryOK(t, cfg.Save(path))
	conf := []byte("[General]\ndns-server=https://1.1.1.1/dns-query\n[Rule]\nDOMAIN,ads.example,REJECT\nDOMAIN,proxy.example,PROXY\nDOMAIN,direct.example,DIRECT\nFINAL,DIRECT\n")
	nodes := []byte("socks://fixture:fixture@127.0.0.1:9#LocalExit\n")
	recoveryOK(t, os.WriteFile(cfg.ConfPath, conf, 0600))
	recoveryOK(t, os.WriteFile(cfg.NodesFile, nodes, 0600))
	raw := []byte(fmt.Sprintf(`{"log":{"disabled":true},"services":[{"type":"api","listen":"127.0.0.1","listen_port":%d,"secret":%q}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`, port, cfg.API.Secret))
	recoveryOK(t, os.WriteFile(filepath.Join(dir, "config.json"), raw, 0600))
	t.Setenv("SAKAMOTO_SING_BOX", native)
	s := NewServer(filepath.Join(dir, "config.json"), dir)
	s.vpnConnected = func() bool { return false }
	s.stablePeriod = 150 * time.Millisecond
	launches := 0
	s.command = func(binary, path, work string) *exec.Cmd {
		data, readErr := os.ReadFile(path)
		recoveryOK(t, readErr)
		var value map[string]any
		recoveryOK(t, json.Unmarshal(data, &value))
		value["inbounds"] = []any{}
		value["log"] = map[string]any{"disabled": true}
		safe, marshalErr := json.Marshal(value)
		recoveryOK(t, marshalErr)
		launches++
		isolated := filepath.Join(work, fmt.Sprintf("isolated-process-%d.json", launches))
		recoveryOK(t, os.WriteFile(isolated, safe, 0600))
		command := exec.Command(binary, "run", "-D", work, "-c", isolated)
		command.Dir = work
		return command
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.RunContext(ctx) }()
	defer func() { cancel(); <-done }()
	waitRecovery(t, func() bool { _, err := os.Stat(filepath.Join(dir, SockName)); return err == nil })
	first, err := RepairAt(ctx, path, RepairOptions{})
	recoveryOK(t, err)
	waitRecovery(t, func() bool { _, err := os.Stat(filepath.Join(dir, "recovery", "last-good.json")); return err == nil })
	s.mu.Lock()
	s.stablePeriod = time.Hour
	s.mu.Unlock()
	snapshot := func() map[string][]byte {
		files := map[string][]byte{}
		for _, name := range []string{"config.json", "sakamoto.yaml", "rules/reject.srs", "recovery/last-good.json"} {
			b, e := os.ReadFile(filepath.Join(dir, name))
			if e == nil {
				files[name] = b
			}
		}
		return files
	}
	unchanged := func(before map[string][]byte, pid int) {
		t.Helper()
		for name, want := range before {
			got, e := os.ReadFile(filepath.Join(dir, name))
			recoveryOK(t, e)
			if !bytes.Equal(got, want) {
				t.Fatalf("failed preparation replaced %s", name)
			}
		}
		s.mu.Lock()
		alive := s.runningLocked()
		got := 0
		if alive {
			got = s.child.Process.Pid
		}
		s.mu.Unlock()
		if !alive || got != pid {
			t.Fatalf("preparation interrupted core: before=%d after=%d", pid, got)
		}
	}
	recoveryOK(t, ValidateConfigurationForUpdate(ctx, path))
	invalidDisk := bytes.Replace(raw, []byte(`"disabled":true`), []byte(`"level":"invalid-recovery-level"`), 1)
	recoveryOK(t, os.WriteFile(s.cfgPath, invalidDisk, 0600))
	preflightFiles := snapshot()
	if ValidateConfigurationForUpdate(ctx, path) == nil {
		t.Fatal("service reconnect accepted invalid on-disk configuration")
	}
	unchanged(preflightFiles, first.PID)
	recoveryOK(t, os.WriteFile(s.cfgPath, raw, 0600))
	savedSecret := cfg.API.Secret
	cfg.API.Secret = strings.Repeat("m", 48)
	changedIdentity, err := cfg.MarshalYAML()
	recoveryOK(t, err)
	recoveryOK(t, os.WriteFile(path, changedIdentity, 0600))
	mismatch, mismatchErr := RepairAt(ctx, path, RepairOptions{CheckOnly: true})
	if mismatchErr == nil || mismatch.Problem != "config_mismatch" || mismatch.Verified {
		t.Fatal("mismatched GUI/API identity reported healthy", mismatch, mismatchErr)
	}
	cfg.API.Secret = savedSecret
	restoredIdentity, err := cfg.MarshalYAML()
	recoveryOK(t, err)
	recoveryOK(t, os.WriteFile(path, restoredIdentity, 0600))
	refused, err := RepairAt(ctx, path, RepairOptions{Regenerate: true})
	if err == nil || refused.Problem != "reconnect_required" {
		t.Fatal("configuration replacement lacked reconnect consent", refused, err)
	}
	unchanged(snapshot(), first.PID)
	recoveryOK(t, os.WriteFile(cfg.NodesFile, nil, 0600))
	before := snapshot()
	failed, err := RepairAt(ctx, path, RepairOptions{Regenerate: true, Restart: true})
	if err == nil || failed.Problem != "generation_failed" {
		t.Fatal("empty generation accepted", failed, err)
	}
	unchanged(before, first.PID)
	recoveryOK(t, os.WriteFile(cfg.NodesFile, nodes, 0600))
	cfg.LogLevel = "invalid-recovery-level"
	recoveryOK(t, cfg.Save(path))
	before = snapshot()
	failed, err = RepairAt(ctx, path, RepairOptions{Regenerate: true, Restart: true})
	if err == nil || failed.Problem != "config_invalid" {
		t.Fatal("invalid native candidate accepted", failed, err)
	}
	unchanged(before, first.PID)
	cfg.LogLevel = "info"
	recoveryOK(t, cfg.Save(path))
	recoveryOK(t, os.WriteFile(filepath.Join(dir, "api-rotation.pending.json"), []byte(`{"secret":"pending-is-not-consumed"}`), 0600))
	before = snapshot()
	failed, err = RepairAt(ctx, path, RepairOptions{Regenerate: true, Restart: true})
	if err == nil || failed.Problem != "pending_api_rotation" {
		t.Fatal("pending key was overwritten", failed, err)
	}
	unchanged(before, first.PID)
	recoveryOK(t, os.Remove(filepath.Join(dir, "api-rotation.pending.json")))
	s.mu.Lock()
	initialDone := s.childDone
	s.mu.Unlock()
	if reply := s.stop(); !strings.HasPrefix(reply, "disconnected") {
		t.Fatal(reply)
	}
	select {
	case <-initialDone:
	case <-time.After(3 * time.Second):
		t.Fatal("initial core did not exit")
	}
	recoveryOK(t, os.WriteFile(s.cfgPath, []byte("broken JSON"), 0600))
	recoveryOK(t, os.WriteFile(path, []byte("broken: [sidecar"), 0600))
	restored, err := RepairAt(ctx, path, RepairOptions{Fallback: true})
	recoveryOK(t, err)
	if !restored.Verified || restored.Action != "rolled_back" || restored.PID == first.PID {
		t.Fatal("broken JSON and sidecar did not recover", restored)
	}
	pointer, err := os.ReadFile(filepath.Join(dir, "recovery", "last-good.json"))
	recoveryOK(t, err)
	regenerated, err := RepairAt(ctx, path, RepairOptions{Regenerate: true, Restart: true})
	recoveryOK(t, err)
	if !regenerated.Verified || regenerated.Action != "regenerated" || regenerated.PID == restored.PID {
		t.Fatal("generation did not activate and verify", regenerated)
	}
	current, err := os.ReadFile(s.cfgPath)
	recoveryOK(t, err)
	var doc struct {
		Services []struct {
			ListenPort int    `json:"listen_port"`
			Secret     string `json:"secret"`
		} `json:"services"`
		Route struct {
			RuleSet []struct {
				Path string `json:"path"`
			} `json:"rule_set"`
		} `json:"route"`
	}
	recoveryOK(t, json.Unmarshal(current, &doc))
	if len(doc.Services) != 1 || doc.Services[0].ListenPort != port || doc.Services[0].Secret != cfg.API.Secret {
		t.Fatal("generated API identity changed")
	}
	for _, rule := range doc.Route.RuleSet {
		if !filepath.IsAbs(rule.Path) {
			t.Fatal("generation did not retain local asset", rule.Path)
		}
		_, e := os.Stat(rule.Path)
		recoveryOK(t, e)
	}
	after, err := os.ReadFile(filepath.Join(dir, "recovery", "last-good.json"))
	recoveryOK(t, err)
	if !bytes.Equal(after, pointer) {
		t.Fatal("prepared generation promoted itself to known working")
	}
	s.mu.Lock()
	pinned := s.circuit.Pinned
	s.mu.Unlock()
	if pinned {
		t.Fatal("new generation retained old engine pin")
	}
	// The installed update validates correctly but fails when run. A real retained
	// engine must recover it once, even though the candidate has valid JSON.
	broken := filepath.Join(dir, "updated-engine")
	script := "#!/bin/sh\nif [ \"$1\" = check ]; then exec '" + strings.ReplaceAll(native, "'", "'\"'\"'") + "' \"$@\"; fi\nexit 23\n"
	recoveryOK(t, os.WriteFile(broken, []byte(script), 0700))
	t.Setenv("SAKAMOTO_SING_BOX", broken)
	rolled, err := RepairAt(ctx, path, RepairOptions{Regenerate: true, Restart: true})
	recoveryOK(t, err)
	if !rolled.Verified || rolled.Action != "rolled_back" {
		t.Fatal("failed generated runtime did not recover", rolled)
	}
	t.Setenv("SAKAMOTO_SING_BOX", native)
	recoveryOK(t, os.Remove(filepath.Join(dir, "recovery", "last-good.json")))
	before = snapshot()
	failed, err = RepairAt(ctx, path, RepairOptions{Fallback: true, Restart: true})
	if err == nil || failed.Problem != "fallback_unavailable" {
		t.Fatal("missing fallback accepted", failed, err)
	}
	unchanged(before, rolled.PID)
	recoveryOK(t, recovery.Hold(dir, "configuration-repair fixture"))
	paused, err := RepairAt(ctx, path, RepairOptions{Regenerate: true, Restart: true})
	if err == nil || paused.State != "maintenance" {
		t.Fatal("configuration recovery ignored maintenance", paused, err)
	}
	unchanged(before, rolled.PID)
	if reply := s.resume(); !strings.HasPrefix(reply, "resumed") {
		t.Fatal(reply)
	}
	entered := make(chan struct{}, 1)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- struct{}{}; <-r.Context().Done() }))
	defer source.Close()
	cfg.ConfPath = source.URL + "/rules.conf"
	recoveryOK(t, cfg.Save(path))
	result := make(chan RepairResult, 1)
	go func() { r, _ := RepairAt(ctx, path, RepairOptions{Regenerate: true, Restart: true}); result <- r }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("source preparation did not begin")
	}
	stopped, err := SendToContext(ctx, filepath.Join(dir, SockName), "disconnect")
	recoveryOK(t, err)
	if !strings.HasPrefix(stopped, "disconnected") {
		t.Fatal("manual stop failed", stopped)
	}
	select {
	case r := <-result:
		if r.Verified {
			t.Fatal("canceled preparation activated a configuration", r)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("manual stop did not cancel source preparation")
	}
	time.Sleep(100 * time.Millisecond)
	s.mu.Lock()
	finalRunning := s.runningLocked()
	s.mu.Unlock()
	if finalRunning {
		t.Fatal("late configuration repair undid manual stop")
	}
	// A deliberate stop must also win while reconnect waits for a hung old
	// process to release its resources.
	recoveryOK(t, os.WriteFile(filepath.Join(dir, "recovery", "last-good.json"), pointer, 0600))
	cfg.ConfPath = filepath.Join(dir, "source.conf")
	recoveryOK(t, cfg.Save(path))
	if reply := s.start(); !connectedReply(reply) {
		t.Fatal(reply)
	}
	s.mu.Lock()
	hungChild := s.child
	hungPID := hungChild.Process.Pid
	s.mu.Unlock()
	defer func() { _ = hungChild.Process.Kill() }()
	recoveryOK(t, syscall.Kill(hungPID, syscall.SIGSTOP))
	graceResult := make(chan RepairResult, 1)
	go func() { r, _ := RepairAt(ctx, path, RepairOptions{Restart: true}); graceResult <- r }()
	deadline := time.Now().Add(6 * time.Second)
	for {
		status, e := SendToContext(ctx, filepath.Join(dir, SockName), "status")
		recoveryOK(t, e)
		if strings.HasPrefix(status, "disconnected") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reconnect did not begin owned process shutdown")
		}
		time.Sleep(10 * time.Millisecond)
	}
	blockedConnect, e := SendToContext(ctx, filepath.Join(dir, SockName), "connect")
	recoveryOK(t, e)
	if !strings.Contains(blockedConnect, "repair in progress") {
		t.Fatal("concurrent connect bypassed owned shutdown", blockedConnect)
	}
	_, err = SendToContext(ctx, filepath.Join(dir, SockName), "disconnect")
	recoveryOK(t, err)
	select {
	case r := <-graceResult:
		if r.Verified || r.Problem != "canceled" {
			t.Fatal("late reconnect ignored manual stop", r)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("owned process shutdown did not complete")
	}
	s.mu.Lock()
	lateRunning, lateBlocked := s.runningLocked(), s.circuit.Blocked
	s.mu.Unlock()
	if lateRunning || lateBlocked || syscall.Kill(hungPID, 0) != syscall.ESRCH {
		t.Fatal("manual stop left a restarted, blocked or orphaned core")
	}
	writeRecoveryArtifact(t, map[string]any{"service_update_config_checked_before_disconnect": true, "manual_stop_cancels_reconnect_grace": true, "scenario": "configuration-repair", "real_native_check": true, "real_native_api": true, "corrupt_json_and_sidecar_fallback": true, "generation_failure_retains_pid_and_files": true, "empty_nodes_rejected": true, "pending_api_preserved": true, "api_identity_mismatch_detected": true, "custom_api_port_preserved": true, "assets_retained": true, "prepared_not_promoted": true, "generated_engine_failure_rolled_back": true, "missing_fallback_retains_live_core": true, "manual_stop_cancels_preparation": true, "uses_real_tun": false})
}
