package gen

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/experiment"
	"github.com/pi-dal/sakamoto/internal/sbclient"
)

func freeModeTestPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

// This fake SOCKS exit returns a marker; it never connects to a real host.
func modeTestSOCKS(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var header [2]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return
	}
	if header[0] != 5 {
		return
	}
	if _, err := io.CopyN(io.Discard, conn, int64(header[1])); err != nil {
		return
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return
	}
	var request [4]byte
	if _, err := io.ReadFull(conn, request[:]); err != nil || request[1] != 1 {
		return
	}
	switch request[3] {
	case 1:
		if _, err := io.CopyN(io.Discard, conn, 4); err != nil {
			return
		}
	case 4:
		if _, err := io.CopyN(io.Discard, conn, 16); err != nil {
			return
		}
	case 3:
		var n [1]byte
		if _, err := io.ReadFull(conn, n[:]); err != nil {
			return
		}
		if _, err := io.CopyN(io.Discard, conn, int64(n[0])); err != nil {
			return
		}
	default:
		return
	}
	if _, err := io.CopyN(io.Discard, conn, 2); err != nil {
		return
	}
	if _, err := conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
		return
	}
	if _, err := http.ReadRequest(bufio.NewReader(conn)); err != nil {
		return
	}
	_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 5\r\nConnection: close\r\n\r\nPROXY")
}

func TestNativeModeChangesActuallyRouteNewRequests(t *testing.T) {
	if _, err := exec.LookPath("sing-box"); err != nil {
		t.Skip("sing-box is required for native mode integration")
	}
	dir := t.TempDir()
	conf, nodes := filepath.Join(dir, "test.conf"), filepath.Join(dir, "nodes.txt")
	if err := os.WriteFile(conf, []byte("[General]\ndns-server=https://dns.google/dns-query\n[Rule]\nDOMAIN,localhost,DIRECT\nDOMAIN,ads.example,REJECT\nDOMAIN,proxy.example,PROXY\nFINAL,DIRECT\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nodes, []byte("hysteria2://synthetic@203.0.113.11:443#synthetic\nsocks://203.0.113.10:9000#Exit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.TailscaleOptimize = false
	cfg.NodesFile = nodes
	if err := Run(Options{ConfPath: conf, NodesFile: nodes, Cfg: cfg, OutDir: dir, Quiet: true}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	exit, err := experiment.ProxyOutbound(raw)
	if err != nil {
		t.Fatal(err)
	}
	var generated map[string]any
	if err := json.Unmarshal(raw, &generated); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	socks, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = socks.Close() }()
	go func() {
		for {
			conn, err := socks.Accept()
			if err != nil {
				return
			}
			hits.Add(1)
			go modeTestSOCKS(conn)
		}
	}()
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "DIRECT") }))
	defer direct.Close()
	mixedPort, apiPort := freeModeTestPort(t), freeModeTestPort(t)
	generated["inbounds"] = []any{map[string]any{"type": "mixed", "listen": "127.0.0.1", "listen_port": mixedPort}}
	generated["outbounds"] = []any{
		map[string]any{"type": "direct", "tag": "direct"},
		map[string]any{"type": "socks", "tag": exit, "server": "127.0.0.1", "server_port": socks.Addr().(*net.TCPAddr).Port, "version": "5"},
	}
	generated["services"] = []any{map[string]any{"type": "api", "listen": "127.0.0.1", "listen_port": apiPort, "secret": cfg.API.Secret, "dashboard": map[string]any{"enabled": false}}}
	generated["dns"] = map[string]any{"servers": []any{map[string]any{"type": "hosts", "tag": "test-hosts", "predefined": map[string]any{"localhost": "127.0.0.1"}}}, "final": "test-hosts"}
	route := generated["route"].(map[string]any)
	route["default_domain_resolver"] = map[string]any{"server": "test-hosts"}
	// The production private-address exemption would correctly bypass our
	// loopback test server in all modes. Remove ONLY that exemption here to
	// exercise generated mode rules against local marker endpoints, no TUN.
	var rules []any
	for _, item := range route["rules"].([]any) {
		r := item.(map[string]any)
		if r["ip_is_private"] == true {
			continue
		}
		rules = append(rules, r)
	}
	route["rules"] = rules
	candidate, err := json.Marshal(generated)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "mode-test.json")
	if err := os.WriteFile(path, candidate, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sing-box", "run", "-D", dir, "-c", path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var client *sbclient.Client
	for ctx.Err() == nil {
		probeCtx, stop := context.WithTimeout(ctx, 400*time.Millisecond)
		client, err = sbclient.Dial(probeCtx, fmt.Sprintf("http://127.0.0.1:%d", apiPort), cfg.API.Secret)
		stop()
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("isolated native API failed: %v", err)
	}
	defer client.Close()
	status, err := client.ClashModeStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(status.ModeList, ",") != "Rule,Global,Direct" {
		t.Fatalf("mode list: %v", status.ModeList)
	}
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", mixedPort))
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	targetURL, _ := url.Parse(direct.URL)
	_, port, _ := net.SplitHostPort(targetURL.Host)
	_, err = strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ mode, want string }{{"Rule", "DIRECT"}, {"Global", "PROXY"}, {"Direct", "DIRECT"}, {"Rule", "DIRECT"}} {
		if err := client.SetClashMode(ctx, tc.mode); err != nil {
			t.Fatal(err)
		}
		resp, err := httpClient.Get("http://localhost:" + port)
		if err != nil {
			t.Fatalf("%s request: %v", tc.mode, err)
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil || string(body) != tc.want {
			t.Fatalf("%s routed to %q want %q: %v", tc.mode, body, tc.want, readErr)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("only Global should use SOCKS exit; got %d connections", hits.Load())
	}
}
