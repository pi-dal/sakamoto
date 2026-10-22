package watch

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/experiment"
	"github.com/pi-dal/sakamoto/internal/sbclient"
	"github.com/sagernet/sing-box/daemon"
	"google.golang.org/grpc"
)

// Real watcher socket, independent native event streams and deliberately slow
// local origin exits. Recovery must answer while the learning requests wait.
func TestRecoveryRemainsResponsiveDuringOriginVerification(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "sakamoto-live-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	var slow atomic.Int32
	slow.Store(-502)
	inbounds := []any{}
	rules := []any{}
	for _, tag := range []string{"learning-direct", "learning-proxy", "learning-route"} {
		port := learningSOCKS(t, &slow)
		inbounds = append(inbounds, map[string]any{"type": "mixed", "tag": tag, "listen": "127.0.0.1", "listen_port": port, "users": []any{map[string]any{"username": "sakamoto-learning", "password": "verification-fixture-unused-credential-00000000"}}})
		if tag != "learning-route" {
			outbound := "direct"
			if tag == "learning-proxy" {
				outbound = "proxy"
			}
			rules = append(rules, map[string]any{"inbound": []string{tag}, "action": "route", "outbound": outbound})
		}
	}
	rules = append(rules, map[string]any{"rule_set": []string{"rs-proxy"}, "action": "route", "outbound": "proxy"})
	raw, _ := json.Marshal(map[string]any{"inbounds": inbounds, "route": map[string]any{"final": "direct", "rules": rules}})
	raw, err = experiment.PrepareLiveRules(raw, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := experiment.WriteLiveRules(dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if !experiment.ProbeReady(raw) {
		t.Fatal("fixture verification listeners are invalid")
	}
	if !experiment.LiveRulesReady(raw, dir) {
		t.Fatal("fixture live slot is invalid")
	}
	cfg := config.Default()
	cfg.Experiment.Mode = "auto"
	cfg.FallbackEnabled = false
	cfg.MixedInbound.Enabled = false
	cfg.ICloud.Enabled = false
	cfg.S3.Enabled = false
	cfg.CheckInterval = 50 * time.Millisecond
	cfgPath := filepath.Join(dir, "sakamoto.yaml")
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	fixture := &learningStreamCore{interval: make(chan int64, 1), stopped: make(chan struct{}), port: "80", startedAt: time.Now().Add(time.Millisecond).UnixMilli()}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	daemon.RegisterStartedServiceServer(server, fixture)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	watcher := New(cfg, func(ctx context.Context) (*sbclient.Client, error) {
		return sbclient.Dial(ctx, "http://"+listener.Addr().String(), "")
	})
	watcher.SetConfigPath(cfgPath)
	done := make(chan error, 1)
	go func() { done <- watcher.Run(ctx) }()
	defer func() { cancel(); <-done }()
	for !watcher.learningBusy.Load() && ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !watcher.learningBusy.Load() {
		history, _ := experiment.ReadReport(dir)
		for {
			select {
			case event := <-watcher.Events():
				t.Log(event.Message)
			default:
				t.Fatalf("correlated candidate did not start verification; history=%+v", history)
			}
		}
	}
	started := time.Now()
	status, err := RequestRecovery(cfgPath)
	elapsed := time.Since(started)
	if err != nil || status != "busy" || elapsed > 500*time.Millisecond {
		t.Fatalf("recovery during verification: status=%s duration=%s error=%v", status, elapsed, err)
	}
	actual, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil || string(actual) != string(raw) {
		t.Fatal("verification rewrote configuration")
	}
	if artifact := os.Getenv("SAKAMOTO_TEST_ARTIFACT_DIR"); artifact != "" {
		if err := os.MkdirAll(artifact, 0700); err != nil {
			t.Fatal(err)
		}
		evidence, _ := json.MarshalIndent(map[string]any{"recovery_status": status, "response_ms": elapsed.Milliseconds(), "slow_origin_ms": 2000, "config_unchanged": true}, "", "  ")
		if err := os.WriteFile(filepath.Join(artifact, "recovery-during-learning.json"), evidence, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
