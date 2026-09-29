package tui

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/sbclient"
	"github.com/sagernet/sing-box/daemon"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

type fakeModeCore struct {
	daemon.UnimplementedStartedServiceServer
	current string
	modes   []string
	writes  int
}

func (f *fakeModeCore) GetVersion(context.Context, *emptypb.Empty) (*daemon.Version, error) {
	return &daemon.Version{Version: "test"}, nil
}
func (f *fakeModeCore) GetClashModeStatus(context.Context, *emptypb.Empty) (*daemon.ClashModeStatus, error) {
	return &daemon.ClashModeStatus{ModeList: f.modes, CurrentMode: f.current}, nil
}
func (f *fakeModeCore) SetClashMode(_ context.Context, req *daemon.ClashMode) (*emptypb.Empty, error) {
	f.writes++
	for _, mode := range f.modes {
		if strings.EqualFold(mode, req.Mode) {
			f.current = mode
			break
		}
	}
	return &emptypb.Empty{}, nil
}
func TestModeCyclesCapitalizedAndVerifiesCore(t *testing.T) {
	for _, tc := range []struct{ current, want string }{{"Rule", "Global"}, {"global", "Direct"}, {"Direct", "Rule"}, {"unknown", "Rule"}} {
		if got := nextMode(tc.current); got != tc.want {
			t.Fatalf("nextMode(%q)=%q want %q", tc.current, got, tc.want)
		}
	}
	core := &fakeModeCore{current: "Rule", modes: []string{"Rule", "Global", "Direct"}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	daemon.RegisterStartedServiceServer(server, core)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	defer func() { _ = listener.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := sbclient.Dial(ctx, "http://"+listener.Addr().String(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	m := testModel(t)
	m.conn = client
	m.mode = "stale"
	for _, want := range []string{"Global", "Direct", "Rule"} {
		message := m.cycleMode()().(actionMsg)
		if message.err != nil || message.mode != want {
			t.Fatalf("switch %s: %+v", want, message)
		}
		m.onAction(message)
		if m.mode != want {
			t.Fatalf("mode display did not refresh: %q", m.mode)
		}
	}
	writes := core.writes
	core.modes = []string{"Rule"}
	message := m.cycleMode()().(actionMsg)
	if message.err == nil || !strings.Contains(message.err.Error(), "regenerate") || core.writes != writes {
		t.Fatal("old config falsely claimed Global mode", message.err)
	}
	m.Update(modeMsg("Direct"))
	if m.mode != "Direct" {
		t.Fatal("native mode subscription did not update the view")
	}
}
func TestHomeModeControlIsClickable(t *testing.T) {
	m := testModel(t)
	m.mode = "Global"
	if !strings.Contains(m.View(), "[ Mode: Global ]") {
		t.Fatal("Home lacks routing mode control")
	}
	for _, h := range m.hits {
		if h.action == "routing-mode" {
			if h.x1 > m.width {
				t.Fatal("mode control exceeds viewport")
			}
			return
		}
	}
	t.Fatal("mode control has no mouse target")
}
