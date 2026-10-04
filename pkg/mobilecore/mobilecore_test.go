package mobilecore

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/pi-dal/sakamoto/internal/core"
)

// TestSessionPhaseMatchesCore proves the bridge is a pure type conversion of
// core.PhaseOf: for every combination of the core vocabularies both layers
// must answer with the identical phase string.
func TestSessionPhaseMatchesCore(t *testing.T) {
	services := []core.ServiceState{
		core.ServiceStopped, core.ServiceStarting, core.ServiceRunning,
		core.ServiceStopping, core.ServiceUnavailable, "garbage",
	}
	probes := []core.ProbeState{
		core.ProbeIdle, core.ProbeChecking, core.ProbeReachable, core.ProbeUnverified, "garbage",
	}
	for _, service := range services {
		for _, probe := range probes {
			for _, conflict := range []bool{false, true} {
				want := string(core.PhaseOf(service, probe, conflict))
				got := SessionPhase(string(service), string(probe), conflict)
				if got != want {
					t.Fatalf("SessionPhase(%q, %q, %v) = %q, core says %q", service, probe, conflict, got, want)
				}
			}
		}
	}
}

// TestSessionPhaseVocabulary pins the docs/tui.md experience contract with
// literal strings, independent of the core constants.
func TestSessionPhaseVocabulary(t *testing.T) {
	cases := []struct {
		service, probe string
		conflict       bool
		want           string
	}{
		{"Stopped", "Idle", false, "Disconnected"},
		{"Starting", "Idle", false, "Starting"},
		{"Running", "Idle", false, "TUNRunning"},
		{"Running", "Checking", false, "TUNRunning"}, // TUN running ≠ network reachable
		{"Running", "Reachable", false, "Reachable"},
		{"Running", "Unverified", false, "Unverified"}, // one failed probe is not down
		{"Running", "Reachable", true, "Conflict"},
		{"Unavailable", "Reachable", false, "Unavailable"},
		{"Stopping", "Idle", false, "Stopping"},
		{"Stopped", "Reachable", false, "Disconnected"}, // stale probe never survives a stop
	}
	for _, tc := range cases {
		if got := SessionPhase(tc.service, tc.probe, tc.conflict); got != tc.want {
			t.Fatalf("SessionPhase(%q, %q, %v) = %q, want %q", tc.service, tc.probe, tc.conflict, got, tc.want)
		}
	}
}

func TestSessionPhaseNormalization(t *testing.T) {
	cases := []struct{ in, probe string }{
		{"running", "reachable"},
		{"RUNNING", "REACHABLE"},
		{" Running ", "unverified"},
	}
	for _, tc := range cases {
		want := SessionPhase("Running", tc.probe, false)
		if got := SessionPhase(tc.in, tc.probe, false); got != want {
			t.Fatalf("case-insensitive normalization failed: %q/%q = %q, want %q", tc.in, tc.probe, got, want)
		}
	}
	// Unknown inputs must degrade to renderable defaults, not errors.
	if got := SessionPhase("mystery", "mystery", false); got != "Disconnected" {
		t.Fatalf("unknown inputs = %q, want Disconnected", got)
	}
}

// TestNextRoutingModeMatchesCore proves the bridge cycle equals the core
// cycle for every input, including lowercase and garbage values.
func TestNextRoutingModeMatchesCore(t *testing.T) {
	inputs := []string{"Rule", "rule", "GLOBAL", "global", "Direct", "direct", "", "Manual", " garbage "}
	for _, in := range inputs {
		mode, err := core.ParseRoutingMode(in)
		if err != nil {
			mode = ""
		}
		want := string(core.NextRoutingMode(mode))
		if got := NextRoutingMode(in); got != want {
			t.Fatalf("NextRoutingMode(%q) = %q, core says %q", in, got, want)
		}
	}
}

func TestNextRoutingModeVocabulary(t *testing.T) {
	cycle := map[string]string{"Rule": "Global", "Global": "Direct", "Direct": "Rule", "": "Rule"}
	for current, want := range cycle {
		if got := NextRoutingMode(current); got != want {
			t.Fatalf("NextRoutingMode(%q) = %q, want %q", current, got, want)
		}
	}
}

