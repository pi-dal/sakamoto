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
)

func TestOfflineRescueRetainsExternalDNSAndProxyChanges(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "fake-os.json")
	rescueOK(t, os.WriteFile(state, []byte(`{"dns":["192.0.2.53"],"http":{"enabled":true,"server":"127.0.0.1","port":9999},"https":{"enabled":true,"server":"127.0.0.1","port":2334}}`), 0600))
	rescueOK(t, os.WriteFile(filepath.Join(dir, "dns-restore.json"), []byte(`{"service":"Test Wi-Fi","servers":[]}`), 0600))
	rescueOK(t, os.WriteFile(filepath.Join(dir, "proxy-restore.json"), []byte(`{"service":"Test Wi-Fi","http":{"enabled":false,"server":"","port":0},"https":{"enabled":false,"server":"","port":0},"managed":{"enabled":true,"server":"127.0.0.1","port":2334}}`), 0600))
	sys := System{EUID: 0, UID: os.Getuid(), Run: func(ctx context.Context, command string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestOfflineOSProcess$", "--", command}, args...)...)
		cmd.Env = append(os.Environ(), "SAKAMOTO_FAKE_OS=1", "SAKAMOTO_FAKE_STATE="+state)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}}
	if _, err := Exit(context.Background(), dir, sys); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(state)
	rescueOK(t, err)
	var after map[string]any
	rescueOK(t, json.Unmarshal(raw, &after))
	if after["dns"].([]any)[0] != "192.0.2.53" || after["http"].(map[string]any)["port"] != float64(9999) || after["http"].(map[string]any)["enabled"] != true || after["https"].(map[string]any)["enabled"] != false {
		t.Fatalf("external settings overwritten or owned proxy retained: %s", raw)
	}
}

func TestOfflineRescueRefusesLostDNSSnapshot(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "fake-os.json")
	rescueOK(t, os.WriteFile(state, []byte(`{"dns":["127.0.0.1"]}`), 0600))
	rescueOK(t, os.WriteFile(filepath.Join(dir, "sakamoto.yaml"), []byte("dns_guard:\n  enabled: true\n  service: Test Wi-Fi\n"), 0600))
	sys := System{EUID: 0, UID: os.Getuid(), Run: func(ctx context.Context, command string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestOfflineOSProcess$", "--", command}, args...)...)
		cmd.Env = append(os.Environ(), "SAKAMOTO_FAKE_OS=1", "SAKAMOTO_FAKE_STATE="+state)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}}
	outcome, err := Exit(context.Background(), dir, sys)
	if err == nil || outcome.JobsStopped || outcome.DNSRestored {
		t.Fatal("lost DNS snapshot reported a successful escape", outcome, err)
	}
}

func TestOfflineRescueRefusesAnotherLoadedRuntime(t *testing.T) {
	dir := t.TempDir()
	mutations := 0
	sys := System{EUID: 0, UID: os.Getuid(), Run: func(_ context.Context, command string, args ...string) (string, error) {
		if strings.HasSuffix(command, "launchctl") && args[0] == "print" {
			return "environment = {\n SAKAMOTO_DIR => /another/runtime\n}", nil
		}
		mutations++
		return "", errors.New("unexpected mutation")
	}}
	if _, err := Exit(context.Background(), dir, sys); err == nil {
		t.Fatal("another runtime was accepted")
	}
	if mutations != 0 {
		t.Fatal("scope rejection changed OS settings")
	}
}

func TestOfflineRescueKeepsCorruptEvidenceAndDoesNotStopJobs(t *testing.T) {
	dir := t.TempDir()
	rescueOK(t, os.WriteFile(filepath.Join(dir, "dns-restore.json"), []byte("corrupt snapshot"), 0600))
	mutations := 0
	sys := System{EUID: 0, UID: os.Getuid(), Run: func(_ context.Context, _ string, args ...string) (string, error) {
		if args[0] == "print" {
			return "Could not find service", errors.New("113")
		}
		mutations++
		return "", nil
	}}
	outcome, err := Exit(context.Background(), dir, sys)
	if err == nil || mutations != 0 || outcome.JobsStopped {
		t.Fatal("corrupt snapshot caused a stop or false success")
	}
	raw, readErr := os.ReadFile(filepath.Join(outcome.Evidence, "dns-restore.json"))
	rescueOK(t, readErr)
	if string(raw) != "corrupt snapshot" {
		t.Fatal("corrupt evidence lost")
	}
}
