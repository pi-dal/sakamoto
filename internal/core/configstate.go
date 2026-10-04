package core

// ConfigState tracks whether the generated sing-box config on disk matches
// what the running core actually loaded, and what the user must do next.
//
// The lifecycle comes straight from docs/tui.md: settings edits, policy
// rules, node changes and imports all end with "Regenerate, then disconnect
// and reconnect before expecting the core to use the new configuration."
// No front end may imply that saving alone changed the running tunnel.
type ConfigState string

const (
	// ConfigClean means the generated config is what the core loaded; no
	// action is pending.
	ConfigClean ConfigState = "Clean"
	// ConfigNeedsRegenerate means saved changes exist that Regenerate has
	// not folded into the generated config yet.
	ConfigNeedsRegenerate ConfigState = "NeedsRegenerate"
	// ConfigNeedsReconnect means the generated config is newer than what
	// the core loaded; a disconnect + reconnect applies it.
	ConfigNeedsReconnect ConfigState = "NeedsReconnect"
)

// Modified records any saved configuration change (settings form, policy
// rule, node add/edit/delete, subscription change). It always demands a
// regenerate — even from NeedsReconnect, because a newer edit invalidates the
// just-regenerated output and the core must never pick up a half-applied
// mixture.
func (s ConfigState) Modified() ConfigState { return ConfigNeedsRegenerate }

// Regenerated records the outcome of Config → Regenerate. Success moves to
// NeedsReconnect; a failed run leaves the state untouched (a failed
// regeneration must not erase the pending-change signal, and must not fake
// progress on a clean state either).
func (s ConfigState) Regenerated(ok bool) ConfigState {
	if !ok {
		return s
	}
	return ConfigNeedsReconnect
}

// Applied records that the core reconnected and loaded the generated config.
// This is the only transition back to Clean. It is safe to call from any
// state: reconnecting after a failed regenerate legitimately loads the last
// good config, which is consistent again.
func (s ConfigState) Applied() ConfigState { return ConfigClean }

// Pending reports whether any user action is still required before the
// running core is guaranteed to match the saved configuration.
func (s ConfigState) Pending() bool { return s != ConfigClean }
