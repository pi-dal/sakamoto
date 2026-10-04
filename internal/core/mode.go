package core

import (
	"errors"
	"fmt"
	"strings"
)

// RoutingMode is a sing-box clash routing mode. The macOS TUI cycles
// Rule → Global → Direct with the `m` key / Mode button (docs/tui.md), and an
// iOS client must offer the same three values with the same semantics:
//
//   - Rule: ordinary split routing per the generated rule set.
//   - Global: ordinary public traffic goes through the selected chain, even
//     for domains otherwise marked DIRECT; private/Tailscale exclusions, ad
//     rejects and STUN/QUIC blocks stay active.
//   - Direct: ordinary public traffic goes directly; the same protections
//     stay active.
//
// Mode changes affect new connections only; existing streams are not closed.
type RoutingMode string

const (
	ModeRule   RoutingMode = "Rule"
	ModeGlobal RoutingMode = "Global"
	ModeDirect RoutingMode = "Direct"
)

// ErrRoutingModeUnavailable reports that the running core does not expose the
// requested mode. This is the "old generated config" case from docs/tui.md:
// the correct user guidance is regenerate + reconnect, never a silent no-op.
var ErrRoutingModeUnavailable = errors.New("routing mode is unavailable in the running core; regenerate the config and reconnect")

// ParseRoutingMode parses a mode name case-insensitively, accepting the raw
// lowercase clash mode strings ("rule", "global", "direct") as well as the
// display spellings. Unknown or empty values return an error rather than
// guessing, because switching modes is a user-visible action.
func ParseRoutingMode(s string) (RoutingMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "rule":
		return ModeRule, nil
	case "global":
		return ModeGlobal, nil
	case "direct":
		return ModeDirect, nil
	default:
		return "", fmt.Errorf("unknown routing mode %q", s)
	}
}

// NextRoutingMode returns the next mode in the Rule → Global → Direct cycle.
// An unknown or empty current value starts the cycle at Rule, matching the
// historical macOS behavior where a fresh session without a reported mode
// cycles into Rule first.
func NextRoutingMode(current RoutingMode) RoutingMode {
	switch current {
	case ModeRule:
		return ModeGlobal
	case ModeGlobal:
		return ModeDirect
	default:
		return ModeRule
	}
}

// ModeAvailable reports whether the requested mode is among the modes the
// running core exposes. Comparison is case-insensitive because the native API
// reports lowercase clash mode strings.
func ModeAvailable(requested RoutingMode, available []RoutingMode) bool {
	for _, m := range available {
		if strings.EqualFold(string(m), string(requested)) {
			return true
		}
	}
	return false
}

// RequestMode validates a mode switch against the available modes before it
// is sent to the core. A false result means the caller must surface
// ErrRoutingModeUnavailable (regenerate + reconnect) instead of pretending
// the switch happened; docs/tui.md explicitly forbids reporting a false
// success for an old generated config.
func RequestMode(requested RoutingMode, available []RoutingMode) error {
	if !ModeAvailable(requested, available) {
		return fmt.Errorf("%w: %s", ErrRoutingModeUnavailable, requested)
	}
	return nil
}
