package svc

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/daemon"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

type fakeControlCore struct {
	daemon.UnimplementedStartedServiceServer
	mu                      sync.Mutex
	tested, group, selected string
}

func (f *fakeControlCore) GetVersion(context.Context, *emptypb.Empty) (*daemon.Version, error) {
	return &daemon.Version{Version: "test"}, nil
}
func (f *fakeControlCore) URLTest(_ context.Context, request *daemon.URLTestRequest) (*emptypb.Empty, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tested = request.OutboundTag
	return &emptypb.Empty{}, nil
}
func (f *fakeControlCore) SelectOutbound(_ context.Context, request *daemon.SelectOutboundRequest) (*emptypb.Empty, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.group, f.selected = request.GroupTag, request.OutboundTag
	return &emptypb.Empty{}, nil
}
func (f *fakeControlCore) SubscribeGroups(_ *emptypb.Empty, stream grpc.ServerStreamingServer[daemon.Groups]) error {
	return stream.Send(&daemon.Groups{Group: []*daemon.Group{{Tag: "ManualPick", Selected: "JP Lite"}}})
}

func TestSocketControlsForwardExactTagsAndKeepSecretsPrivate(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	fake := &fakeControlCore{}
	server := grpc.NewServer()
	daemon.RegisterStartedServiceServer(server, fake)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	root := t.TempDir()
	secret := strings.Repeat("0123456789abcdef", 4)
	port := listener.Addr().(*net.TCPAddr).Port
	config := map[string]any{"services": []map[string]any{{"type": "api", "listen": "127.0.0.1", "listen_port": port, "secret": secret}}}
	raw, _ := json.Marshal(config)
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	supervisor := NewServer(path, root)
	command := func(command string) string {
		t.Helper()
		client, remote := net.Pipe()
		defer func() { _ = client.Close() }()
		go supervisor.handle(remote)
		if _, err := io.WriteString(client, command+"\n"); err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(client)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), secret) {
			t.Fatal("API secret leaked")
		}
		return string(raw)
	}
	if reply := command(`urltest {"tag":"JP Lite"}`); !strings.Contains(reply, "queued") {
		t.Fatal(reply)
	}
	if reply := command(`select {"group":"ManualPick","outbound":"JP Lite"}`); !strings.Contains(reply, "Selected") {
		t.Fatal(reply)
	}
	fake.mu.Lock()
	if fake.tested != "JP Lite" || fake.group != "ManualPick" || fake.selected != "JP Lite" {
		t.Fatal("whitespace in tag lost")
	}
	fake.mu.Unlock()
	if reply := command("state"); !strings.Contains(reply, "JP Lite") {
		t.Fatal(reply)
	}
	if reply := command(`select {"group":"ManualPick","outbound":"JP Lite"} {}`); !strings.HasPrefix(reply, "error:") {
		t.Fatal("trailing JSON accepted")
	}
	if reply := command("reload"); !strings.Contains(reply, "not running") {
		t.Fatal("reload changed a disconnected supervisor")
	}
}
