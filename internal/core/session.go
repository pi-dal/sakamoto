package core

// ServiceState is the connection lifecycle of the tunnel engine as observed
// from the controller side. It deliberately says nothing about whether the
// network behind the tunnel works; that is ProbeState's job.
//
// Map to docs/tui.md: the Home status bar distinguishes "TUN started" (ServiceRunning)
// from "network probe passed" (PhaseReachable). Platform adapters feed this
// enum: the macOS TUI maps the supervisor report ("connected …" /
// "disconnected"), an iOS client maps NetworkExtension provider state.
type ServiceState string

const (
	// ServiceStopped means no TUN is up. This is also the state before the
	// first report arrives; the UI must not claim any connectivity.
	ServiceStopped ServiceState = "Stopped"
	// ServiceStarting is a requested-but-unconfirmed start. A UI may show a
	// pending indicator, but must not display Reachable from this state.
	ServiceStarting ServiceState = "Starting"
	// ServiceRunning means the TUN interface is up ("● TUN running"). This
	// alone is NOT network reachability; the probe decides that.
	ServiceRunning ServiceState = "Running"
	// ServiceStopping is a requested-but-unconfirmed stop. The UI keeps
	// treating the TUN as up until the stop is confirmed, matching the
	// macOS TUI which shows the running label until the supervisor replies.
	ServiceStopping ServiceState = "Stopping"
	// ServiceUnavailable means the controlling service (macOS supervisor
	// daemon, iOS tunnel provider handle) could not be reached at all. No
	// other state can be trusted while this holds.
	ServiceUnavailable ServiceState = "Unavailable"
)

// Running reports whether the TUN should be treated as up. Starting and
// Stopping are optimistic/pessimistic in-flight guesses, never facts.
func (s ServiceState) Running() bool { return s == ServiceRunning }

// SessionPhase is the single label a front end shows in its status bar. It is
// derived from ServiceState, ProbeState and the VPN-conflict flag so that
// every platform renders the same seven states with the same precedence.
//
// Precedence (highest first), mirroring the macOS status bar exactly:
//
//  1. Unavailable  — the controller cannot be reached; nothing else is known.
//  2. Conflict     — another VPN TUN is active while ours runs (docs/tui.md:
//     "Two TUNs conflict. Disconnect both VPNs, then reconnect sakamoto.").
//  3. Reachable    — running AND the probe passed.
//  4. Unverified   — running but the last probe failed once; retrying.
//  5. TUNRunning   — running, probe not conclusive (idle or checking).
//  6. Starting / Stopping — requested transitions, not yet confirmed.
//  7. Disconnected — everything else, including any stale probe result.
type SessionPhase string

const (
	PhaseDisconnected SessionPhase = "Disconnected"
	PhaseStarting     SessionPhase = "Starting"
	PhaseTUNRunning   SessionPhase = "TUNRunning"
	PhaseReachable    SessionPhase = "Reachable"
	PhaseUnverified   SessionPhase = "Unverified"
	PhaseConflict     SessionPhase = "Conflict"
	PhaseUnavailable  SessionPhase = "Unavailable"
	PhaseStopping     SessionPhase = "Stopping"
)

// PhaseOf folds the independent observations into one status-bar phase.
//
// Key boundaries (all covered by tests):
//
//   - Probe results are only meaningful while the TUN runs. A leftover
//     ProbeReachable after a disconnect collapses to PhaseDisconnected:
//     TUN running is required for network reachability, so a dead tunnel can
//     never inherit a previously passed probe.
//   - Conflict is only reported while running. A conflicting foreign VPN with
//     our tunnel down is still PhaseDisconnected (the conflict is surfaced as
//     a Notice by the adapters, not as the phase).
//   - PhaseUnverified is a running state, not a down state: one failed probe
//     must never be rendered as "network unavailable".
func PhaseOf(s ServiceState, probe ProbeState, conflict bool) SessionPhase {
	switch {
	case s == ServiceUnavailable:
		return PhaseUnavailable
	case s == ServiceRunning && conflict:
		return PhaseConflict
	case s == ServiceRunning && probe == ProbeReachable:
		return PhaseReachable
	case s == ServiceRunning && probe == ProbeUnverified:
		return PhaseUnverified
	case s == ServiceRunning:
		return PhaseTUNRunning
	case s == ServiceStarting:
		return PhaseStarting
	case s == ServiceStopping:
		return PhaseStopping
	default:
		return PhaseDisconnected
	}
}
