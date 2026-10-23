package desktop_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/desktop"
	"github.com/sagernet/sing-box/daemon"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Failure cases: closed history in reset snapshots, delta-only updates, unknown
// IDs, duplicate events, empty resets, dropped streams, unsupported tracking,
// stale data after navigation/hiding, snapshot aliasing and hidden subscribers.
// Exercise these through the real gRPC client and controller, without a TUN.
type connectionFixture struct {
	*fixture
	updates       chan *daemon.ConnectionEvents
	failures      chan error
	subscriptions atomic.Int32
	calls         atomic.Int32
	unsupported   atomic.Bool
}

func (f *connectionFixture) SubscribeConnections(_ *daemon.SubscribeConnectionsRequest, stream grpc.ServerStreamingServer[daemon.ConnectionEvents]) error {
	f.calls.Add(1)
	if f.unsupported.Load() {
		return status.Error(codes.Unimplemented, "private fixture detail")
	}
	f.subscriptions.Add(1)
	defer f.subscriptions.Add(-1)
	if err := stream.Send(&daemon.ConnectionEvents{Reset_: true}); err != nil {
		return err
	}
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case err := <-f.failures:
			return err
		case value := <-f.updates:
			if err := stream.Send(value); err != nil {
				return err
			}
		}
	}
}
func TestLiveConnectionsEndToEnd(t *testing.T) {
	f := &connectionFixture{fixture: &fixture{mode: "rule", connected: true, selected: "RealityAuto"}, updates: make(chan *daemon.ConnectionEvents, 8), failures: make(chan error, 1)}
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
	c := desktop.New(path, desktop.Options{ExternalCore: true, PollInterval: 25 * time.Millisecond, ProbeURLs: []string{probe.URL}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	c.SetVisible(true)
	eventually(t, c, func(s desktop.Snapshot) bool { return s.APIReady })
	if f.calls.Load() != 0 {
		t.Fatal("home subscribed to connections")
	}
	c.SetConnectionsVisible(true)
	eventually(t, c, func(s desktop.Snapshot) bool { return s.ConnectionsState == desktop.ConnectionsLive })
	f.updates <- &daemon.ConnectionEvents{Reset_: true, Events: []*daemon.ConnectionEvent{
		{Id: "one", Connection: &daemon.Connection{Id: "one", Domain: "example.com", Destination: "192.0.2.1:443", Network: "tcp", CreatedAt: 100, Outbound: "Demo Tokyo", UplinkTotal: 100, DownlinkTotal: 200, ProcessInfo: &daemon.ProcessInfo{ProcessPath: "/private/user/Applications/Safari"}}},
		{Id: "history", Connection: &daemon.Connection{ClosedAt: 123, Domain: "closed.example"}},
		{Id: "ipv6", Connection: &daemon.Connection{Destination: "[2001:db8::1]:443", Network: "udp", CreatedAt: 200}},
	}}
	s := eventually(t, c, func(s desktop.Snapshot) bool { return len(s.Connections) == 2 })
	if s.Connections[0].ID != "ipv6" || s.Connections[1].Process != "Safari" {
		t.Fatal("ordering, IPv6 fallback or process projection failed", s.Connections)
	}
	s.Connections[0].Target = "changed externally"
	if c.Snapshot().Connections[0].Target == "changed externally" {
		t.Fatal("snapshot aliases controller data")
	}
	f.updates <- &daemon.ConnectionEvents{Events: []*daemon.ConnectionEvent{
		{Id: "one", Type: daemon.ConnectionEventType_CONNECTION_EVENT_UPDATE, UplinkDelta: 50, DownlinkDelta: 500},
		{Id: "missing", Type: daemon.ConnectionEventType_CONNECTION_EVENT_UPDATE, UplinkDelta: 999},
		{Id: "ipv6", Type: daemon.ConnectionEventType_CONNECTION_EVENT_CLOSED},
	}}
	eventually(t, c, func(s desktop.Snapshot) bool {
		return len(s.Connections) == 1 && s.Connections[0].UplinkTotal == 150 && s.Connections[0].DownlinkTotal == 700
	})
	f.updates <- &daemon.ConnectionEvents{Events: []*daemon.ConnectionEvent{{Id: "one", Connection: &daemon.Connection{Domain: "example.com", UplinkTotal: 200, DownlinkTotal: 800}}}}
	eventually(t, c, func(s desktop.Snapshot) bool { return len(s.Connections) == 1 && s.Connections[0].DownlinkTotal == 800 })
	f.updates <- &daemon.ConnectionEvents{Reset_: true}
	eventually(t, c, func(s desktop.Snapshot) bool {
		return s.ConnectionsState == desktop.ConnectionsLive && len(s.Connections) == 0
	})
	f.failures <- status.Error(codes.Unavailable, "private fixture detail")
	eventually(t, c, func(s desktop.Snapshot) bool {
		return s.ConnectionsState == desktop.ConnectionsRetrying && len(s.Connections) == 0 && s.APIReady
	})
	eventually(t, c, func(s desktop.Snapshot) bool {
		return s.ConnectionsState == desktop.ConnectionsLive && f.calls.Load() >= 2
	})
	c.SetConnectionsVisible(false)
	eventually(t, c, func(s desktop.Snapshot) bool {
		return len(s.Connections) == 0 && s.ConnectionsState == "" && f.subscriptions.Load() == 0
	})
	// Rapid navigation cannot republish an earlier view's stream into the new view.
	for i := 0; i < 5; i++ {
		c.SetConnectionsVisible(true)
		c.SetConnectionsVisible(false)
	}
	eventually(t, c, func(s desktop.Snapshot) bool { return f.subscriptions.Load() == 0 && len(s.Connections) == 0 })
	f.unsupported.Store(true)
	c.SetConnectionsVisible(true)
	eventually(t, c, func(s desktop.Snapshot) bool {
		return s.ConnectionsState == desktop.ConnectionsUnsupported && s.APIReady && s.Traffic.Available
	})
	calls := f.calls.Load()
	time.Sleep(150 * time.Millisecond)
	if f.calls.Load() != calls {
		t.Fatal("unsupported tracking kept retrying")
	}
	c.SetConnectionsVisible(false)
	f.unsupported.Store(false)
	c.SetConnectionsVisible(true)
	eventually(t, c, func(s desktop.Snapshot) bool { return s.ConnectionsState == desktop.ConnectionsLive })
	f.updates <- &daemon.ConnectionEvents{Events: []*daemon.ConnectionEvent{{Id: "fresh", Connection: &daemon.Connection{Domain: "fresh.example"}}}}
	eventually(t, c, func(s desktop.Snapshot) bool { return len(s.Connections) == 1 })
	c.SetVisible(false)
	eventually(t, c, func(s desktop.Snapshot) bool {
		return len(s.Connections) == 0 && s.ConnectionsState == "" && f.subscriptions.Load() == 0
	})
	idleCalls := f.calls.Load()
	time.Sleep(150 * time.Millisecond)
	if f.calls.Load() != idleCalls {
		t.Fatal("hidden panel resumed tracking")
	}
	c.SetVisible(true)
	eventually(t, c, func(s desktop.Snapshot) bool {
		return s.ConnectionsState == desktop.ConnectionsLive && len(s.Connections) == 0
	})
	c.SetVisible(false)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("connection observer leaked on shutdown")
	}
	if dir := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.MarshalIndent(map[string]any{"schema": 1, "passed": true, "usesRealTUN": false, "scenarios": []string{"on-demand subscription", "active-only snapshot", "IPv6 and process projection", "immutable snapshots", "traffic deltas", "unknown update ignored", "duplicate lifecycle events", "closed removal", "empty reset", "reconnect", "unsupported tracking without retry loop", "rapid navigation", "hidden idle", "fresh snapshot on reopen", "clean shutdown"}}, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "connections-e2e.json"), append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
