package watch

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/sbclient"
	"github.com/sagernet/sing-box/daemon"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

type fakeRecoveryCore struct {
	daemon.UnimplementedStartedServiceServer
	mu       sync.Mutex
	selected string
	updates  chan *daemon.Groups
	tested   chan string
	switched chan string
}

func (f *fakeRecoveryCore) snapshot(healthy bool) *daemon.Groups {
	f.mu.Lock()
	selected := f.selected
	f.mu.Unlock()
	fallback := &daemon.GroupItem{Tag: "o1"}
	if healthy {
		fallback.UrlTestDelay = 95
		fallback.UrlTestTime = time.Now().Unix()
	}
	return &daemon.Groups{Group: []*daemon.Group{
		{Tag: "MainProxy", Selected: selected},
		{Tag: "RealityAuto", Items: []*daemon.GroupItem{{Tag: "r1"}}},
		{Tag: "OthersAuto", Items: []*daemon.GroupItem{fallback}},
	}}
}
func (f *fakeRecoveryCore) GetVersion(context.Context, *emptypb.Empty) (*daemon.Version, error) {
	return &daemon.Version{Version: "test"}, nil
}
func (f *fakeRecoveryCore) SubscribeGroups(_ *emptypb.Empty, stream grpc.ServerStreamingServer[daemon.Groups]) error {
	if err := stream.Send(f.snapshot(false)); err != nil {
		return err
	}
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case snap := <-f.updates:
			if err := stream.Send(snap); err != nil {
				return err
			}
		}
	}
}
func (f *fakeRecoveryCore) URLTest(_ context.Context, req *daemon.URLTestRequest) (*emptypb.Empty, error) {
	select {
	case f.tested <- req.OutboundTag:
	default:
	}
	if req.OutboundTag == "OthersAuto" {
		select {
		case f.updates <- f.snapshot(true):
		default:
		}
	}
	return &emptypb.Empty{}, nil
}
func (f *fakeRecoveryCore) SelectOutbound(_ context.Context, req *daemon.SelectOutboundRequest) (*emptypb.Empty, error) {
	f.mu.Lock()
	f.selected = req.OutboundTag
	f.mu.Unlock()
	select {
	case f.switched <- req.OutboundTag:
	default:
	}
	return &emptypb.Empty{}, nil
}
func TestTriggerRunsFreshTestsAndWatcherOwnsSelection(t *testing.T) {
	dir, err := os.MkdirTemp("", "sr-recovery-integration-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	cfgPath := filepath.Join(dir, "sakamoto.yaml")
	cfg := config.Default()
	cfg.CheckInterval = time.Hour
	cfg.TestSettle = time.Second
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	fake := &fakeRecoveryCore{selected: "RealityAuto", updates: make(chan *daemon.Groups, 4), tested: make(chan string, 4), switched: make(chan string, 2)}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	daemon.RegisterStartedServiceServer(server, fake)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	defer func() { _ = listener.Close() }()
	url := "http://" + listener.Addr().String()
	w := New(cfg, func(ctx context.Context) (*sbclient.Client, error) { return sbclient.Dial(ctx, url, "") })
	w.SetConfigPath(cfgPath)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	defer func() { cancel(); <-done }()
	var response string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(recoveryPath(cfgPath)); err == nil {
			response, err = RequestRecovery(cfgPath)
			if err == nil && response == "queued" {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if response != "queued" {
		t.Fatalf("watcher did not queue recovery: %q", response)
	}
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case tag := <-fake.tested:
			seen[tag] = true
		case <-time.After(4 * time.Second):
			t.Fatal("recovery did not test both groups")
		}
	}
	if !seen["RealityAuto"] || !seen["OthersAuto"] {
		t.Fatalf("tested wrong groups: %v", seen)
	}
	select {
	case chosen := <-fake.switched:
		if chosen != "OthersAuto" {
			t.Fatalf("unexpected selection %q", chosen)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("watcher did not switch to freshly healthy alternate")
	}
}
