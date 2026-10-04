package tui

import (
	"strings"
	"testing"

	"github.com/pi-dal/sakamoto/internal/core"
)

// TestStatusBadgeMatchesCorePhase pins the status-bar badge to the shared
// core phase derivation. The supervisor protocol only produces the values
// below; the label strings are geometry-relevant (mouse hit regions are
// measured from the rendered width) and must not drift.
func TestStatusBadgeMatchesCorePhase(t *testing.T) {
	cases := []struct {
		name          string
		serviceState  string
		networkState  string
		shadowrocket  bool
		wantLabel     string
		wantCorePhase core.SessionPhase
	}{
		{"fresh session", "", "", false, "● Disconnected", core.PhaseDisconnected},
		{"disconnected", "disconnected", "", false, "● Disconnected", core.PhaseDisconnected},
		{"connected, probe not run", "connected", "", false, "● TUN running", core.PhaseTUNRunning},
		{"connected, probe checking", "connected", "Checking", false, "● TUN running", core.PhaseTUNRunning},
		{"connected, probe passed", "connected", "Available · system routing", false, "● Network reachable", core.PhaseReachable},
		{"connected, probe failed once", "connected", "Unverified", false, "● TUN running · retrying probe", core.PhaseUnverified},
		{"supervisor unreachable", "unavailable", "", false, "● Supervisor unavailable", core.PhaseUnavailable},
		{"two TUNs conflict", "connected", "Available · system routing", true, "⚠ VPN conflict", core.PhaseConflict},
		{"foreign VPN while stopped", "disconnected", "", true, "● Disconnected", core.PhaseDisconnected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testModel(t)
			m.serviceState = tc.serviceState
			m.networkState = tc.networkState
			m.shadowrocket = tc.shadowrocket
			label, styled := m.statusBadge()
			if label != tc.wantLabel {
				t.Fatalf("label = %q, want %q", label, tc.wantLabel)
			}
			if styled == "" {
				t.Fatal("badge must be styled")
			}
			phase := core.PhaseOf(servicePhaseState(tc.serviceState), probePhaseState(tc.networkState), tc.shadowrocket)
			if phase != tc.wantCorePhase {
				t.Fatalf("phase = %s, want %s", phase, tc.wantCorePhase)
			}
		})
	}
}

// TestViewRendersCorePhaseInvariants checks the rendered Home header against
// the documented vocabulary: TUN running is never claimed as network
// reachable without a passed probe, and one failed probe never renders as an
// outage.
func TestViewRendersCorePhaseInvariants(t *testing.T) {
	m := testModel(t)
	m.serviceState = "connected"
	view := m.View()
	if !strings.Contains(view, "TUN running") {
		t.Fatal("running tunnel not shown as TUN running")
	}
	if strings.Contains(view, "Network reachable") {
		t.Fatal("network reachable claimed without a passed probe")
	}

	m.networkState = "Unverified"
	view = m.View()
	if !strings.Contains(view, "retrying probe") {
		t.Fatal("unverified probe must render as retrying")
	}
	if strings.Contains(view, "Network unavailable") {
		t.Fatal("a single failed probe must not render as an outage")
	}
}
