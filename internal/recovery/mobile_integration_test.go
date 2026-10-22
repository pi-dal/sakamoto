package recovery

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This is a process-boundary scenario: a real child persists Running and is
// killed without cleanup. Each replacement reads the same private journal.
func TestMobileRecoveryProcessHelper(t *testing.T) {
	dir := os.Getenv("SAKAMOTO_MOBILE_RECOVERY_CHILD")
	if dir == "" {
		return
	}
	journal, err := NewMobile(dir)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := journal.Begin(false)
	if err != nil {
		t.Fatal(err)
	}
	if err = journal.Ready(ticket, "child-instance", "fixture-core"); err != nil {
		t.Fatal(err)
	}
	_, _ = os.Stdout.WriteString("RECOVERY_READY\n")
	<-time.After(time.Hour)
}

func TestMobileRecoveryCrashBudgetAcrossProcessDeath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "journal")
	child := exec.Command(os.Args[0], "-test.run=^TestMobileRecoveryProcessHelper$")
	child.Env = append(os.Environ(), "SAKAMOTO_MOBILE_RECOVERY_CHILD="+dir)
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	line := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			if scanner.Text() == "RECOVERY_READY" {
				line <- scanner.Text()
				return
			}
		}
		line <- ""
	}()
	select {
	case got := <-line:
		if got == "" {
			t.Fatal("child did not start")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child startup timed out")
	}
	if err = child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	clock := time.Now()
	journal, err := NewMobile(dir)
	if err != nil {
		t.Fatal(err)
	}
	journal.now = func() time.Time { return clock }
	if _, err = journal.Begin(false); err == nil {
		t.Fatal("unclean predecessor must enter backoff")
	}
	for attempt, delay := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second} {
		state, err := journal.Status()
		if err != nil {
			t.Fatal(err)
		}
		if state.Attempts != attempt || state.Blocked {
			t.Fatalf("unexpected budget: %+v", state)
		}
		clock = state.RetryAt.Add(time.Millisecond)
		replacement, err := NewMobile(dir)
		if err != nil {
			t.Fatal(err)
		}
		replacement.now = func() time.Time { return clock }
		ticket, err := replacement.Begin(false)
		if err != nil {
			t.Fatal(err)
		}
		if err = replacement.Ready(ticket, "next-instance", "fixture-core"); err != nil {
			t.Fatal(err)
		}
		if err = replacement.Fail(ticket); err != nil {
			t.Fatal(err)
		}
		state, err = replacement.Status()
		if err != nil {
			t.Fatal(err)
		}
		if attempt < 2 && state.RetryAt.Sub(clock) != delay*2 {
			t.Fatalf("incorrect next backoff: %+v", state)
		}
		journal = replacement
	}
	state, err := journal.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Blocked || state.Attempts != MaxRetries {
		t.Fatalf("budget not blocked: %+v", state)
	}
	clock = clock.Add(24 * time.Hour)
	if _, err = journal.Begin(false); err == nil {
		t.Fatal("elapsed wall time reset the episode")
	}
	ticket, err := journal.Begin(true)
	if err != nil {
		t.Fatal(err)
	}
	if err = journal.Ready(ticket, "manual-instance", "fixture-core"); err != nil {
		t.Fatal(err)
	}
	if err = journal.Stable(ticket, "manual-instance"); err == nil {
		t.Fatal("early stable promotion")
	}
	clock = clock.Add(StableInterval)
	if err = journal.Stable(ticket, "wrong-instance"); err == nil {
		t.Fatal("accepted different core instance")
	}
	if err = journal.Stable(ticket, "manual-instance"); err != nil {
		t.Fatal(err)
	}
	state, err = journal.Status()
	if err != nil {
		t.Fatal(err)
	}
	if state.Attempts != 0 || !state.Stable {
		t.Fatalf("did not confirm stable instance: %+v", state)
	}
	mobileArtifact(t, "process-crash-budget", map[string]any{"three_retries": true, "backoff_seconds": []int{2, 4, 8}, "blocked_across_restart": true, "same_instance_stability": true})
}

func TestMobileRecoveryStopSupersedesLateWorkAndCorruption(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "journal")
	journal, err := NewMobile(dir)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := journal.Begin(true)
	if err != nil {
		t.Fatal(err)
	}
	if err = journal.Stop(); err != nil {
		t.Fatal(err)
	}
	if err = journal.Ready(ticket, "late", "fixture"); err == nil {
		t.Fatal("late start undid stop")
	}
	if err = journal.Fail(ticket); err == nil {
		t.Fatal("late failure undid stop")
	}
	if _, err = journal.Begin(false); err == nil {
		t.Fatal("automatic launch undid manual hold")
	}
	next, err := journal.Begin(true)
	if err != nil {
		t.Fatal(err)
	}
	if next == ticket {
		t.Fatal("ABA ticket reused")
	}
	if err = journal.Ready(ticket, "late", "fixture"); err == nil {
		t.Fatal("predecessor changed replacement")
	}
	if err = journal.Ready(next, "current", "fixture"); err != nil {
		t.Fatal(err)
	}
	state, err := journal.Status()
	if err != nil {
		t.Fatal(err)
	}
	if state.Instance != "current" {
		t.Fatal(state)
	}
	if err = os.WriteFile(filepath.Join(dir, MobileJournalName), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = journal.Begin(false); err == nil {
		t.Fatal("corruption allowed automatic start")
	}
	repaired, err := journal.Begin(true)
	if err != nil || repaired == "" {
		t.Fatal("explicit repair could not reset corrupt journal", err)
	}
	evidence, err := filepath.Glob(filepath.Join(dir, "mobile-rejected-*.json"))
	if err != nil || len(evidence) != 1 {
		t.Fatal("corrupt private journal was not preserved", err)
	}
	mobileArtifact(t, "stop-and-corruption", map[string]any{"late_start_rejected": true, "manual_hold": true, "corrupt_journal_fails_closed": true})
}

func mobileArtifact(t *testing.T, name string, evidence map[string]any) {
	t.Helper()
	base := os.Getenv("SAKAMOTO_RECOVERY_ARTIFACT_DIR")
	if base == "" {
		base = t.TempDir()
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	evidence["scenario"] = name
	raw, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(base, "mobile-"+name+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
