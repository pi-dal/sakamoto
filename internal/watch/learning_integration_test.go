package watch

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/experiment"
	"github.com/pi-dal/sakamoto/internal/sbclient"
)

// The real core handles authenticated ingress, routing, SOCKS and reconnects.
// Only the two remote origins are synthetic, so failures are deterministic.
func TestNativeLearningVerifiesRecoveryAndRollsBack(t *testing.T) {
	if _, err := exec.LookPath("sing-box"); err != nil {
		t.Skip("native core required")
	}
	var report []map[string]any
	for _, tc := range []struct {
		name                 string
		direct, proxy, after int
		learned              bool
	}{
		{"stale_generated_config", 502, 200, 200, false},
		{"missing_live_slot", 502, 200, 200, false},
		{"proxy_recovers", 502, 200, 200, true},
		{"direct_recovered", 200, 200, 200, false},
		{"both_fail", 502, 502, 502, false},
		{"proxy_denied", 502, 403, 403, false},
		{"proxy_rate_limited", 502, 429, 429, false},
		{"post_install_regression", 502, 200, 502, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var direct, proxied atomic.Int32
			direct.Store(int32(tc.direct))
			proxied.Store(int32(tc.proxy))
			directPort := learningSOCKS(t, &direct)
			proxyPort := learningSOCKS(t, &proxied, tc.proxy, tc.after)
			echoPort := learningEcho(t)
			streamPort := learningPort(t)
			api := learningPort(t)
			cfg := config.Default()
			cfg.Experiment.Mode = "auto"
			cfg.API.URL = fmt.Sprintf("http://127.0.0.1:%d", api)
			cfgPath := filepath.Join(dir, "sakamoto.yaml")
			if err := cfg.Save(cfgPath); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]any{
				"log":       map[string]any{"level": "error"},
				"outbounds": []any{map[string]any{"type": "socks", "tag": "direct", "server": "127.0.0.1", "server_port": directPort}, map[string]any{"type": "socks", "tag": "exit", "server": "127.0.0.1", "server_port": proxyPort}, map[string]any{"type": "direct", "tag": "stream-direct"}},
				"inbounds":  []any{map[string]any{"type": "direct", "tag": "protected-dns", "listen": "127.0.0.1", "listen_port": learningPort(t)}, map[string]any{"type": "direct", "tag": "stream", "listen": "127.0.0.1", "listen_port": streamPort, "override_address": "127.0.0.1", "override_port": echoPort}},
				"route":     map[string]any{"final": "direct", "rules": []any{map[string]any{"inbound": []string{"protected-dns"}, "action": "hijack-dns"}, map[string]any{"inbound": []string{"stream"}, "action": "route", "outbound": "stream-direct"}, map[string]any{"rule_set": []string{"rs-proxy"}, "action": "route", "outbound": "exit"}}, "rule_set": []any{map[string]any{"type": "inline", "tag": "rs-proxy", "rules": []any{map[string]any{"domain": []string{"explicit.example"}}}}}},
				"services":  []any{map[string]any{"type": "api", "listen": "127.0.0.1", "listen_port": api, "secret": cfg.API.Secret}},
			})
			prepared, err := experiment.PrepareProbe(raw)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name != "missing_live_slot" {
				prepared, err = experiment.PrepareLiveRules(prepared, dir, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err = experiment.WriteLiveRules(dir, nil); err != nil {
					t.Fatal(err)
				}
			}
			var checked struct {
				Route struct {
					Rules []struct {
						Action  string
						Inbound []string
					}
				}
			}
			if err = json.Unmarshal(prepared, &checked); err != nil {
				t.Fatal(err)
			}
			if len(checked.Route.Rules) == 0 || checked.Route.Rules[0].Action != "hijack-dns" || len(checked.Route.Rules[0].Inbound) != 1 || checked.Route.Rules[0].Inbound[0] != "protected-dns" {
				t.Fatal("learning ingress displaced the protected DNS lifecycle rule")
			}
			configFile := filepath.Join(dir, "config.json")
			if err = os.WriteFile(configFile, prepared, 0600); err != nil {
				t.Fatal(err)
			}
			var process *exec.Cmd
			stop := func() {
				if process != nil {
					_ = process.Process.Kill()
					_ = process.Wait()
					process = nil
				}
			}
			t.Cleanup(stop)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			start := func() error {
				stop()
				process = exec.CommandContext(ctx, "sing-box", "run", "-D", dir, "-c", configFile)
				logFile, e := os.OpenFile(filepath.Join(dir, "core.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
				if e != nil {
					return e
				}
				defer func() { _ = logFile.Close() }()
				process.Stdout = logFile
				process.Stderr = logFile
				if e = process.Start(); e != nil {
					return e
				}
				for ctx.Err() == nil {
					client, e := sbclient.Dial(ctx, cfg.API.URL, cfg.API.Secret)
					if e == nil {
						client.Close()
						return nil
					}
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(25 * time.Millisecond):
					}
				}
				return ctx.Err()
			}
			if err = start(); err != nil {
				t.Fatal(err)
			}
			stream, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", streamPort))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stream.Close() }()
			echo := func() {
				_ = stream.SetDeadline(time.Now().Add(3 * time.Second))
				if _, err := stream.Write([]byte("existing-ssh")); err != nil {
					t.Fatal(err)
				}
				payload := make([]byte, 12)
				if _, err := io.ReadFull(stream, payload); err != nil || string(payload) != "existing-ssh" {
					t.Fatalf("existing TCP stream interrupted: %v", err)
				}
			}
			echo()
			var statusMessages int
			if tc.name == "proxy_recovers" {
				client, e := sbclient.Dial(ctx, cfg.API.URL, cfg.API.Secret)
				if e != nil {
					t.Fatal(e)
				}
				statusCtx, stopStatus := context.WithTimeout(ctx, 2300*time.Millisecond)
				statuses, _ := client.SubscribeStatus(statusCtx, time.Nanosecond)
				for range statuses {
					statusMessages++
				}
				stopStatus()
				client.Close()
				if statusMessages < 2 || statusMessages > 4 {
					t.Fatalf("native status floor failed: %d updates in 2.3s", statusMessages)
				}
			}
			w := New(cfg, func(ctx context.Context) (*sbclient.Client, error) {
				return sbclient.Dial(ctx, cfg.API.URL, cfg.API.Secret)
			})
			w.SetConfigPath(cfgPath)
			epoch, err := w.learningCoreEpoch(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "stale_generated_config" {
				future := time.Now().Add(time.Second)
				if err := os.Chtimes(configFile, future, future); err != nil {
					t.Fatal(err)
				}
			}
			result := w.learnCandidate(ctx, experiment.Candidate{Domain: "origin.example", Port: "80", Failures: 1, Required: 3, Reason: "direct dial failure"})
			state, err := experiment.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(state.Domains) > 0; got != tc.learned {
				t.Fatalf("learned=%v want=%v result=%+v", got, tc.learned, result)
			}
			afterEpoch, err := w.learningCoreEpoch(ctx)
			if err != nil || !afterEpoch.Equal(epoch) {
				t.Fatalf("learning replaced the core: %v", err)
			}
			if process.ProcessState != nil {
				t.Fatal("learning stopped the native core")
			}
			actual, e := os.ReadFile(configFile)
			if e != nil {
				t.Fatal(e)
			}
			if string(actual) != string(prepared) {
				t.Fatal("live learning rewrote the generated configuration")
			}
			if !tc.learned {
				actual, e := os.ReadFile(configFile)
				if e != nil {
					t.Fatal(e)
				}
				if string(actual) != string(prepared) {
					t.Fatal("unsuccessful learning changed generated config")
				}
			}
			echo()
			if (tc.name == "missing_live_slot" || tc.name == "stale_generated_config") && result.State != "deferred" {
				t.Fatalf("missing slot must defer without reconnect: %+v", result)
			}
			if result.State == "" {
				t.Fatal("missing learning explanation")
			}
			report = append(report, map[string]any{"case": tc.name, "state": result.State, "learned": tc.learned, "restarts": 0, "direct_http": tc.direct, "proxy_http": tc.proxy, "after_http": tc.after, "same_tcp_stream": true, "native_status_messages_2_3s": statusMessages})
		})
	}
	if dir := os.Getenv("SAKAMOTO_TEST_ARTIFACT_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "desktop-learning.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func learningEcho(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var accepting, workers sync.WaitGroup
	accepting.Add(1)
	t.Cleanup(func() { _ = listener.Close(); accepting.Wait(); workers.Wait() })
	go func() {
		defer accepting.Done()
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer func() { _ = connection.Close() }()
				_, _ = io.Copy(connection, connection)
			}()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
}

func learningPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return p
}

func learningSOCKS(t *testing.T, code *atomic.Int32, responses ...int) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	var workers sync.WaitGroup
	t.Cleanup(func() { _ = l.Close(); workers.Wait() })
	go func() {
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer func() { _ = c.Close() }()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				var head [2]byte
				if _, e := io.ReadFull(c, head[:]); e != nil {
					return
				}
				if _, e := io.CopyN(io.Discard, c, int64(head[1])); e != nil {
					return
				}
				if _, e := c.Write([]byte{5, 0}); e != nil {
					return
				}
				var req [4]byte
				if _, e := io.ReadFull(c, req[:]); e != nil {
					return
				}
				n := 0
				switch req[3] {
				case 1:
					n = 4
				case 4:
					n = 16
				case 3:
					var size [1]byte
					if _, e := io.ReadFull(c, size[:]); e != nil {
						return
					}
					n = int(size[0])
				default:
					return
				}
				if _, e := io.CopyN(io.Discard, c, int64(n+2)); e != nil {
					return
				}
				if _, e := c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); e != nil {
					return
				}
				request, e := http.ReadRequest(bufio.NewReader(c))
				if e != nil {
					return
				}
				_ = request.Body.Close()
				status := int(code.Load())
				if status < 0 {
					time.Sleep(2 * time.Second)
					status = -status
				}
				if len(responses) > 0 {
					status = responses[min(int(requests.Add(1))-1, len(responses)-1)]
				}
				_, _ = io.WriteString(c, "HTTP/1.1 "+strconv.Itoa(status)+" "+http.StatusText(status)+"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}
