package desktop_test

import (
	"bufio"
	"context"
	"github.com/pi-dal/sakamoto/internal/desktop"
	"github.com/sagernet/sing-box/daemon"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fixture struct {
	daemon.UnimplementedStartedServiceServer
	mu             sync.Mutex
	connected      bool
	mode           string
	ignoreMode     bool
	rejectMode     bool
	selected       string
	manual         string
	tested         bool
	selections     []string
	commands       []string
	connectGate    chan struct{}
	connectEntered chan struct{}
	traffic        atomic.Int32
}

func (f *fixture) GetVersion(context.Context, *emptypb.Empty) (*daemon.Version, error) {
	return &daemon.Version{Version: "menu-bar-fixture"}, nil
}
func (f *fixture) GetClashModeStatus(context.Context, *emptypb.Empty) (*daemon.ClashModeStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &daemon.ClashModeStatus{CurrentMode: f.mode, ModeList: []string{"rule", "global", "direct"}}, nil
}
func (f *fixture) SetClashMode(_ context.Context, value *daemon.ClashMode) (*emptypb.Empty, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rejectMode {
		return nil, status.Error(codes.Unavailable, "fixture-private-token")
	}
	if !f.ignoreMode {
		f.mode = value.Mode
	}
	return &emptypb.Empty{}, nil
}
func (f *fixture) SelectOutbound(_ context.Context, value *daemon.SelectOutboundRequest) (*emptypb.Empty, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.selections = append(f.selections, value.GroupTag+"="+value.OutboundTag)
	if value.GroupTag == "MainProxy" {
		f.selected = value.OutboundTag
	}
	if value.GroupTag == "ManualPick" {
		f.manual = value.OutboundTag
	}
	return &emptypb.Empty{}, nil
}
func (f *fixture) URLTest(context.Context, *daemon.URLTestRequest) (*emptypb.Empty, error) {
	f.mu.Lock()
	f.tested = true
	f.mu.Unlock()
	return &emptypb.Empty{}, nil
}
func (f *fixture) groups() *daemon.Groups {
	f.mu.Lock()
	defer f.mu.Unlock()
	item := &daemon.GroupItem{Tag: "Demo Tokyo", Type: "test"}
	if f.tested {
		item.UrlTestDelay = 42
		item.UrlTestTime = time.Now().Unix()
	}
	return &daemon.Groups{Group: []*daemon.Group{
		{Tag: "MainProxy", Selectable: true, Selected: f.selected, Items: []*daemon.GroupItem{{Tag: "RealityAuto"}, {Tag: "ManualPick"}}},
		{Tag: "RealityAuto", Selected: "Demo Tokyo", Items: []*daemon.GroupItem{item}},
		{Tag: "ManualPick", Selectable: true, Selected: f.manual, Items: []*daemon.GroupItem{item}},
	}}
}
func (f *fixture) SubscribeGroups(_ *emptypb.Empty, stream grpc.ServerStreamingServer[daemon.Groups]) error {
	tick := time.NewTicker(30 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := stream.Send(f.groups()); err != nil {
			return err
		}
		select {
		case <-stream.Context().Done():
			return nil
		case <-tick.C:
		}
	}
}
func (f *fixture) SubscribeClashMode(_ *emptypb.Empty, stream grpc.ServerStreamingServer[daemon.ClashMode]) error {
	tick := time.NewTicker(30 * time.Millisecond)
	defer tick.Stop()
	for {
		f.mu.Lock()
		mode := f.mode
		f.mu.Unlock()
		if err := stream.Send(&daemon.ClashMode{Mode: mode}); err != nil {
			return err
		}
		select {
		case <-stream.Context().Done():
			return nil
		case <-tick.C:
		}
	}
}
func (f *fixture) SubscribeStatus(_ *daemon.SubscribeStatusRequest, stream grpc.ServerStreamingServer[daemon.Status]) error {
	f.traffic.Add(1)
	defer f.traffic.Add(-1)
	if err := stream.Send(&daemon.Status{TrafficAvailable: true, Uplink: 2048, Downlink: 4096}); err != nil {
		return err
	}
	<-stream.Context().Done()
	return nil
}
func (f *fixture) serveSupervisor(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return
	}
	command := strings.TrimSpace(line)
	f.mu.Lock()
	f.commands = append(f.commands, command)
	f.mu.Unlock()
	if command == "connect" && f.connectGate != nil {
		select {
		case f.connectEntered <- struct{}{}:
		default:
		}
		<-f.connectGate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := "error: unsupported"
	switch command {
	case "status":
		reply = "disconnected"
		if f.connected {
			reply = "connected pid=123 dns=off"
		}
	case "connect":
		f.connected = true
		reply = "connected pid=123 dns=off"
	case "disconnect":
		f.connected = false
		reply = "disconnected"
	case "capabilities":
		reply = "native-dns-v1"
	}
	_, _ = conn.Write([]byte(reply + "\n"))
}

func eventually(t *testing.T, c *desktop.Controller, predicate func(desktop.Snapshot) bool) desktop.Snapshot {
	t.Helper()
	deadline := time.NewTimer(6 * time.Second)
	defer deadline.Stop()
	for {
		s := c.Snapshot()
		if predicate(s) {
			return s
		}
		select {
		case <-c.Changes():
		case <-time.After(20 * time.Millisecond):
		case <-deadline.C:
			t.Fatalf("state did not converge: phase=%s api=%v busy=%q notice=%q", s.Phase, s.APIReady, s.Busy, s.Notice.Text)
		}
	}
}
