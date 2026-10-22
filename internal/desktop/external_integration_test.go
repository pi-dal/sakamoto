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
	"github.com/sagernet/sing-box/daemon"
	"google.golang.org/grpc"
)

func TestExternalCoreEndToEnd(t *testing.T) {
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
	path := filepath.Join(t.TempDir(), "sakamoto.yaml")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer probe.Close()
	controller := desktop.New(path, desktop.Options{ExternalCore: true, PollInterval: 30 * time.Millisecond, ProbeURLs: []string{probe.URL}, ConflictCheck: func(context.Context) bool { t.Error("external core checked macOS conflicts"); return false }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { controller.Run(ctx); close(done) }()
	eventually(t, controller, func(s desktop.Snapshot) bool { return s.ExternalCore && s.APIReady && s.Phase == core.PhaseReachable })
	if controller.Connect(ctx) == nil || controller.Disconnect(ctx) == nil {
		t.Fatal("external core accepted a lifecycle operation")
	}
	if err := controller.SetMode(ctx, "global"); err != nil {
		t.Fatal(err)
	}
	if err := controller.SelectNode(ctx, "RealityAuto", "Demo Tokyo"); err != nil {
		t.Fatal(err)
	}
	controller.SetVisible(true)
	eventually(t, controller, func(s desktop.Snapshot) bool { return s.Mode == "global" && s.Traffic.Available })
	controller.SetVisible(false)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("external core observer did not stop")
	}
	f.mu.Lock()
	commands, selections, running := append([]string(nil), f.commands...), strings.Join(f.selections, ","), f.connected
	f.mu.Unlock()
	if len(commands) != 0 || !running || selections != "ManualPick=Demo Tokyo,MainProxy=ManualPick" {
		t.Fatal("external core violated lifecycle or selection boundary")
	}
	if dir := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		raw, err := json.MarshalIndent(map[string]any{"schema": 1, "passed": true, "externalCore": true, "supervisorRequests": len(commands), "closePreservesTunnel": running, "selectionSequence": selections, "usesRealTUN": false}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "external-core-e2e.json"), append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
