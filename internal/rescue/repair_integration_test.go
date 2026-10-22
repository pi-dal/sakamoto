package rescue

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepairLaunchctlProcess(t *testing.T) {
	if os.Getenv("SAKAMOTO_REPAIR_LAUNCHCTL") != "1" {
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
	statePath := os.Getenv("SAKAMOTO_REPAIR_LAUNCH_STATE")
	raw, _ := os.ReadFile(statePath)
	var state map[string]any
	rescueOK(t, json.Unmarshal(raw, &state))
	argv := args[index:]
	if argv[0] == "print" {
		if strings.Contains(argv[1], "dev.pi-dal") {
			rescueWrite(t, os.Stderr, "Could not find service\n")
			os.Exit(113)
		}
		rescueWrite(t, os.Stdout, "state = exited\nSAKAMOTO_DIR => "+state["runtime"].(string)+"\n")
		os.Exit(0)
	}
	if argv[0] != "kickstart" || len(argv) != 2 || argv[1] != "system/dev.sakamoto.daemon" {
		os.Exit(3)
	}
	state["starts"] = state["starts"].(float64) + 1
	raw, _ = json.Marshal(state)
	rescueOK(t, os.WriteFile(statePath, raw, 0600))
	os.Exit(0)
}
func TestRepairSupervisorKickstartIsScopedAndNeverKills(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "launchctl.json")
	write := func(runtime string) {
		raw, _ := json.Marshal(map[string]any{"runtime": runtime, "starts": 0})
		rescueOK(t, os.WriteFile(state, raw, 0600))
	}
	sys := System{UID: os.Getuid(), EUID: 0, Run: func(ctx context.Context, _ string, args ...string) (string, error) {
		argv := append([]string{"-test.run=^TestRepairLaunchctlProcess$", "--"}, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], argv...)
		cmd.Env = append(os.Environ(), "SAKAMOTO_REPAIR_LAUNCHCTL=1", "SAKAMOTO_REPAIR_LAUNCH_STATE="+state)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}}
	write(dir)
	rescueOK(t, StartSupervisor(context.Background(), dir, sys))
	raw, _ := os.ReadFile(state)
	var after map[string]any
	rescueOK(t, json.Unmarshal(raw, &after))
	if after["starts"].(float64) != 1 {
		t.Fatal("supervisor not started", string(raw))
	}
	write(filepath.Join(dir, "other-runtime"))
	if err := StartSupervisor(context.Background(), dir, sys); err == nil {
		t.Fatal("started a foreign runtime")
	}
	raw, _ = os.ReadFile(state)
	rescueOK(t, json.Unmarshal(raw, &after))
	if after["starts"].(float64) != 0 {
		t.Fatal("wrong-runtime check touched launchd")
	}
	if out := os.Getenv("SAKAMOTO_RECOVERY_ARTIFACT_DIR"); out != "" {
		rescueOK(t, os.MkdirAll(out, 0700))
		rescueOK(t, os.WriteFile(filepath.Join(out, "repair-supervisor.json"), []byte(`{"scenario":"quick-repair-supervisor","scoped_kickstart":true,"never_kills_running_job":true,"wrong_runtime_refused":true,"os_boundary":"isolated subprocess fixture"}`), 0600))
	}
}
