package gen

import (
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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
)

// Real VLESS entry -> real VLESS landing -> loopback origin, no TUN or owner data.
func TestNativeGenericChainExit(t *testing.T) {
	engine, err := exec.LookPath("sing-box")
	if err != nil {
		t.Skip("sing-box is required for the native chain workflow")
	}
	dir := t.TempDir()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "CHAIN-ORIGIN") }))
	defer origin.Close()
	entryPort, exitPort, mixedPort := freeModeTestPort(t), freeModeTestPort(t), freeModeTestPort(t)
	const entryID = "00000000-0000-4000-8000-000000000001"
	const exitID = "00000000-0000-4000-8000-000000000002"
	start := func(name string, content map[string]any, port int) (*exec.Cmd, string) {
		t.Helper()
		path := filepath.Join(dir, name+".json")
		data, marshalErr := json.Marshal(content)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if writeErr := os.WriteFile(path, data, 0600); writeErr != nil {
			t.Fatal(writeErr)
		}
		if output, checkErr := exec.Command(engine, "check", "-D", dir, "-c", path).CombinedOutput(); checkErr != nil {
			t.Fatalf("%s native check: %v: %s", name, checkErr, output)
		}
		logPath := filepath.Join(dir, name+".log")
		log, openErr := os.Create(logPath)
		if openErr != nil {
			t.Fatal(openErr)
		}
		cmd := exec.Command(engine, "run", "-D", dir, "-c", path)
		cmd.Stdout, cmd.Stderr = log, log
		if startErr := cmd.Start(); startErr != nil {
			_ = log.Close()
			t.Fatal(startErr)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait(); _ = log.Close() })
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
			if dialErr == nil {
				_ = conn.Close()
				return cmd, logPath
			}
			time.Sleep(25 * time.Millisecond)
		}
		output, _ := os.ReadFile(logPath)
		t.Fatalf("%s did not start: %s", name, output)
		return nil, ""
	}
	serverConfig := func(port int, id string) map[string]any {
		return map[string]any{"log": map[string]any{"level": "info"}, "inbounds": []any{map[string]any{"type": "vless", "tag": "vless-in", "listen": "127.0.0.1", "listen_port": port, "users": []any{map[string]any{"uuid": id}}}}, "outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}}, "route": map[string]any{"final": "direct"}}
	}
	_, entryLog := start("entry", serverConfig(entryPort, entryID), entryPort)
	landing, landingLog := start("landing", serverConfig(exitPort, exitID), exitPort)
	entry := map[string]any{"type": "vless", "tag": "Entry", "server": "127.0.0.1", "server_port": entryPort, "uuid": entryID}
	exit := map[string]any{"type": "vless", "tag": "Landing", "server": "127.0.0.1", "server_port": exitPort, "uuid": exitID}
	feedNodes := []map[string]any{entry, exit}
	var feedJSON atomic.Value
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, feedJSON.Load().(string)) }))
	defer feed.Close()
	conf := filepath.Join(dir, "test.conf")
	if err := os.WriteFile(conf, []byte("[General]\ndns-server=https://dns.example/dns-query\n[Rule]\nDOMAIN,proxy.example,PROXY\nDOMAIN,direct.example,DIRECT\nDOMAIN,ads.example,REJECT\nFINAL,DIRECT\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.TailscaleOptimize = false
	cfg.ChainExit = "Landing"
	cfg.Experiment.Mode = "on"
	cfg.URLTest.URL, cfg.URLTest.Interval = origin.URL, "10m"
	cfg.Subscriptions = []config.SubSource{{Name: "fixture", URL: feed.URL, Format: "singbox"}}
	generate := func() ([]byte, error) {
		payload, err := json.Marshal(map[string]any{"outbounds": feedNodes})
		if err != nil {
			return nil, err
		}
		feedJSON.Store(string(payload))
		err = Run(Options{ConfPath: conf, Cfg: cfg, OutDir: dir, Quiet: true, OmitLearningProbe: true})
		if err != nil {
			return nil, err
		}
		return os.ReadFile(filepath.Join(dir, "config.json"))
	}
	raw, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	for _, value := range root["outbounds"].([]any) {
		row := value.(map[string]any)
		if row["tag"] == "Landing" && row["detour"] != "MainProxy" {
			t.Fatal("landing does not dial through MainProxy")
		}
		if members, ok := row["outbounds"].([]any); ok {
			for _, member := range members {
				if member == "Landing" {
					t.Fatal("landing remains in an entry group")
				}
			}
		}
	}
	if root["route"].(map[string]any)["final"] != "Landing" {
		t.Fatal("proxy fallback bypasses landing")
	}
	for _, value := range root["dns"].(map[string]any)["servers"].([]any) {
		row := value.(map[string]any)
		if detour, exists := row["detour"]; exists && detour != "Landing" {
			t.Fatalf("DNS bypasses chain: %v", detour)
		}
	}
	// Keep generated group/detour wiring and rules. Remove only TUN, API,
	// public DNS, and private-address bypass for the loopback-only workflow.
	root["inbounds"] = []any{map[string]any{"type": "mixed", "listen": "127.0.0.1", "listen_port": mixedPort}}
	delete(root, "services")
	root["dns"] = map[string]any{"servers": []any{map[string]any{"type": "hosts", "tag": "test-hosts"}}, "final": "test-hosts"}
	route := root["route"].(map[string]any)
	route["default_domain_resolver"] = "test-hosts"
	var rules []any
	for _, value := range route["rules"].([]any) {
		if value.(map[string]any)["ip_is_private"] != true {
			rules = append(rules, value)
		}
	}
	route["rules"] = rules
	_, _ = start("client", root, mixedPort)
	proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", mixedPort))
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	request := func() (string, error) {
		response, err := client.Get(origin.URL)
		if err != nil {
			return "", err
		}
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		if response.StatusCode != 200 {
			return string(body), fmt.Errorf("HTTP %d", response.StatusCode)
		}
		return string(body), err
	}
	if body, err := request(); err != nil || body != "CHAIN-ORIGIN" {
		t.Fatalf("VLESS chain request: %q %v", body, err)
	}
	entryEvidence, _ := os.ReadFile(entryLog)
	landingEvidence, _ := os.ReadFile(landingLog)
	if !strings.Contains(string(entryEvidence), fmt.Sprintf("127.0.0.1:%d", exitPort)) || !strings.Contains(string(landingEvidence), strings.TrimPrefix(origin.URL, "http://")) {
		t.Fatal("native logs did not confirm both hops")
	}
	_ = landing.Process.Kill()
	_, _ = landing.Process.Wait()
	if body, err := request(); err == nil && body == "CHAIN-ORIGIN" {
		t.Fatal("failed landing silently bypassed")
	}

	// Invalid selections must preserve the last generated files.
	for _, invalid := range []string{"Missing", "MainProxy"} {
		cfg.ChainExit = invalid
		if _, err := generate(); err == nil {
			t.Fatalf("accepted invalid exit %s", invalid)
		}
		after, _ := os.ReadFile(filepath.Join(dir, "config.json"))
		if string(after) != string(raw) {
			t.Fatal("failed generation replaced config")
		}
	}
	cfg.ChainExit = "Landing"
	entry["detour"] = "Landing"
	if _, err := generate(); err == nil {
		t.Fatal("accepted indirect entry/exit cycle")
	}
	delete(entry, "detour")
	feedNodes = []map[string]any{exit}
	if _, err := generate(); err == nil {
		t.Fatal("accepted explicit chain without upstream")
	}
	feedNodes = []map[string]any{entry, exit, {"type": "socks", "tag": "First SOCKS", "server": "127.0.0.1", "server_port": 1080}, {"type": "socks", "tag": "Second SOCKS", "server": "127.0.0.1", "server_port": 1081}}
	cfg.ChainExit = ""
	legacy, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(legacy, &root); err != nil {
		t.Fatal(err)
	}
	if root["route"].(map[string]any)["final"] != "First SOCKS" {
		t.Fatal("legacy SOCKS selection changed")
	}
	cfg.SocksExit = "Landing"
	if _, err := generate(); err != nil {
		t.Fatal(err)
	}
	cfg.ChainEnabled = false
	disabled, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(disabled, &root); err != nil {
		t.Fatal(err)
	}
	if root["route"].(map[string]any)["final"] != "MainProxy" {
		t.Fatal("disabling chain did not restore entry routing")
	}

	if artifact := os.Getenv("SAKAMOTO_CHAIN_ARTIFACT_DIR"); artifact != "" {
		if err := os.MkdirAll(artifact, 0700); err != nil {
			t.Fatal(err)
		}
		receipt, _ := json.MarshalIndent(map[string]any{"passed": true, "vless_to_vless": true, "both_native_hops_observed": true, "no_bypass_when_landing_fails": true, "exit_removed_from_groups": true, "dns_uses_exit": true, "invalid_exit_preserves_config": true, "cycle_rejected": true, "missing_upstream_rejected": true, "legacy_first_socks_retained": true, "legacy_explicit_alias": true, "disable_restores_entry_routing": true}, "", "  ")
		if err := os.WriteFile(filepath.Join(artifact, "native-chain.json"), receipt, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
