//go:build darwin || linux

package desktop_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/desktop"
	"github.com/pi-dal/sakamoto/internal/svc"
	"github.com/sagernet/sing-box/daemon"
	"google.golang.org/grpc"
)

func TestResidentRepairEndToEnd(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "sakamoto-resident-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	f := &fixture{mode: "rule", connected: true, selected: "RealityAuto"}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	daemon.RegisterStartedServiceServer(server, f)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	cfg := config.Default()
	cfg.API.URL = "http://" + listener.Addr().String()
	cfg.API.Secret = ""
	path := filepath.Join(dir, "sakamoto.yaml")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	unix, err := net.Listen("unix", filepath.Join(dir, svc.SockName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close() }()
	go func() {
		for {
			conn, err := unix.Accept()
			if err != nil {
				return
			}
			go f.serveSupervisor(conn)
		}
	}()
	var probes, conflicts atomic.Int32
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { probes.Add(1); w.WriteHeader(http.StatusNoContent) }))
	defer probe.Close()
	c := desktop.New(path, desktop.Options{PollInterval: 25 * time.Millisecond, ProbeURLs: []string{probe.URL}, ConflictCheck: func(context.Context) bool { conflicts.Add(1); return false }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	time.Sleep(140 * time.Millisecond)
	f.mu.Lock()
	initialCommands := len(f.commands)
	f.mu.Unlock()
	if initialCommands != 0 || probes.Load() != 0 || conflicts.Load() != 0 || f.groupSubs.Load() != 0 || f.modes.Load() != 0 {
		t.Fatal("never-opened app did background work")
	}
	c.SetVisible(true)
	eventually(t, c, func(s desktop.Snapshot) bool { return s.APIReady && s.Traffic.Available })
	c.SetVisible(false)
	deadline := time.Now().Add(3 * time.Second)
	for (f.groupSubs.Load() != 0 || f.modes.Load() != 0 || f.traffic.Load() != 0) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.groupSubs.Load() != 0 || f.modes.Load() != 0 || f.traffic.Load() != 0 || c.Snapshot().APIReady || len(c.Snapshot().Groups) != 0 || len(c.Snapshot().Modes) != 0 {
		t.Fatal("hidden app retained active observation")
	}
	f.mu.Lock()
	idleCommands := len(f.commands)
	f.mu.Unlock()
	idleProbes, idleConflicts := probes.Load(), conflicts.Load()
	time.Sleep(180 * time.Millisecond)
	f.mu.Lock()
	afterCommands := len(f.commands)
	f.mu.Unlock()
	if afterCommands != idleCommands || probes.Load() != idleProbes || conflicts.Load() != idleConflicts {
		t.Fatal("hidden app continued polling/probing")
	}
	c.SetVisible(true)
	eventually(t, c, func(s desktop.Snapshot) bool { return s.APIReady && s.Traffic.Available })
	var mu sync.Mutex
	state := "healthy"
	var calls []svc.RepairOptions
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	block := false
	f.mu.Lock()
	f.repair = func(raw string) string {
		var options svc.RepairOptions
		if err := json.Unmarshal([]byte(raw), &options); err != nil {
			return "invalid"
		}
		mu.Lock()
		calls = append(calls, options)
		current, blocked := state, block
		mu.Unlock()
		if blocked {
			entered <- struct{}{}
			<-gate
		}
		result := svc.RepairResult{State: current, Action: "none", Verified: current == "healthy", PID: 123, Detail: "fixture-private-token https://private.example"}
		if !options.CheckOnly && current == "stopped" {
			result.State = "repaired"
			result.Action = "rolled_back"
			result.Verified = true
			f.mu.Lock()
			f.connected = true
			f.mu.Unlock()
		}
		if options.Restart {
			result.State = "repaired"
			result.Action = "restarted"
			result.Verified = true
		}
		bytes, _ := json.Marshal(result)
		return string(bytes)
	}
	f.mu.Unlock()
	if err := c.CheckHealth(ctx); err != nil || !c.Snapshot().Health.Verified {
		t.Fatal("read-only diagnosis failed", err)
	}
	if err := c.Repair(ctx, false); err != nil || c.Snapshot().Health.State != "healthy" {
		t.Fatal("healthy repair failed", err)
	}
	mu.Lock()
	state = "stopped"
	mu.Unlock()
	f.mu.Lock()
	f.connected = false
	f.mu.Unlock()
	if err := c.Repair(ctx, false); err != nil || c.Snapshot().Health.Action != "rolled_back" {
		t.Fatal("stopped core recovery failed", err)
	}
	mu.Lock()
	state = "degraded"
	mu.Unlock()
	if c.CheckHealth(ctx) == nil || c.Snapshot().Health.Verified {
		t.Fatal("degraded API was reported healthy")
	}
	if strings.Contains(c.Snapshot().Health.Summary, "fixture-private") || strings.Contains(c.Snapshot().Notice.Text, "private.example") {
		t.Fatal("private repair detail leaked")
	}
	if err := c.Repair(ctx, true); err != nil || c.Snapshot().Health.Action != "restarted" {
		t.Fatal("explicit reconnect did not reach supervisor", err)
	}
	mu.Lock()
	state = "maintenance"
	mu.Unlock()
	if c.Repair(ctx, false) == nil || c.Snapshot().Health.State != "maintenance" {
		t.Fatal("maintenance was ignored")
	}
	watcher, err := net.Listen("unix", filepath.Join(dir, "watch.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Close() }()
	var watcherCalls atomic.Int32
	var watcherReply atomic.Value
	watcherReply.Store("queued")
	go func() {
		for {
			conn, err := watcher.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				line, _ := bufio.NewReader(conn).ReadString('\n')
				if strings.TrimSpace(line) == "recover" {
					watcherCalls.Add(1)
					_, _ = conn.Write([]byte(watcherReply.Load().(string) + "\n"))
				}
			}()
		}
	}()
	if err := c.RecoverRoute(ctx); err != nil || c.Snapshot().Recovery.State != "queued" || strings.Contains(c.Snapshot().Notice.Text, "completed") {
		t.Fatal("queued recovery was misrepresented", err)
	}
	acknowledgements := []string{"busy", "cooldown", "manual", "disabled", "unavailable", "maintenance"}
	for _, reply := range acknowledgements {
		watcherReply.Store(reply)
		if err := c.RecoverRoute(ctx); err != nil || c.Snapshot().Recovery.State != reply || strings.Contains(c.Snapshot().Notice.Text, "completed") {
			t.Fatal("watcher acknowledgement changed", reply, err)
		}
	}
	watcherReply.Store("unknown fixture-private-token")
	if c.RecoverRoute(ctx) == nil || c.Snapshot().Recovery.State != "unavailable" || strings.Contains(c.Snapshot().Notice.Text, "fixture-private") {
		t.Fatal("unknown watcher reply was accepted or leaked")
	}
	mu.Lock()
	state = "healthy"
	block = true
	mu.Unlock()
	result := make(chan error, 1)
	go func() { result <- c.Repair(ctx, false) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("repair did not start")
	}
	if c.Repair(ctx, true) == nil {
		t.Fatal("duplicate repair accepted")
	}
	c.SetVisible(false)
	close(gate)
	if err := <-result; err != nil {
		t.Fatal("hiding canceled explicit repair", err)
	}
	_ = unix.Close()
	if c.CheckHealth(ctx) == nil || c.Snapshot().Health.State != "unavailable" {
		t.Fatal("missing supervisor did not expose service recovery")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("resident controller did not stop")
	}
	f.mu.Lock()
	running := f.connected
	f.mu.Unlock()
	if !running || watcherCalls.Load() != 8 {
		t.Fatal("repair lifecycle/recovery ownership changed")
	}
	mu.Lock()
	options := append([]svc.RepairOptions(nil), calls...)
	mu.Unlock()
	if !options[0].CheckOnly || options[1].Restart || !options[4].Restart {
		t.Fatal("repair mode contract changed", options)
	}
	if artifact := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR"); artifact != "" {
		if err := os.MkdirAll(artifact, 0755); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.MarshalIndent(map[string]any{"schema": 1, "passed": true, "initialBackgroundRequests": initialCommands, "hiddenPollingRequests": afterCommands - idleCommands, "hiddenSubscriptions": 0, "healthyProcessRetained": true, "stoppedCoreRecovered": true, "rollbackReported": true, "privateErrorsRedacted": true, "explicitRestart": true, "maintenanceRespected": true, "recoveryAcknowledgement": "queued", "repairSurvivesHide": true, "quitPreservesTunnel": running, "usesRealTUN": false}, "", "  ")
		if err := os.WriteFile(filepath.Join(artifact, "resident-repair-e2e.json"), append(raw, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
