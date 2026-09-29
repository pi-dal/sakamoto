package watch

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/sagernet/sing-box/daemon"
)

func recoverySnapshot(selected string) *daemon.Groups {
	return &daemon.Groups{Group: []*daemon.Group{
		{Tag: "MainProxy", Selected: selected},
		{Tag: "RealityAuto", Items: []*daemon.GroupItem{{Tag: "r1"}}},
		{Tag: "OthersAuto", Items: []*daemon.GroupItem{{Tag: "o1"}}},
	}}
}
func TestRecoveryDecisionSafety(t *testing.T) {
	chain := []string{"RealityAuto", "OthersAuto"}
	now := time.Now()
	for _, tc := range []struct {
		enabled  bool
		chain    []string
		snap     *daemon.Groups
		settling bool
		last     time.Time
		want     string
	}{
		{false, chain, recoverySnapshot("RealityAuto"), false, time.Time{}, "disabled"},
		{true, []string{"RealityAuto"}, recoverySnapshot("RealityAuto"), false, time.Time{}, "disabled"},
		{true, chain, nil, false, time.Time{}, "unavailable"},
		{true, chain, recoverySnapshot("ManualPick"), false, time.Time{}, "manual"},
		{true, chain, recoverySnapshot("RealityAuto"), true, time.Time{}, "busy"},
		{true, chain, recoverySnapshot("RealityAuto"), false, now.Add(-time.Second), "cooldown"},
		{true, chain, recoverySnapshot("RealityAuto"), false, now.Add(-2 * time.Minute), "queued"},
	} {
		if got := recoveryDecision(tc.enabled, tc.chain, tc.snap, tc.settling, tc.last, now); got != tc.want {
			t.Fatalf("got %q want %q", got, tc.want)
		}
	}
}
func TestUserOnlyRecoverySocketAndSingleWatcher(t *testing.T) {
	dir, err := os.MkdirTemp("", "sr-watch-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	cfgPath := filepath.Join(dir, "sakamoto.yaml")
	cfg := config.Default()
	w := New(cfg, nil)
	w.SetConfigPath(cfgPath)
	ctx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan error, 1), make(chan struct{})
	go func() { w.serveRecovery(ctx, ready); close(done) }()
	defer func() { cancel(); <-done }()
	if err := <-ready; err != nil {
		select {
		case ev := <-w.Events():
			t.Fatalf("%v: %s", err, ev.Message)
		default:
			t.Fatal(err)
		}
	}
	info, err := os.Stat(recoveryPath(cfgPath))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("socket mode: %v %v", info, err)
	}
	lock, err := os.Stat(filepath.Join(dir, recoveryLockName))
	if err != nil || lock.Mode().Perm() != 0600 {
		t.Fatalf("lock mode: %v %v", lock, err)
	}
	go func() { req := <-w.recoveryCh; req.reply <- "queued" }()
	if got, err := RequestRecovery(cfgPath); err != nil || got != "queued" {
		t.Fatalf("trigger response: %q %v", got, err)
	}
	conn, err := net.DialTimeout("unix", recoveryPath(cfgPath), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("disconnect\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	_ = conn.Close()
	if err != nil || line != "unavailable\n" {
		t.Fatalf("unsupported command: %q %v", line, err)
	}
	other := New(cfg, nil)
	other.SetConfigPath(cfgPath)
	otherCtx, otherCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer otherCancel()
	if err := other.Run(otherCtx); err == nil {
		t.Fatal("a second watcher must not control the same selector")
	}
}
