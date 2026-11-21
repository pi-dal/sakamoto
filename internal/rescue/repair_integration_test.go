package rescue

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestRepairSupervisorKickstartIsScopedAndNeverKills(t *testing.T) {
	dir := t.TempDir()
	owner := dir
	started := false
	const job = "system/dev.sakamoto.daemon"
	sys := System{UID: os.Getuid(), EUID: 0, Run: func(_ context.Context, command string, args ...string) (string, error) {
		if command == "/bin/launchctl" {
			switch {
			case slices.Equal(args, []string{"print", job}):
				return "state = exited\nSAKAMOTO_DIR => " + owner + "\n", nil
			case slices.Equal(args, []string{"print", "system/dev.pi-dal.sing-box"}):
				return "Could not find service", errors.New("missing job")
			case slices.Equal(args, []string{"kickstart", job}):
				started = true
				return "", nil
			}
		}
		t.Fatalf("unexpected system operation: %s %v", command, args)
		return "", nil
	}}
	rescueOK(t, StartSupervisor(context.Background(), dir, sys))
	if !started {
		t.Fatal("supervisor not started")
	}
	owner = filepath.Join(dir, "other-runtime")
	started = false
	if err := StartSupervisor(context.Background(), dir, sys); err == nil || started {
		t.Fatal("repair accepted or started a foreign supervisor")
	}
	if out := os.Getenv("SAKAMOTO_RECOVERY_ARTIFACT_DIR"); out != "" {
		rescueOK(t, os.MkdirAll(out, 0700))
		rescueOK(t, os.WriteFile(filepath.Join(out, "repair-supervisor.json"), []byte(`{"scenario":"quick-repair-supervisor","scoped_kickstart":true,"never_kills_running_job":true,"wrong_runtime_refused":true,"os_boundary":"isolated launchctl fixture"}`), 0600))
	}
}
