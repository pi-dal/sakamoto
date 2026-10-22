package watch

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/experiment"
	"github.com/pi-dal/sakamoto/internal/sbclient"
	"github.com/sagernet/sing-box/daemon"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

type learningStreamCore struct {
	daemon.UnimplementedStartedServiceServer
	interval  chan int64
	stopped   chan struct{}
	port      string
	startedAt int64
}

func (f *learningStreamCore) GetVersion(context.Context, *emptypb.Empty) (*daemon.Version, error) {
	return &daemon.Version{Version: "fixture"}, nil
}
func (f *learningStreamCore) GetStartedAt(context.Context, *emptypb.Empty) (*daemon.StartedAt, error) {
	return &daemon.StartedAt{StartedAt: f.startedAt}, nil
}
func (f *learningStreamCore) SubscribeGroups(_ *emptypb.Empty, s grpc.ServerStreamingServer[daemon.Groups]) error {
	if err := s.Send(&daemon.Groups{Group: []*daemon.Group{{Tag: "MainProxy", Selected: "primary"}, {Tag: "primary", Items: []*daemon.GroupItem{{Tag: "node", UrlTestDelay: 1, UrlTestTime: time.Now().Unix()}}}}}); err != nil {
		return err
	}
	<-s.Context().Done()
	return nil
}
func (f *learningStreamCore) SubscribeConnections(r *daemon.SubscribeConnectionsRequest, s grpc.ServerStreamingServer[daemon.ConnectionEvents]) error {
	f.interval <- r.Interval
	event := func(domain string) *daemon.ConnectionEvent {
		return &daemon.ConnectionEvent{Type: daemon.ConnectionEventType_CONNECTION_EVENT_NEW, Id: domain, Connection: &daemon.Connection{Network: "tcp", Outbound: "direct", Domain: domain, Destination: domain + ":" + f.port}}
	}
	if err := s.Send(&daemon.ConnectionEvents{Reset_: true, Events: []*daemon.ConnectionEvent{event("historical.example")}}); err != nil {
		return err
	}
	select {
	case <-s.Context().Done():
		return nil
	case <-time.After(150 * time.Millisecond):
	}
	if err := s.Send(&daemon.ConnectionEvents{Events: []*daemon.ConnectionEvent{event("live.example")}}); err != nil {
		return err
	}
	<-s.Context().Done()
	close(f.stopped)
	return nil
}
func (f *learningStreamCore) SubscribeLog(_ *emptypb.Empty, s grpc.ServerStreamingServer[daemon.Log]) error {
	message := func(domain string) *daemon.Log_Message {
		return &daemon.Log_Message{Level: daemon.LogLevel_ERROR, Message: "connection: open connection to " + domain + ":" + f.port + " using outbound/direct[direct]: dial tcp 203.0.113.1:" + f.port + ": connect: connection refused"}
	}
	if err := s.Send(&daemon.Log{Reset_: true, Messages: []*daemon.Log_Message{message("historical.example")}}); err != nil {
		return err
	}
	select {
	case <-s.Context().Done():
		return nil
	case <-time.After(50 * time.Millisecond):
	}
	if err := s.Send(&daemon.Log{Messages: []*daemon.Log_Message{message("live.example")}}); err != nil {
		return err
	}
	<-s.Context().Done()
	return nil
}

func TestLearningStreamsIgnoreReplayAndStopWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Experiment.Mode = "auto"
	cfg.FallbackEnabled = false
	cfg.CheckInterval = 50 * time.Millisecond
	cfgPath := filepath.Join(dir, "sakamoto.yaml")
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	fixture := &learningStreamCore{interval: make(chan int64, 1), stopped: make(chan struct{}), port: "22"}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	daemon.RegisterStartedServiceServer(server, fixture)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w := New(cfg, func(ctx context.Context) (*sbclient.Client, error) {
		return sbclient.Dial(ctx, "http://"+listener.Addr().String(), "")
	})
	w.SetConfigPath(cfgPath)
	done := make(chan error, 1)
	go func() { done <- w.runOnce(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case interval := <-fixture.interval:
		if interval != int64(5*time.Second) {
			t.Fatalf("native interval=%d", interval)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var history []experiment.LearningResult
	for ctx.Err() == nil {
		history, err = experiment.ReadReport(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(history) > 0 {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(20 * time.Millisecond):
		}
	}
	if len(history) != 1 || history[0].Domain != "live.example" || history[0].State != "skipped" {
		t.Fatalf("unexpected replay/evidence outcomes: %+v", history)
	}
	disabled, loadErr := config.Load(cfgPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	disabled.Experiment.Mode = "off"
	if err = disabled.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fixture.stopped:
	case <-ctx.Done():
		t.Fatal("learning streams remained subscribed after disabling")
	}
	if artifact := os.Getenv("SAKAMOTO_TEST_ARTIFACT_DIR"); artifact != "" {
		if err = os.MkdirAll(artifact, 0700); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.MarshalIndent(map[string]any{"interval_ns": int64(5 * time.Second), "historical_evidence_ignored": true, "live_failure_explained": history, "disabled_streams_stopped": true}, "", "  ")
		if err = os.WriteFile(filepath.Join(artifact, "desktop-learning-streams.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
