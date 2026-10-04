package core

import (
	"fmt"
	"time"
)

// ProbeState is the outcome of the small HTTPS network probe described in
// docs/tui.md. The probe checks independent endpoints through the system
// routing (TUN) and, when configured, through the local browser proxy entry.
//
// Vocabulary contract: a single probe failure is Unverified — a reason to
// retry with backoff, never a verdict that the network is down. Only a passed
// probe confirms the displayed path, and even then it says nothing about
// unrelated domains, excluded routes, DNS privacy, or the exit IP.
type ProbeState string

const (
	// ProbeIdle means no probe result exists (before the first check, and
	// after the tunnel stops). Adapters must reset to this on disconnect.
	ProbeIdle ProbeState = "Idle"
	// ProbeChecking means a probe is in flight.
	ProbeChecking ProbeState = "Checking"
	// ProbeReachable means the displayed path was verified. The path detail
	// (system routing and/or local browser proxy) travels alongside in
	// ProbeOutcome.Path.
	ProbeReachable ProbeState = "Reachable"
	// ProbeUnverified means the probe did not pass this round. The tunnel is
	// still running; the UI says "retrying probe", not "network down".
	ProbeUnverified ProbeState = "Unverified"
)

// Verified path descriptions. These strings are part of the product
// vocabulary (docs/tui.md reports them after "Network reachable"); front ends
// render them verbatim.
const (
	PathSystem     = "system routing"
	PathProxy      = "browser proxy"
	PathBoth       = "system routing and browser proxy"
	PathSystemOnly = "system routing (browser proxy unverified)"
	PathProxyOnly  = "browser proxy (system routing unverified)"
)

// Probe timing policy, extracted verbatim from the macOS TUI so every
// platform retries identically.
const (
	// ProbeRetryBase is the delay after the first failed probe.
	ProbeRetryBase = 10 * time.Second
	// ProbeRetryMax caps the exponential backoff; the probe never gives up.
	ProbeRetryMax = time.Minute
	// ProbeSuccessRefresh is how long a passed probe stays trusted before a
	// re-check is scheduled.
	ProbeSuccessRefresh = 2 * time.Minute
)

// ProbeOutcome is one complete probe round across the independent paths.
// State is Reachable when at least the displayed path works; Err carries the
// joined causes only when no path could be verified.
type ProbeOutcome struct {
	State ProbeState
	Path  string // verified path description; empty unless Reachable
	Err   error  // joined failure causes when Unverified; nil otherwise
}

// ClassifyProbe merges the independent path results. routeErr is the probe
// error for the system-routing path (nil = passed); proxyErr is the error for
// the local browser proxy path and is only considered when proxyEnabled.
//
// Classification (identical to the historical macOS behavior):
//
//   - route passed, proxy passed/enabled  → Reachable "system routing and browser proxy"
//   - route passed, proxy failed          → Reachable "system routing (browser proxy unverified)"
//   - route failed, proxy passed          → Reachable "browser proxy (system routing unverified)"
//   - route failed, proxy failed/disabled → Unverified with joined causes
//
// Note the asymmetry by design: the browser proxy alone still proves a working
// network path, so it counts as Reachable with a qualified path description.
func ClassifyProbe(routeErr, proxyErr error, proxyEnabled bool) ProbeOutcome {
	if routeErr == nil {
		switch {
		case proxyEnabled && proxyErr == nil:
			return ProbeOutcome{State: ProbeReachable, Path: PathBoth}
		case proxyEnabled:
			return ProbeOutcome{State: ProbeReachable, Path: PathSystemOnly}
		default:
			return ProbeOutcome{State: ProbeReachable, Path: PathSystem}
		}
	}
	if proxyEnabled && proxyErr == nil {
		return ProbeOutcome{State: ProbeReachable, Path: PathProxyOnly}
	}
	if proxyEnabled {
		// The error text is product vocabulary surfaced in user notices;
		// both causes stay wrapped for errors.Is checks.
		return ProbeOutcome{
			State: ProbeUnverified,
			Err:   fmt.Errorf("%s: %w; %s: %w", PathSystem, routeErr, PathProxy, proxyErr),
		}
	}
	return ProbeOutcome{State: ProbeUnverified, Err: fmt.Errorf("%s: %w", PathSystem, routeErr)}
}

// NextProbeRetry returns how long to wait before the next probe after the
// given number of consecutive failures. The sequence is 10s, 20s, 40s, then a
// 60s cap forever: probing continues indefinitely, because Unverified is a
// retry state, not a terminal one. failures <= 0 returns the base delay.
func NextProbeRetry(failures int) time.Duration {
	backoff := ProbeRetryBase
	for i := 1; i < failures && backoff < ProbeRetryMax; i++ {
		backoff *= 2
	}
	if backoff > ProbeRetryMax {
		backoff = ProbeRetryMax
	}
	return backoff
}
