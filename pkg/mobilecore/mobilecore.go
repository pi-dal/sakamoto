package mobilecore

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pi-dal/sakamoto/internal/core"
)

// ProbeOutcome is the result of one network probe round, as shown on the
// Home status bar. Error is empty unless State is "Unverified"; it carries
// the per-path causes in the product wording ("system routing: …; browser
// proxy: …").
type ProbeOutcome struct {
	State string // "Reachable" or "Unverified" (see core.ProbeState)
	Path  string // verified path description; empty unless Reachable
	Error string // failure causes; empty unless Unverified
}

// Config-state machine event names for ConfigStateTransition.
const (
	EventModified            = "modified"             // any saved configuration change
	EventRegenerateSucceeded = "regenerate_succeeded" // Config → Regenerate finished OK
	EventRegenerateFailed    = "regenerate_failed"    // Regenerate failed; pending signal stays
	EventApplied             = "applied"              // core reconnected and loaded the config
)

// SessionPhase folds the connection observations into the single status-bar
// phase (docs/tui.md): "Disconnected", "Starting", "TUNRunning", "Reachable",
// "Unverified", "Conflict", or "Unavailable" ("Stopping" when the client
// reports a stop in flight).
//
// serviceState uses the core.ServiceState vocabulary ("Stopped", "Starting",
// "Running", "Stopping", "Unavailable"); probeState uses the core.ProbeState
// vocabulary ("Idle", "Checking", "Reachable", "Unverified"). Inputs are
// case- and surrounding-space-insensitive; an unknown service state counts as
// "Stopped" and an unknown probe state as "Idle" so the label always
// renders. The precedence
// rules — TUN running is not network reachable, a stale probe cannot survive
// a disconnect, one failed probe stays Unverified, conflict and unavailable
// dominate — live in core.PhaseOf.
func SessionPhase(serviceState, probeState string, conflict bool) string {
	phase := core.PhaseOf(parseServiceState(serviceState), parseProbeState(probeState), conflict)
	return string(phase)
}

// NextRoutingMode returns the next mode in the Rule → Global → Direct cycle
// (docs/tui.md: the Mode button / `m` key). An unknown or empty current value
// starts the cycle at "Rule", matching the macOS TUI before the first mode
// report arrives.
func NextRoutingMode(current string) string {
	mode, err := core.ParseRoutingMode(current)
	if err != nil {
		mode = "" // NextRoutingMode treats an unknown mode as the cycle start.
	}
	return string(core.NextRoutingMode(mode))
}

// ClassifyProbe merges the two independent path results (docs/tui.md: system
// routing, and the local browser proxy when configured). A false flag means
// that path failed its probe. At least one passing path yields "Reachable"
// with the qualified path description; all failing yields "Unverified" —
// a retry state, never a verdict that the network is down.
//
// The placeholder cause text ("probe failed") is used when Unverified; pass
// real causes to ClassifyProbeDetail.
func ClassifyProbe(routePassed, proxyPassed, proxyEnabled bool) ProbeOutcome {
	var routeErr, proxyErr error
	if !routePassed {
		routeErr = errPlaceholder
	}
	if !proxyPassed {
		proxyErr = errPlaceholder
	}
	return toOutcome(core.ClassifyProbe(routeErr, proxyErr, proxyEnabled))
}

// ClassifyProbeDetail is ClassifyProbe with per-path cause texts. An empty
// error string means that path passed; a non-empty string is the failure
// cause, wrapped in the product wording and preserved for display.
func ClassifyProbeDetail(routeError, proxyError string, proxyEnabled bool) ProbeOutcome {
	var routeErr, proxyErr error
	if routeError != "" {
		routeErr = errors.New(routeError)
	}
	if proxyError != "" {
		proxyErr = errors.New(proxyError)
	}
	return toOutcome(core.ClassifyProbe(routeErr, proxyErr, proxyEnabled))
}

