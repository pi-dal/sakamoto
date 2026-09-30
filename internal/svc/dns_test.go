package svc

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
)

type dnsLifecycleSetup struct {
	servers     []string
	failRestore bool
	sets        int
}

func (f *dnsLifecycleSetup) run(args ...string) (string, error) {
	if args[0] == "-getdnsservers" {
		if len(f.servers) == 0 {
			return "There aren't any DNS Servers set on Wi-Fi.\n", nil
		}
		return strings.Join(f.servers, "\n") + "\n", nil
	}
	if args[0] != "-setdnsservers" {
		return "", errors.New("unexpected command")
	}
	if args[2] == "Empty" {
		if f.failRestore {
			return "", errors.New("restore denied")
		}
		f.servers = []string{}
	} else {
		f.servers = append([]string{}, args[2:]...)
	}
	f.sets++
	return "", nil
}
func nativeDNSServerForTest(t *testing.T) (*Server, *dnsLifecycleSetup) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := config.Default()
	cfg.DNSGuard.Enabled = true
	if err := cfg.Save(filepath.Join(dir, "sakamoto.yaml")); err != nil {
		t.Fatal(err)
	}
	core := map[string]any{
		"inbounds": []any{map[string]any{"type": "direct", "tag": "protected-dns", "listen": "127.0.0.1", "listen_port": 53}},
		"services": []any{map[string]any{"type": "api", "listen": "127.0.0.1", "secret": cfg.API.Secret, "dashboard": map[string]any{"enabled": false}}},
		"dns":      map[string]any{"final": "remote", "servers": []any{map[string]any{"tag": "remote", "type": "https", "detour": "Exit"}}},
		"route":    map[string]any{"rules": []any{map[string]any{"inbound": []string{"protected-dns"}, "action": "hijack-dns"}}},
	}
	b, err := json.Marshal(core)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	setup := &dnsLifecycleSetup{servers: []string{}}
	s := NewServer(path, dir)
	s.dnsRun = setup.run
	s.dnsProbe = func(string) error { return nil }
	s.dnsPorts = func(string) error { return nil }
	s.command = func(string, string, string) *exec.Cmd { return exec.Command("/bin/sleep", "30") }
	s.dnsWait = 10 * time.Millisecond
	t.Cleanup(func() { setup.failRestore = false; s.stop() })
	return s, setup
}
func TestSupervisorRestoresDNSBeforeStoppingListener(t *testing.T) {
	s, setup := nativeDNSServerForTest(t)
	if result := s.start(); !strings.HasPrefix(result, "connected") || !strings.Contains(result, "dns=protected") {
		t.Fatal(result)
	}
	if len(setup.servers) != 1 || setup.servers[0] != "127.0.0.1" {
		t.Fatal("DNS not protected after native health")
	}
	setup.failRestore = true
	if result := s.stop(); !strings.HasPrefix(result, "disconnect failed") {
		t.Fatal("stopped resolver despite failed restoration", result)
	}
	if !strings.HasPrefix(s.status(), "connected") {
		t.Fatal("core lost on restore failure")
	}
	setup.failRestore = false
	if result := s.stop(); !strings.HasPrefix(result, "disconnected") {
		t.Fatal(result)
	}
	if len(setup.servers) != 0 {
		t.Fatal("DHCP DNS was not restored before core stop")
	}
}
func TestSupervisorFailedHealthDoesNotChangeSystemDNS(t *testing.T) {
	s, setup := nativeDNSServerForTest(t)
	s.dnsProbe = func(string) error { return errors.New("synthetic DoH unavailable") }
	if result := s.start(); !strings.HasPrefix(result, "start failed") {
		t.Fatal("unhealthy native DNS accepted", result)
	}
	if setup.sets != 0 || len(setup.servers) != 0 {
		t.Fatal("DNS changed before native UDP/TCP health passed")
	}
}
func TestSupervisorPortConflictDoesNotStartCoreOrChangeDNS(t *testing.T) {
	s, setup := nativeDNSServerForTest(t)
	s.dnsPorts = func(string) error { return errors.New("port occupied") }
	if result := s.start(); !strings.HasPrefix(result, "start failed") {
		t.Fatal(result)
	}
	if s.child != nil || setup.sets != 0 {
		t.Fatal("port conflict changed lifecycle or DNS")
	}
}
