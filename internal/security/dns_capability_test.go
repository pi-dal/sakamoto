package security

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
)

func TestProtectedConnectRefusesLegacyRootWithoutChangingFiles(t *testing.T) {
	dir, err := os.MkdirTemp("", "sr-dns-cap-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	t.Setenv("SAKAMOTO_DIR", dir)
	cfg := config.Default()
	cfg.DNSGuard.Enabled = true
	path := filepath.Join(dir, "sakamoto.yaml")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(dir, "svc.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	seen := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		line, _ := bufio.NewReader(conn).ReadString('\n')
		seen <- line
		_, _ = conn.Write([]byte("unknown: capabilities\n"))
	}()
	if _, _, err := ConnectWithPending(path); err == nil || !strings.Contains(err.Error(), "root daemon lacks") {
		t.Fatal("legacy root was accepted", err)
	}
	if command := <-seen; command != "capabilities\n" {
		t.Fatalf("unsafe command issued to old root: %q", command)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("refused connection changed sidecar", err)
	}
}