func toOutcome(outcome core.ProbeOutcome) ProbeOutcome {
	text := ""
	if outcome.Err != nil {
		text = outcome.Err.Error()
	}
	return ProbeOutcome{State: string(outcome.State), Path: outcome.Path, Error: text}
}

// NodeStatus derives the latency-test vocabulary from docs/tui.md and
// skills.md: "Untested", "Testing", "Reachable", or "Failed". Selection is a
// separate fact (the filled dot) and is intentionally not an input here: a
// selected dot is not a connectivity claim.
func NodeStatus(delayMS int32, testing bool) string {
	return string(core.StatusFromLatency(delayMS, testing))
}

// Bindable flattenings of the bool-based ClassifyProbe above (gomobile bind
// skips functions that return the ProbeOutcome struct, so the three rendered
// fields are exposed separately). All three answer one classification round.
func ProbeStateOf(routePassed, proxyPassed, proxyEnabled bool) string {
	return ClassifyProbe(routePassed, proxyPassed, proxyEnabled).State
}

func ProbePathOf(routePassed, proxyPassed, proxyEnabled bool) string {
	return ClassifyProbe(routePassed, proxyPassed, proxyEnabled).Path
}

func ProbeErrorOf(routePassed, proxyPassed, proxyEnabled bool) string {
	outcome := ClassifyProbe(routePassed, proxyPassed, proxyEnabled)
	return outcome.Error
}

// ConfigStateTransition advances the configuration-change state machine
// (docs/tui.md: regenerate after edits, reconnect after regenerating).
//
// state uses the core.ConfigState vocabulary ("Clean", "NeedsRegenerate",
// "NeedsReconnect"); event is one of the Event* constants of this package.
// Both are case- and surrounding-space-insensitive. Unknown states or events
// return an error: a state-machine misuse must be loud, unlike the rendering
// helpers.
func ConfigStateTransition(state, event string) (string, error) {
	current, err := parseConfigState(state)
	if err != nil {
		return "", err
	}
	event = strings.TrimSpace(event)
	switch {
	case strings.EqualFold(event, EventModified):
		current = current.Modified()
	case strings.EqualFold(event, EventRegenerateSucceeded):
		current = current.Regenerated(true)
	case strings.EqualFold(event, EventRegenerateFailed):
		current = current.Regenerated(false)
	case strings.EqualFold(event, EventApplied):
		current = current.Applied()
	default:
		return "", fmt.Errorf("unknown config event %q; use modified, regenerate_succeeded, regenerate_failed, or applied", event)
	}
	return string(current), nil
}

// errPlaceholder is the cause used by the bool-based ClassifyProbe, which
// carries no error detail by design.
var errPlaceholder = errors.New("probe failed")

func parseServiceState(s string) core.ServiceState {
	s = strings.TrimSpace(s)
	for _, v := range []core.ServiceState{
		core.ServiceStopped, core.ServiceStarting, core.ServiceRunning,
		core.ServiceStopping, core.ServiceUnavailable,
	} {
		if strings.EqualFold(s, string(v)) {
			return v
		}
	}
	return core.ServiceStopped // a status label must always render
}

func parseProbeState(s string) core.ProbeState {
	s = strings.TrimSpace(s)
	for _, v := range []core.ProbeState{
		core.ProbeIdle, core.ProbeChecking, core.ProbeReachable, core.ProbeUnverified,
	} {
		if strings.EqualFold(s, string(v)) {
			return v
		}
	}
	return core.ProbeIdle
}

func parseConfigState(s string) (core.ConfigState, error) {
	s = strings.TrimSpace(s)
	for _, v := range []core.ConfigState{
		core.ConfigClean, core.ConfigNeedsRegenerate, core.ConfigNeedsReconnect,
	} {
		if strings.EqualFold(s, string(v)) {
			return v, nil
		}
	}
	return "", fmt.Errorf("unknown config state %q; use Clean, NeedsRegenerate, or NeedsReconnect", s)
}
