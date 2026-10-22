package rescue

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pi-dal/sakamoto/internal/recovery"
)

func rescueOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func rescueWrite(t *testing.T, file *os.File, text string) {
	t.Helper()
	_, err := file.WriteString(text)
	rescueOK(t, err)
}

// The fake macOS boundary runs as separate processes against a private JSON
// state file. It never calls real launchctl/networksetup or touches a live VPN.
func TestOfflineOSProcess(t *testing.T) {
	if os.Getenv("SAKAMOTO_FAKE_OS") != "1" {
		return
	}
	args := os.Args
	index := 0
	for i, arg := range args {
		if arg == "--" {
			index = i + 1
			break
		}
	}
	if index == 0 {
		os.Exit(2)
	}
	command, args := args[index], args[index+1:]
	statePath := os.Getenv("SAKAMOTO_FAKE_STATE")
	raw, _ := os.ReadFile(statePath)
	var state map[string]any
	rescueOK(t, json.Unmarshal(raw, &state))
	if strings.HasSuffix(command, "launchctl") {
		rescueWrite(t, os.Stderr, "Could not find service\n")
		os.Exit(113)
	}
	if !strings.HasSuffix(command, "networksetup") {
		os.Exit(2)
	}
	switch args[0] {
	case "-getdnsservers":
		dns := state["dns"].([]any)
		if len(dns) == 0 {
			rescueWrite(t, os.Stdout, "There aren't any DNS Servers set on Test Wi-Fi.\n")
		} else {
			for _, ip := range dns {
				rescueWrite(t, os.Stdout, ip.(string)+"\n")
			}
		}
	case "-setdnsservers":
		dns := []any{}
		if args[2] != "Empty" {
			for _, ip := range args[2:] {
				dns = append(dns, ip)
			}
		}
		state["dns"] = dns
	default:
		kind := "http"
		if strings.Contains(args[0], "secureweb") {
			kind = "https"
		}
		p := state[kind].(map[string]any)
		switch {
		case strings.HasPrefix(args[0], "-get"):
			enabled := "No"
			if p["enabled"] == true {
				enabled = "Yes"
			}
			port := p["port"].(float64)
			rescueWrite(t, os.Stdout, "Enabled: "+enabled+"\nServer: "+p["server"].(string)+"\nPort: "+formatPort(port)+"\nAuthenticated Proxy Enabled: 0\n")
		case strings.HasSuffix(args[0], "proxystate"):
			p["enabled"] = args[2] == "on"
		default:
			p["server"] = args[2]
			p["port"] = json.Number(args[3])
		}
	}
	raw, _ = json.Marshal(state)
	rescueOK(t, os.WriteFile(statePath, raw, 0600))
	os.Exit(0)
}

func formatPort(value float64) string { raw, _ := json.Marshal(int(value)); return string(raw) }

func TestOfflineRescueWithoutAnyApplicationProcess(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "fake-os.json")
	rescueOK(t, os.WriteFile(state, []byte(`{"dns":["127.0.0.1"],"http":{"enabled":true,"server":"127.0.0.1","port":2334},"https":{"enabled":true,"server":"127.0.0.1","port":2334}}`), 0600))
	rescueOK(t, os.WriteFile(filepath.Join(dir, "dns-restore.json"), []byte(`{"service":"Test Wi-Fi","servers":[]}`), 0600))
	rescueOK(t, os.WriteFile(filepath.Join(dir, "proxy-restore.json"), []byte(`{"service":"Test Wi-Fi","http":{"enabled":false,"server":"","port":0},"https":{"enabled":false,"server":"","port":0},"managed":{"enabled":true,"server":"127.0.0.1","port":2334}}`), 0600))
	sys := System{EUID: 0, UID: os.Getuid(), Home: dir, Run: func(ctx context.Context, command string, args ...string) (string, error) {
		argv := append([]string{"-test.run=^TestOfflineOSProcess$", "--", command}, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], argv...)
		cmd.Env = append(os.Environ(), "SAKAMOTO_FAKE_OS=1", "SAKAMOTO_FAKE_STATE="+state, "http_proxy=http://127.0.0.1:1", "https_proxy=http://127.0.0.1:1")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}}
	for i := 0; i < 2; i++ {
		outcome, err := Exit(context.Background(), dir, sys)
		if err != nil {
			t.Fatal(err)
		}
		if !outcome.DNSRestored || !outcome.ProxyRestored || !recovery.Held(dir) {
			t.Fatalf("incomplete rescue: %+v", outcome)
		}
	}
	raw, _ := os.ReadFile(state)
	var after map[string]any
	rescueOK(t, json.Unmarshal(raw, &after))
	if len(after["dns"].([]any)) != 0 || after["http"].(map[string]any)["enabled"] != false || after["https"].(map[string]any)["enabled"] != false {
		t.Fatalf("OS settings not restored: %s", raw)
	}
	if dir := os.Getenv("SAKAMOTO_RECOVERY_ARTIFACT_DIR"); dir != "" {
		rescueOK(t, os.MkdirAll(dir, 0700))
		rescueOK(t, os.WriteFile(filepath.Join(dir, "offline-rescue.json"), []byte(`{"supervisor":false,"watcher":false,"core":false,"dns_restored_to_dhcp":true,"proxy_restored":true,"idempotent":true,"maintenance_retained":true,"os_boundary":"isolated subprocess fixture"}`), 0600))
	}
}

func TestOfflineRescuePreservesSnapshotWhenOSRestorationFails(t *testing.T) {
	dir := t.TempDir()
	rescueOK(t, os.WriteFile(filepath.Join(dir, "dns-restore.json"), []byte(`{"service":"Test Wi-Fi","servers":[]}`), 0600))
	sys := System{EUID: 0, UID: os.Getuid(), Home: dir, Run: func(_ context.Context, command string, _ ...string) (string, error) {
		if strings.HasSuffix(command, "launchctl") {
			return "Could not find service", errors.New("113")
		}
		return "", errors.New("OS command unavailable")
	}}
	if _, err := Exit(context.Background(), dir, sys); err == nil {
		t.Fatal("restoration failure reported success")
	}
	if _, err := os.Stat(filepath.Join(dir, "dns-restore.json")); err != nil {
		t.Fatal("lost DNS evidence")
	}
	if !recovery.Held(dir) {
		t.Fatal("failed rescue resumed takeover")
	}
}

func TestOfflineRescueRefusesUnprivilegedExit(t *testing.T) {
	called := false
	sys := System{EUID: 501, Run: func(context.Context, string, ...string) (string, error) { called = true; return "", nil }}
	if _, err := Exit(context.Background(), t.TempDir(), sys); err == nil || called {
		t.Fatal("unprivileged exit touched OS controls")
	}
}
