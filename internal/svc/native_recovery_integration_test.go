package svc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/recovery"
	"github.com/pi-dal/sakamoto/internal/sbclient"
)

// An actual native core runs with loopback inbounds only: no TUN, system DNS,
// launchd, external origins, or user runtime. It verifies that the copied engine
// really starts, the live API receipt is usable, and a bad candidate rolls back.
func TestRecoveryNativeCoreRollbackAndAPIOutage(t *testing.T) {
	binary := singBoxExecutable()
	if _, err := exec.LookPath(binary); err != nil {
		t.Fatal("native core is required for recovery E2E:", err)
	}
	dir, err := os.MkdirTemp("/tmp", "sakamoto-rescue-native-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	port := func() int {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = listener.Close() }()
		return listener.Addr().(*net.TCPAddr).Port
	}
	api, mixed := port(), port()
	raw := []byte(fmt.Sprintf(`{"log":{"disabled":true},"experimental":{"cache_file":{"enabled":true}},"services":[{"type":"api","listen":"127.0.0.1","listen_port":%d,"secret":"offline-recovery-integration-key-0123456789abcdef"}],"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"rules":[{"clash_mode":"Global","action":"route","outbound":"direct"},{"clash_mode":"Rule","action":"route","outbound":"direct"}],"final":"direct"}}`, api, mixed))
	recoveryOK(t, os.WriteFile(filepath.Join(dir, "config.json"), raw, 0600))
	recoveryOK(t, os.WriteFile(filepath.Join(dir, "sakamoto.yaml"), []byte("dns_guard:\n  enabled: false\n"), 0600))
	s := NewServer(filepath.Join(dir, "config.json"), dir)
	s.protectedRuntimeParent = t.TempDir()
	s.protectedStateParent = t.TempDir()
	s.vpnConnected = func() bool { return false }
	s.retryDelay = 20 * time.Millisecond
	s.stablePeriod = 200 * time.Millisecond
	defer s.stop()
	if reply := s.start(); !strings.HasPrefix(reply, "connected") {
		t.Fatal(reply)
	}
	waitRecovery(t, func() bool { _, err := os.Stat(filepath.Join(dir, "recovery", "last-good.json")); return err == nil })
	s.mu.Lock()
	firstPID := s.child.Process.Pid
	s.stablePeriod = time.Hour
	s.mu.Unlock()
	modeClient := func() *sbclient.Client {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var spec struct{ Services []struct{ Secret string } }
		recoveryOK(t, json.Unmarshal(raw, &spec))
		client, err := sbclient.Dial(ctx, fmt.Sprintf("http://127.0.0.1:%d", api), spec.Services[0].Secret)
		recoveryOK(t, err)
		return client
	}
	first := modeClient()
	recoveryOK(t, first.SetClashMode(context.Background(), "Global"))
	first.Close()
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = echo.Close() }()
	go func() {
		conn, err := echo.Accept()
		if err == nil {
			defer func() { _ = conn.Close() }()
			_, _ = io.Copy(conn, conn)
		}
	}()
	stream, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", mixed), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	recoveryOK(t, stream.SetDeadline(time.Now().Add(5*time.Second)))
	if _, err := fmt.Fprintf(stream, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", echo.Addr(), echo.Addr()); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stream)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	if _, err := fmt.Fprint(stream, "before\n"); err != nil {
		t.Fatal(err)
	}
	if line, err := reader.ReadString('\n'); err != nil || line != "before\n" {
		t.Fatal("native stream unavailable", line, err)
	}
	var config map[string]any
	recoveryOK(t, json.Unmarshal(raw, &config))
	config["services"].([]any)[0].(map[string]any)["secret"] = "{{SECRET_mq2i6rt2}}"
	staged, _ := json.Marshal(config)
	recoveryOK(t, os.WriteFile(s.cfgPath, staged, 0600))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_, apiErr := s.coreEpoch(ctx)
	cancel()
	if apiErr == nil {
		t.Fatal("staged API key unexpectedly authorized against old core")
	}
	if _, err := fmt.Fprint(stream, "after\n"); err != nil {
		t.Fatal(err)
	}
	if line, err := reader.ReadString('\n'); err != nil || line != "after\n" {
		t.Fatal("API failure interrupted existing stream", line, err)
	}
	s.mu.Lock()
	samePID := s.child.Process.Pid == firstPID
	s.mu.Unlock()
	if !samePID {
		t.Fatal("API failure restarted the data plane")
	}
	// The native child reloads the verified private generation even when the
	// staging path already carries a different API identity.
	s.mu.Lock()
	appliedArgs := append([]string(nil), s.child.Args...)
	s.mu.Unlock()
	foundSnapshot := false
	for i, arg := range appliedArgs {
		if arg == "-c" && i+1 < len(appliedArgs) {
			path := appliedArgs[i+1]
			if !strings.HasPrefix(path, s.protectedRuntimeParent+string(os.PathSeparator)) {
				t.Fatal("native process did not use captured configuration")
			}
			bytes, readErr := os.ReadFile(path)
			recoveryOK(t, readErr)
			foundSnapshot = !strings.Contains(string(bytes), config["services"].([]any)[0].(map[string]any)["secret"].(string))
		}
	}
	if !foundSnapshot {
		t.Fatal("staged edit affected captured configuration")
	}
	// A broken staged config must not be auto-activated after a real core crash.
	recoveryOK(t, os.WriteFile(s.cfgPath, []byte("bad candidate"), 0600))
	s.mu.Lock()
	killErr := s.child.Process.Kill()
	s.mu.Unlock()
	recoveryOK(t, killErr)
	waitRecovery(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.circuit.Blocked && !s.runningLocked()
	})
	if reply := s.rollback(); !strings.HasPrefix(reply, "restored") {
		t.Fatal(reply)
	}
	if reply := s.start(); !strings.HasPrefix(reply, "connected") {
		t.Fatal(reply)
	}
	s.mu.Lock()
	fallbackPID := s.child.Process.Pid
	pinnedPath := s.child.Path
	s.mu.Unlock()
	if fallbackPID == firstPID || !strings.Contains(pinnedPath, "recovery/bundles/") {
		t.Fatal("known-working engine was not executed", pinnedPath)
	}
	waitRecovery(t, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_, err := s.coreEpoch(ctx)
		return err == nil
	})
	restarted := modeClient()
	status, err := restarted.ClashModeStatus(context.Background())
	restarted.Close()
	recoveryOK(t, err)
	if status.CurrentMode != "Global" {
		t.Fatalf("new generation started in %s", status.CurrentMode)
	}
	s.mu.Lock()
	killErr = s.child.Process.Kill()
	s.mu.Unlock()
	recoveryOK(t, killErr)
	waitRecovery(t, func() bool { return strings.Contains(s.status(), "blocked") })
	state, err := recovery.LoadCircuit(dir)
	if err != nil || !state.Blocked || !state.RollbackUsed {
		t.Fatal("failed rollback did not trip persistent breaker", state, err)
	}
	writeRecoveryArtifact(t, map[string]any{"scenario": "real-native-core", "recovered_mode": status.CurrentMode, "same_stream_during_api_failure": true, "first_pid": firstPID, "fallback_pid": fallbackPID, "pinned_engine_executed": true, "bad_candidate_not_activated": true, "failed_rollback_blocked": true})
}
