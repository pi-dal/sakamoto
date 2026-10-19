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

// fakeFlapCore serves a sing-box API whose URL-test results can be
// toggled, so the test can drive the watcher through a failover then a
// "recovered" round inside one cooldown window.
type fakeFlapCore struct {
	daemon.UnimplementedStartedServiceServer
	mu        sync.Mutex
	selected  string
	testCalls int
	// healthy decides which tag reports a fresh URL test after each round.
	healthy  string
	updates  chan *daemon.Groups
	switched chan string
}

func (f *fakeFlapCore) snapshot() *daemon.Groups {
	f.mu.Lock()
	selected, healthy := f.selected, f.healthy
	f.mu.Unlock()
	mk := func(tag string) *daemon.Group {
		it := &daemon.GroupItem{Tag: tag + "-node"}
		if tag == healthy {
			it.UrlTestDelay = 95
			it.UrlTestTime = time.Now().Unix()
		}
		return &daemon.Group{Tag: tag, Items: []*daemon.GroupItem{it}}
	}
	return &daemon.Groups{Group: []*daemon.Group{
		{Tag: "MainProxy", Selected: selected},
		mk("RealityAuto"),
		mk("OthersAuto"),
	}}
}

func (f *fakeFlapCore) setHealthy(tag string) {
	f.mu.Lock()
	f.healthy = tag
	f.mu.Unlock()
}

func (f *fakeFlapCore) GetVersion(context.Context, *emptypb.Empty) (*daemon.Version, error) {
	return &daemon.Version{Version: "test"}, nil
}

func (f *fakeFlapCore) SubscribeGroups(_ *emptypb.Empty, stream grpc.ServerStreamingServer[daemon.Groups]) error {
	if err := stream.Send(f.snapshot()); err != nil {
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

func (f *fakeFlapCore) URLTest(_ context.Context, req *daemon.URLTestRequest) (*emptypb.Empty, error) {
	f.mu.Lock()
	f.testCalls++
	f.mu.Unlock()
	select {
	case f.updates <- f.snapshot():
	default:
	}
	return &emptypb.Empty{}, nil
}

func (f *fakeFlapCore) SelectOutbound(_ context.Context, req *daemon.SelectOutboundRequest) (*emptypb.Empty, error) {
	f.mu.Lock()
	f.selected = req.OutboundTag
	f.mu.Unlock()
	select {
	case f.switched <- req.OutboundTag:
	default:
	}
	return &emptypb.Empty{}, nil
}

// TestCooldownSuppressesFlap verifies that once the watcher issues a
// SelectOutbound on a selector, a subsequent healthy result for the preferred
// chain head cannot trigger a switch-back inside SwitchMinInterval.
func TestCooldownSuppressesFlap(t *testing.T) {
	dir, err := os.MkdirTemp("", "sr-cooldown-integration-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	cfgPath := filepath.Join(dir, "sakamoto.yaml")
	cfg := config.Default()
	cfg.CheckInterval = 50 * time.Millisecond
	// TestSettle is clamped up to URLTestGroupWait (= TCPTimeout 15s + 5s),
	// so SwitchMinInterval must exceed that to observe a suppressed evaluate.
	cfg.TestSettle = time.Millisecond
	cfg.RecoverAfter = 1 // switch back after a single healthy round → fastest flap possible
	cfg.SwitchMinInterval = 60 * time.Second
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	fake := &fakeFlapCore{
		selected: "RealityAuto",
		healthy:  "OthersAuto", // RealityAuto starts unhealthy
		updates:  make(chan *daemon.Groups, 16),
		switched: make(chan string, 8),
	}
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
	go func() {
		for e := range w.Events() {
			t.Logf("event [%s] %s", e.Level, e.Message)
		}
	}()

	// First failover: OthersAuto is the only healthy member → switch away.
	// First round needs one CheckInterval plus URLTestGroupWait ≈ 20s.
	select {
	case got := <-fake.switched:
		if got != "OthersAuto" {
			t.Fatalf("first switch chose %q", got)
		}
	case <-time.After(30 * time.Second):
		fake.mu.Lock()
		n := fake.testCalls
		fake.mu.Unlock()
		t.Fatalf("watcher did not fail over to OthersAuto (urltest calls=%d)", n)
	}
	switchTime := time.Now()

	// Now RealityAuto reports healthy. With RecoverAfter=1 the watcher would
	// immediately try to switch back if SwitchMinInterval were not enforced.
	fake.setHealthy("RealityAuto")

	// Allow roughly one more settle cycle (~20s) to prove suppression; we are
	// still well inside the 60s cooldown window.
	suppressedDeadline := switchTime.Add(25 * time.Second)
	select {
	case got := <-fake.switched:
		t.Fatalf("switch-back to %q happened inside cooldown after %s", got, time.Since(switchTime))
	case <-time.After(time.Until(suppressedDeadline)):
		// suppressed as expected
	}

	// Past the cooldown, the next healthy round must be allowed through.
	select {
	case got := <-fake.switched:
		if got != "RealityAuto" {
			t.Fatalf("post-cooldown switch chose %q", got)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("watcher did not restore RealityAuto after cooldown")
	}
}