// TestClassifyProbeMatchesCore proves the bool-based bridge classification is
// identical to core.ClassifyProbe with the corresponding error/nil causes.
func TestClassifyProbeMatchesCore(t *testing.T) {
	cause := errors.New("probe failed")
	for _, routePassed := range []bool{false, true} {
		for _, proxyPassed := range []bool{false, true} {
			for _, proxyEnabled := range []bool{false, true} {
				routeErr, proxyErr := (*error)(nil), (*error)(nil)
				if !routePassed {
					routeErr = &cause
				}
				if !proxyPassed {
					proxyErr = &cause
				}
				var wantRoute, wantProxy error
				if routeErr != nil {
					wantRoute = *routeErr
				}
				if proxyErr != nil {
					wantProxy = *proxyErr
				}
				want := core.ClassifyProbe(wantRoute, wantProxy, proxyEnabled)
				got := ClassifyProbe(routePassed, proxyPassed, proxyEnabled)
				if got.State != string(want.State) || got.Path != want.Path {
					t.Fatalf("ClassifyProbe(%v, %v, %v) = %+v, core says state=%s path=%q",
						routePassed, proxyPassed, proxyEnabled, got, want.State, want.Path)
				}
			}
		}
	}
}

// TestClassifyProbeVocabulary pins the path wording and the Unverified
// contract: a single failed path is never rendered as an outage.
func TestClassifyProbeVocabulary(t *testing.T) {
	cases := []struct {
		name                     string
		routePassed, proxyPassed bool
		proxyEnabled             bool
		wantState, wantPath      string
	}{
		{"both pass", true, true, true, "Reachable", "system routing and browser proxy"},
		{"proxy fails", true, false, true, "Reachable", "system routing (browser proxy unverified)"},
		{"route fails, proxy saves", false, true, true, "Reachable", "browser proxy (system routing unverified)"},
		{"all fail", false, false, true, "Unverified", ""},
		{"proxy off, route passes", true, false, false, "Reachable", "system routing"},
		{"proxy off, route fails", false, false, false, "Unverified", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyProbe(tc.routePassed, tc.proxyPassed, tc.proxyEnabled)
			if got.State != tc.wantState || got.Path != tc.wantPath {
				t.Fatalf("= %+v, want state=%s path=%q", got, tc.wantState, tc.wantPath)
			}
		})
	}
	if got := ClassifyProbe(false, false, false); !strings.Contains(got.Error, "system routing: probe failed") {
		t.Fatalf("unverified outcome lost the product-worded cause: %q", got.Error)
	}
}

// TestClassifyProbeDetailKeepsCauses proves client-supplied causes survive in
// the product wording, matching the macOS TUI notices byte for byte.
func TestClassifyProbeDetailKeepsCauses(t *testing.T) {
	got := ClassifyProbeDetail("dial timeout", "proxy refused", true)
	if got.State != "Unverified" {
		t.Fatalf("state = %s, want Unverified", got.State)
	}
	want := "system routing: dial timeout; browser proxy: proxy refused"
	if got.Error != want {
		t.Fatalf("error = %q, want %q", got.Error, want)
	}
	if single := ClassifyProbeDetail("dial timeout", "", true); single.State != "Reachable" || single.Path != "browser proxy (system routing unverified)" {
		t.Fatalf("failing route with passing proxy = %+v", single)
	}
	if other := ClassifyProbeDetail("", "proxy refused", true); other.State != "Reachable" || other.Path != "system routing (browser proxy unverified)" {
		t.Fatalf("passing route with failing proxy = %+v", other)
	}
}

// TestNodeStatusMatchesCore proves the bridge status derivation equals
// core.StatusFromLatency everywhere.
func TestNodeStatusMatchesCore(t *testing.T) {
	delays := []int32{-2, -1, 0, 1, 250}
	for _, delay := range delays {
		for _, testing := range []bool{false, true} {
			want := string(core.StatusFromLatency(delay, testing))
			if got := NodeStatus(delay, testing); got != want {
				t.Fatalf("NodeStatus(%d, %v) = %q, core says %q", delay, testing, got, want)
			}
		}
	}
}

