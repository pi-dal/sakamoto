package watch

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/recovery"
	"github.com/pi-dal/sakamoto/internal/sbclient"
)

func TestMaintenanceKeepsWatcherSocketButDoesNotDialOrTakeOver(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "watch-rescue-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	cfg := config.Default()
	cfg.Experiment.Mode = "off"
	path := filepath.Join(dir, "sakamoto.yaml")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := recovery.Hold(dir, "isolated rescue exercise"); err != nil {
		t.Fatal(err)
	}
	var dials atomic.Int32
	watcher := New(cfg, func(context.Context) (*sbclient.Client, error) { dials.Add(1); return nil, context.Canceled })
	watcher.SetConfigPath(path)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- watcher.Run(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("unix", recoveryPath(path)); err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	status, err := RequestRecovery(path)
	if err != nil || status != "maintenance" {
		t.Fatalf("paused recovery reply: %s %v", status, err)
	}
	if dials.Load() != 0 {
		t.Fatal("maintenance watcher contacted native API")
	}
	if _, err := os.Stat(filepath.Join(dir, "proxy-restore.json")); !os.IsNotExist(err) {
		t.Fatal("maintenance watcher changed proxy state")
	}
}
