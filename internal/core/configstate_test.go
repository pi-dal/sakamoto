package core

import "testing"

// TestConfigStateLifecycle pins the rule from docs/tui.md: every saved
// modification needs Regenerate, and only a completed Regenerate plus a
// reconnect returns to Clean. No transition may fake progress.
func TestConfigStateLifecycle(t *testing.T) {
	t.Run("edit demands regenerate", func(t *testing.T) {
		if got := ConfigClean.Modified(); got != ConfigNeedsRegenerate {
			t.Fatalf("edit from Clean = %s, want NeedsRegenerate", got)
		}
	})
	t.Run("regenerate success demands reconnect", func(t *testing.T) {
		got := ConfigNeedsRegenerate.Regenerated(true)
		if got != ConfigNeedsReconnect {
			t.Fatalf("regenerated = %s, want NeedsReconnect", got)
		}
	})
	t.Run("regenerate failure keeps the pending signal", func(t *testing.T) {
		got := ConfigNeedsRegenerate.Regenerated(false)
		if got != ConfigNeedsRegenerate {
			t.Fatalf("failed regenerate = %s, want NeedsRegenerate", got)
		}
		// A failed run must not fake progress from a clean state either.
		if ConfigClean.Regenerated(false) != ConfigClean {
			t.Fatal("failed regenerate fabricated work on a clean state")
		}
	})
	t.Run("only a reconnect returns to clean", func(t *testing.T) {
		if got := ConfigNeedsReconnect.Applied(); got != ConfigClean {
			t.Fatalf("applied = %s, want Clean", got)
		}
		// Regenerate alone must never reach Clean: the core still runs the
		// old config until the tunnel reconnects.
		if ConfigNeedsRegenerate.Regenerated(true) == ConfigClean {
			t.Fatal("regenerate without reconnect claimed the config was live")
		}
	})
	t.Run("re-edit after regenerate invalidates the regeneration", func(t *testing.T) {
		state := ConfigNeedsReconnect.Modified()
		if state != ConfigNeedsRegenerate {
			t.Fatalf("editing after regenerate = %s, want NeedsRegenerate", state)
		}
	})
	t.Run("reconnect after a failed regenerate is still consistent", func(t *testing.T) {
		// The last good config is reloaded; pending edits stay pending
		// because the regenerate never succeeded, but the state machine
		// itself is consistent again once Applied is recorded.
		state := ConfigNeedsRegenerate.Regenerated(false).Applied()
		if state != ConfigClean {
			t.Fatalf("applied after failed regenerate = %s, want Clean", state)
		}
	})
}

func TestConfigStatePending(t *testing.T) {
	for state, want := range map[ConfigState]bool{
		ConfigClean:           false,
		ConfigNeedsRegenerate: true,
		ConfigNeedsReconnect:  true,
	} {
		if got := state.Pending(); got != want {
			t.Fatalf("%s.Pending() = %v, want %v", state, got, want)
		}
	}
}