func TestNodeStatusVocabulary(t *testing.T) {
	if got := NodeStatus(0, false); got != "Untested" {
		t.Fatalf("no result = %q, want Untested", got)
	}
	if got := NodeStatus(0, true); got != "Testing" {
		t.Fatalf("in flight = %q, want Testing", got)
	}
	if got := NodeStatus(80, false); got != "Reachable" {
		t.Fatalf("measured = %q, want Reachable", got)
	}
	if got := NodeStatus(-1, false); got != "Failed" {
		t.Fatalf("failure marker = %q, want Failed", got)
	}
}

// TestConfigStateTransitionMatchesCore proves the event-driven bridge machine
// equals the method-driven core machine from every state.
func TestConfigStateTransitionMatchesCore(t *testing.T) {
	states := []core.ConfigState{core.ConfigClean, core.ConfigNeedsRegenerate, core.ConfigNeedsReconnect}
	events := []struct {
		name  string
		apply func(core.ConfigState) core.ConfigState
	}{
		{EventModified, func(s core.ConfigState) core.ConfigState { return s.Modified() }},
		{EventRegenerateSucceeded, func(s core.ConfigState) core.ConfigState { return s.Regenerated(true) }},
		{EventRegenerateFailed, func(s core.ConfigState) core.ConfigState { return s.Regenerated(false) }},
		{EventApplied, func(s core.ConfigState) core.ConfigState { return s.Applied() }},
	}
	for _, state := range states {
		for _, event := range events {
			want := string(event.apply(state))
			got, err := ConfigStateTransition(string(state), event.name)
			if err != nil {
				t.Fatalf("ConfigStateTransition(%q, %q): %v", state, event.name, err)
			}
			if got != want {
				t.Fatalf("ConfigStateTransition(%q, %q) = %q, core says %q", state, event.name, got, want)
			}
		}
	}
}

func TestConfigStateTransitionVocabulary(t *testing.T) {
	cases := []struct{ state, event, want string }{
		{"Clean", "modified", "NeedsRegenerate"},
		{"NeedsRegenerate", "regenerate_succeeded", "NeedsReconnect"},
		{"NeedsRegenerate", "regenerate_failed", "NeedsRegenerate"},
		{"NeedsReconnect", "applied", "Clean"},
		{"NeedsReconnect", "modified", "NeedsRegenerate"}, // re-edit invalidates the regeneration
		{"Clean", "applied", "Clean"},
	}
	for _, tc := range cases {
		got, err := ConfigStateTransition(tc.state, tc.event)
		if err != nil {
			t.Fatalf("ConfigStateTransition(%q, %q): %v", tc.state, tc.event, err)
		}
		if got != tc.want {
			t.Fatalf("ConfigStateTransition(%q, %q) = %q, want %q", tc.state, tc.event, got, tc.want)
		}
	}
}

// TestConfigStateTransitionRejectsUnknown pins strictness: a state machine
// misuse must be loud, unlike the rendering helpers.
func TestConfigStateTransitionRejectsUnknown(t *testing.T) {
	if _, err := ConfigStateTransition("Clean", "reboot"); err == nil {
		t.Fatal("unknown event accepted")
	}
	if _, err := ConfigStateTransition("Pristine", "modified"); err == nil {
		t.Fatal("unknown state accepted")
	}
	if _, err := ConfigStateTransition("", "modified"); err == nil {
		t.Fatal("empty state accepted")
	}
}

// TestBridgePackageHasNoPlatformDeps enforces the isolation contract: this
// package must depend only on internal/core and the standard library. Any
// Bubble Tea, sing-box, gRPC, or TUI import in the dependency graph fails the
// build of this test.
func TestBridgePackageHasNoPlatformDeps(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	deps := string(out)
	for _, forbidden := range []string{
		"charmbracelet",     // Bubble Tea / lipgloss TUI stack
		"sagernet",          // sing-box daemon types
		"google.golang.org", // gRPC / protobuf
		"internal/tui",      // macOS TUI model
		"internal/config",   // sidecar configuration I/O
		"internal/svc",      // macOS launchd supervisor client
		"internal/sbclient", // gRPC client
	} {
		if strings.Contains(deps, forbidden) {
			t.Errorf("dependency graph contains forbidden package %q:\n%s", forbidden, deps)
		}
	}
}
