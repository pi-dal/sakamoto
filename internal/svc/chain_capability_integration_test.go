package svc

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
)

// An installed 0.2.25 supervisor can regenerate, but would silently ignore
// chain_exit. The new client must require its explicit generator capability.
func TestRepairRefusesSupervisorWithoutGenericChain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sakamoto.yaml")
	cfg := config.Default()
	cfg.ChainExit = "Landing"
	recoveryOK(t, cfg.Save(path))
	before, err := os.ReadFile(path)
	recoveryOK(t, err)
	listener, err := net.Listen("unix", filepath.Join(dir, SockName))
	recoveryOK(t, err)
	var mutations atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(conn).ReadString('\n')
			reply := `{"capabilities":["configuration-repair-v1"]}`
			if strings.TrimSpace(line) != "info" {
				mutations.Add(1)
				reply = `{"verified":false,"detail":"unexpected repair"}`
			}
			_, _ = fmt.Fprintln(conn, reply)
			_ = conn.Close()
		}
	}()
	defer func() { _ = listener.Close(); <-done }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := RepairAt(ctx, path, RepairOptions{Regenerate: true, Restart: true})
	if err == nil || !result.UpgradeRequired || result.Problem != "service_upgrade_required" || mutations.Load() != 0 {
		t.Fatal("old generator received chain regeneration", result, err, mutations.Load())
	}
	after, err := os.ReadFile(path)
	recoveryOK(t, err)
	if string(before) != string(after) {
		t.Fatal("capability check changed saved config")
	}
	writeRecoveryArtifact(t, map[string]any{"scenario": "generic-chain-old-generator", "upgrade_required_before_repair": true, "no_disconnect_or_regenerate": true, "source_preserved": true, "real_unix_socket": true})
}
