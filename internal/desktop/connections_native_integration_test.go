package desktop_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/desktop"
)

// The actual pinned core proxies a loopback echo stream. Verify that observation
// sees real data, stopping observation preserves that same stream, and closed
// history does not reappear when the page is reopened. No TUN or user config.
func TestLiveConnectionsNativeCore(t *testing.T) {
	binary := os.Getenv("SAKAMOTO_SING_BOX")
	if binary == "" {
		binary = "sing-box"
	}
	native, err := exec.LookPath(binary)
	if err != nil {
		t.Skip("native core not on PATH; run with mise exec sing-box@1.14.2")
	}
	port := func() int {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()
		return l.Addr().(*net.TCPAddr).Port
	}
	dir := t.TempDir()
	api, mixed := port(), port()
	raw := fmt.Sprintf(`{"log":{"disabled":true},"services":[{"type":"api","listen":"127.0.0.1","listen_port":%d,"secret":"connections-e2e-only"}],"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`, api, mixed)
	corePath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(corePath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(native, "run", "-D", dir, "-c", corePath)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	cfg := config.Default()
	cfg.API.URL = fmt.Sprintf("http://127.0.0.1:%d", api)
	cfg.API.Secret = "connections-e2e-only"
	path := filepath.Join(dir, "sakamoto.yaml")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer probe.Close()
	c := desktop.New(path, desktop.Options{ExternalCore: true, PollInterval: 50 * time.Millisecond, ProbeURLs: []string{probe.URL}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("native connection observation did not stop")
		}
	}()
	c.SetVisible(true)
	c.SetConnectionsVisible(true)
	eventually(t, c, func(s desktop.Snapshot) bool { return s.ConnectionsState == desktop.ConnectionsLive })
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = echo.Close() }()
	go func() {
		conn, err := echo.Accept()
		if err == nil {
			defer func() { _ = conn.Close() }()
			_, _ = io.Copy(conn, conn)
		}
	}()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", mixed), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", echo.Addr(), echo.Addr()); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line, "200") {
		t.Fatal("loopback proxy CONNECT failed", line, err)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	exchange := func(text string) {
		t.Helper()
		if _, err := fmt.Fprintln(conn, text); err != nil {
			t.Fatal(err)
		}
		line, err := reader.ReadString('\n')
		if err != nil || line != text+"\n" {
			t.Fatal("same data stream interrupted", line, err)
		}
	}
	exchange("before-observation")
	target := echo.Addr().String()
	find := func(s desktop.Snapshot) (desktop.Connection, bool) {
		for _, v := range s.Connections {
			if v.Destination == target {
				return v, true
			}
		}
		return desktop.Connection{}, false
	}
	eventually(t, c, func(s desktop.Snapshot) bool {
		v, ok := find(s)
		return ok && v.Outbound == "direct" && v.UplinkTotal > 0 && v.DownlinkTotal > 0
	})
	c.SetConnectionsVisible(false)
	exchange("after-leaving-page")
	if len(c.Snapshot().Connections) != 0 {
		t.Fatal("closed page retained native destinations")
	}
	c.SetConnectionsVisible(true)
	eventually(t, c, func(s desktop.Snapshot) bool { _, ok := find(s); return ok })
	c.SetVisible(false)
	exchange("after-hiding-panel")
	c.SetVisible(true)
	eventually(t, c, func(s desktop.Snapshot) bool { _, ok := find(s); return ok })
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	eventually(t, c, func(s desktop.Snapshot) bool {
		_, ok := find(s)
		return s.ConnectionsState == desktop.ConnectionsLive && !ok
	})
	c.SetConnectionsVisible(false)
	c.SetConnectionsVisible(true)
	eventually(t, c, func(s desktop.Snapshot) bool { return s.ConnectionsState == desktop.ConnectionsLive })
	if _, ok := find(c.Snapshot()); ok {
		t.Fatal("native closed history reappeared as active")
	}
	if dir := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.MarshalIndent(map[string]any{"schema": 1, "passed": true, "actualNativeCore": true, "usesRealTUN": false, "loopbackOnly": true, "observedRealConnection": true, "observedTrafficTotals": true, "sameStreamAfterPageClose": true, "sameStreamAfterPanelHide": true, "closedHistoryExcluded": true}, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "connections-native-e2e.json"), append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
