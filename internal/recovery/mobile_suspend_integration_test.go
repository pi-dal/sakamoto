package recovery

import (
	"path/filepath"
	"testing"
	"time"
)

// Exercise persistent journals across replacement owners, as when iOS stops
// a provider for network loss, sleep or an app update and launches it again.
func TestMobileRecoverySystemSuspensionPreservesCrashEpisode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "journal")
	clock := time.Now()
	open := func() *Mobile {
		t.Helper()
		m, err := NewMobile(dir)
		if err != nil {
			t.Fatal(err)
		}
		m.now = func() time.Time { return clock }
		return m
	}
	m := open()
	ticket, err := m.Begin(true)
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 12; round++ {
		if err = m.Ready(ticket, "core", "fixture"); err != nil {
			t.Fatal(err)
		}
		if err = m.Suspend(ticket); err != nil {
			t.Fatal(err)
		}
		state, err := open().Status()
		if err != nil {
			t.Fatal(err)
		}
		if state.Phase != "stopped" || state.Held || state.Blocked || state.Attempts != 0 {
			t.Fatalf("system stop consumed recovery budget: %+v", state)
		}
		if err = m.Fail(ticket); err == nil {
			t.Fatal("late failure changed a suspended episode")
		}
		if err = m.Ready(ticket, "late", "fixture"); err == nil {
			t.Fatal("late startup undid system stop")
		}
		m = open()
		ticket, err = m.Begin(false)
		if err != nil {
			t.Fatal(err)
		}
	}
	// A real startup failure still incurs backoff and consumes one attempt.
	if err = m.Fail(ticket); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Begin(false); err == nil {
		t.Fatal("real failure bypassed backoff")
	}
	state, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	clock = state.RetryAt.Add(time.Millisecond)
	ticket, err = m.Begin(false)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Ready(ticket, "after-failure", "fixture"); err != nil {
		t.Fatal(err)
	}
	if err = m.Suspend(ticket); err != nil {
		t.Fatal(err)
	}
	state, err = open().Status()
	if err != nil {
		t.Fatal(err)
	}
	if state.Attempts != 1 {
		t.Fatal("system suspension reset a real failure", state)
	}
	m = open()
	ticket, err = m.Begin(false)
	if err != nil {
		t.Fatal(err)
	}
	// Neither failed-fallback blocking nor explicit disconnect can be cleared.
	if err = m.Fail(ticket); err != nil {
		t.Fatal(err)
	}
	fallback, err := m.BeginFallback()
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Fail(fallback); err != nil {
		t.Fatal(err)
	}
	if err = m.Suspend(fallback); err == nil {
		t.Fatal("failed core was classified as a clean system suspension")
	}
	if _, err = open().Begin(false); err == nil {
		t.Fatal("blocked episode was resumed")
	}
	if err = m.Stop(); err != nil {
		t.Fatal(err)
	}
	if err = m.Suspend(ticket); err == nil {
		t.Fatal("system suspension undid manual disconnect")
	}
	if _, err = open().Begin(false); err == nil {
		t.Fatal("manual hold was cleared")
	}
	mobileArtifact(t, "system-suspension", map[string]any{"system_stops": 12, "real_failure_preserved": true, "stale_callbacks_rejected": true, "blocked_and_manual_hold_preserved": true})
}
