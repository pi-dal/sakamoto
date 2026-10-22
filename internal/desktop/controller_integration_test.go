//go:build darwin || linux

package desktop_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/core"
	"github.com/pi-dal/sakamoto/internal/desktop"
	"github.com/pi-dal/sakamoto/internal/svc"
	"github.com/sagernet/sing-box/daemon"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestMenuBarControllerEndToEnd(t *testing.T) {
	// A short /tmp path stays below macOS's Unix socket path limit.
	dir, err := os.MkdirTemp("", "sakamoto-menu-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	f := &fixture{mode: "rule", selected: "RealityAuto", connectGate: make(chan struct{}), connectEntered: make(chan struct{}, 1)}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	const fixtureSecret = "menu-bar-isolated-fixture"
	authenticate := func(ctx context.Context) error {
		md, _ := metadata.FromIncomingContext(ctx)
		if values := md.Get("authorization"); len(values) != 1 || values[0] != "Bearer "+fixtureSecret {
			return status.Error(codes.Unauthenticated, "fixture authorization required")
		}
		return nil
	}
	server := grpc.NewServer(
		grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			if err := authenticate(ctx); err != nil {
				return nil, err
			}
			return handler(ctx, req)
		}),
		grpc.StreamInterceptor(func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			if err := authenticate(stream.Context()); err != nil {
				return err
			}
			return handler(srv, stream)
		}),
	)
	daemon.RegisterStartedServiceServer(server, f)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	cfg := config.Default()
	cfg.API.URL = "http://" + listener.Addr().String()
	cfg.API.Secret = fixtureSecret
	cfgPath := filepath.Join(dir, "sakamoto.yaml")
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer probe.Close()
	c := desktop.New(cfgPath, desktop.Options{PollInterval: 30 * time.Millisecond, ProbeURLs: []string{probe.URL}, ConflictCheck: func(context.Context) bool { return false }})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	defer cancel()
	eventually(t, c, func(s desktop.Snapshot) bool { return s.Phase == core.PhaseUnavailable })
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
	eventually(t, c, func(s desktop.Snapshot) bool { return s.Phase == core.PhaseDisconnected && s.ConfigReady })
	connected := make(chan error, 1)
	go func() { connected <- c.Connect(ctx) }()
	select {
	case <-f.connectEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("connect did not reach isolated socket")
	}
	if err := c.Connect(ctx); err == nil {
		t.Fatal("concurrent connect was accepted")
	}
	close(f.connectGate)
	if err := <-connected; err != nil {
		t.Fatal(err)
	}
	eventually(t, c, func(s desktop.Snapshot) bool {
		return s.APIReady && s.Phase == core.PhaseReachable && len(s.Groups) == 3
	})
	if f.traffic.Load() != 0 {
		t.Fatal("hidden panel started traffic stream")
	}
	c.SetVisible(true)
	eventually(t, c, func(s desktop.Snapshot) bool { return s.Traffic.Available && s.Traffic.Downlink == 4096 })
	if err := c.SetMode(ctx, "global"); err != nil {
		t.Fatal(err)
	}
	eventually(t, c, func(s desktop.Snapshot) bool { return s.Mode == "global" })
	f.mu.Lock()
	f.ignoreMode = true
	f.mu.Unlock()
	if err := c.SetMode(ctx, "direct"); err == nil {
		t.Fatal("unapplied mode was reported as successful")
	}
	f.mu.Lock()
	f.ignoreMode = false
	f.rejectMode = true
	f.mu.Unlock()
	if err := c.SetMode(ctx, "direct"); err == nil {
		t.Fatal("API error was accepted")
	}
	if strings.Contains(c.Snapshot().Notice.Text, "fixture-private-token") {
		t.Fatal("raw API error leaked into view state")
	}
	f.mu.Lock()
	f.rejectMode = false
	f.mu.Unlock()
	if err := c.SetMode(ctx, "unsupported"); err == nil {
		t.Fatal("unsupported mode accepted")
	}
	if err := c.SelectNode(ctx, "RealityAuto", "missing"); err == nil {
		t.Fatal("unknown node accepted")
	}
	if err := c.SelectNode(ctx, "RealityAuto", "Demo Tokyo"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	sequence := strings.Join(f.selections, ",")
	f.mu.Unlock()
	if sequence != "ManualPick=Demo Tokyo,MainProxy=ManualPick" {
		t.Fatalf("manual fallback semantics changed: %s", sequence)
	}
	if err := c.Test(ctx, "Demo Tokyo"); err != nil {
		t.Fatal(err)
	}
	eventually(t, c, func(s desktop.Snapshot) bool { return s.Tests["Demo Tokyo"] == core.NodeReachable })
	c.SetVisible(false)
	deadline := time.Now().Add(3 * time.Second)
	for f.traffic.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if f.traffic.Load() != 0 {
		t.Fatal("hidden panel retained traffic stream")
	}
	if err := c.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, c, func(s desktop.Snapshot) bool {
		return s.Phase == core.PhaseDisconnected && s.Probe == core.ProbeIdle && !s.APIReady && len(s.Groups) == 0
	})
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, c, func(s desktop.Snapshot) bool { return s.APIReady && s.Phase == core.PhaseReachable })
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("controller failed to stop")
	}
	f.mu.Lock()
	stillRunning := f.connected
	commands := append([]string(nil), f.commands...)
	f.mu.Unlock()
	if !stillRunning {
		t.Fatal("closing controller disconnected tunnel")
	}
	count := 0
	for _, command := range commands {
		if command == "connect" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected two explicit connects, got %d", count)
	}
	if artifact := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR"); artifact != "" {
		if err := os.MkdirAll(artifact, 0o755); err != nil {
			t.Fatal(err)
		}
		receipt := map[string]any{"schema": 1, "passed": true, "scenarios": []string{"unavailable recovery", "custom runtime connect", "duplicate connect rejected", "probe verified", "visible traffic only", "mode verified", "unapplied mode rejected", "API errors redacted", "authenticated API", "manual selection sequence", "fresh latency result", "disconnect clears stale state", "close preserves tunnel"}, "selectionSequence": sequence, "connectRequests": count, "usesRealTUN": false}
		raw, err := json.MarshalIndent(receipt, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(artifact, "controller-e2e.json"), append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
