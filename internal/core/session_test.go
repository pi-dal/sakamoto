package core

import "testing"

// TestPhaseOfTransitionMatrix pins the full status-bar state machine. The
// enumerated phases and their precedence must match docs/tui.md: the bar
// distinguishes "TUN started" from "network probe passed", a single probe
// failure stays Unverified, and the supervisor being unreachable dominates.
func TestPhaseOfTransitionMatrix(t *testing.T) {
	probes := []ProbeState{ProbeIdle, ProbeChecking, ProbeReachable, ProbeUnverified}
	for _, service := range []ServiceState{
		ServiceStopped, ServiceStarting, ServiceRunning, ServiceStopping, ServiceUnavailable,
	} {
		for _, probe := range probes {
			for _, conflict := range []bool{false, true} {
				got := PhaseOf(service, probe, conflict)
				var want SessionPhase
				switch {
				case service == ServiceUnavailable:
					want = PhaseUnavailable
				case service == ServiceRunning && conflict:
					want = PhaseConflict
				case service == ServiceRunning && probe == ProbeReachable:
					want = PhaseReachable
				case service == ServiceRunning && probe == ProbeUnverified:
					want = PhaseUnverified
				case service == ServiceRunning:
					want = PhaseTUNRunning
				case service == ServiceStarting:
					want = PhaseStarting
				case service == ServiceStopping:
					want = PhaseStopping
				default:
					want = PhaseDisconnected
				}
				if got != want {
					t.Fatalf("PhaseOf(%s, %s, conflict=%v) = %s, want %s", service, probe, conflict, got, want)
				}
			}
		}
	}
}

func TestPhaseOfBoundaries(t *testing.T) {
	t.Run("TUN running is not network reachable", func(t *testing.T) {
		// A running tunnel with no probe result — or a probe still in
		// flight — must show TUNRunning, never Reachable.
		for _, probe := range []ProbeState{ProbeIdle, ProbeChecking} {
			if got := PhaseOf(ServiceRunning, probe, false); got != PhaseTUNRunning {
				t.Fatalf("running with probe %s = %s, want TUNRunning", probe, got)
			}
		}
	})
	t.Run("stale probe cannot survive a disconnect", func(t *testing.T) {
		// Reachable requires a running TUN AND a passed probe, so a leftover
		// ProbeReachable collapses to Disconnected once the tunnel stops.
		if got := PhaseOf(ServiceStopped, ProbeReachable, false); got != PhaseDisconnected {
			t.Fatalf("stopped with stale reachable probe = %s, want Disconnected", got)
		}
		if got := PhaseOf(ServiceStarting, ProbeReachable, false); got != PhaseStarting {
			t.Fatalf("starting with stale reachable probe = %s, want Starting", got)
		}
	})
	t.Run("single probe failure is Unverified, not down", func(t *testing.T) {
		got := PhaseOf(ServiceRunning, ProbeUnverified, false)
		if got != PhaseUnverified {
			t.Fatalf("one failed probe = %s, want Unverified", got)
		}
		if got == PhaseDisconnected || got == PhaseUnavailable {
			t.Fatal("Unverified must not be rendered as a disconnected or unavailable state")
		}
	})
	t.Run("conflict is only reported while running", func(t *testing.T) {
		if got := PhaseOf(ServiceStopped, ProbeIdle, true); got != PhaseDisconnected {
			t.Fatalf("foreign VPN with our tunnel down = %s, want Disconnected", got)
		}
		if got := PhaseOf(ServiceRunning, ProbeReachable, true); got != PhaseConflict {
			t.Fatal("a conflicting foreign VPN must override Reachable while we run")
		}
	})
	t.Run("unavailable dominates everything", func(t *testing.T) {
		if got := PhaseOf(ServiceUnavailable, ProbeReachable, true); got != PhaseUnavailable {
			t.Fatalf("unavailable controller = %s, want Unavailable", got)
		}
	})
	t.Run("every enumerated phase is reachable exactly as specified", func(t *testing.T) {
		cases := []struct {
			service  ServiceState
			probe    ProbeState
			conflict bool
			want     SessionPhase
		}{
			{ServiceStopped, ProbeIdle, false, PhaseDisconnected},
			{ServiceStarting, ProbeIdle, false, PhaseStarting},
			{ServiceRunning, ProbeIdle, false, PhaseTUNRunning},
			{ServiceRunning, ProbeReachable, false, PhaseReachable},
			{ServiceRunning, ProbeUnverified, false, PhaseUnverified},
			{ServiceRunning, ProbeIdle, true, PhaseConflict},
			{ServiceUnavailable, ProbeIdle, false, PhaseUnavailable},
			{ServiceStopping, ProbeIdle, false, PhaseStopping},
		}
		for _, tc := range cases {
			if got := PhaseOf(tc.service, tc.probe, tc.conflict); got != tc.want {
				t.Fatalf("PhaseOf(%s, %s, %v) = %s, want %s", tc.service, tc.probe, tc.conflict, got, tc.want)
			}
		}
	})
}

func TestServiceStateRunning(t *testing.T) {
	for _, tc := range []struct {
		state ServiceState
		want  bool
	}{
		{ServiceRunning, true},
		{ServiceStarting, false}, // optimistic guess, not a fact
		{ServiceStopping, false}, // pessimistic guess, still not down
		{ServiceStopped, false},
		{ServiceUnavailable, false},
	} {
		if got := tc.state.Running(); got != tc.want {
			t.Fatalf("%s.Running() = %v, want %v", tc.state, got, tc.want)
		}
	}
}
