package core

import (
	"errors"
	"testing"
	"time"
)

// TestClassifyProbeMatrix pins the path classification. The critical
// invariant: a single failed path is never reported as a total outage, and a
// passing browser proxy alone still proves a usable network path.
func TestClassifyProbeMatrix(t *testing.T) {
	routeErr := errors.New("synthetic routing failure")
	proxyErr := errors.New("synthetic browser proxy failure")
	cases := []struct {
		name         string
		route, proxy error
		proxyEnabled bool
		wantState    ProbeState
		wantPath     string
	}{
		{"both paths pass", nil, nil, true, ProbeReachable, PathBoth},
		{"route passes, proxy fails", nil, proxyErr, true, ProbeReachable, PathSystemOnly},
		{"route fails, proxy passes", routeErr, nil, true, ProbeReachable, PathProxyOnly},
		{"both fail", routeErr, proxyErr, true, ProbeUnverified, ""},
		{"proxy disabled, route passes", nil, nil, false, ProbeReachable, PathSystem},
		{"proxy disabled, route fails", routeErr, nil, false, ProbeUnverified, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyProbe(tc.route, tc.proxy, tc.proxyEnabled)
			if got.State != tc.wantState {
				t.Fatalf("state = %s, want %s", got.State, tc.wantState)
			}
			if got.Path != tc.wantPath {
				t.Fatalf("path = %q, want %q", got.Path, tc.wantPath)
			}
		})
	}
}

func TestClassifyProbeSingleFailureIsUnverifiedNotDown(t *testing.T) {
	// docs/tui.md: "A single probe failure is Unverified, not a verdict that
	// the network is down." One failing endpoint out of the independent set
	// must land in Unverified with the cause attached, never in a "down"
	// state (which does not even exist in this model).
	cause := errors.New("one endpoint timed out")
	got := ClassifyProbe(cause, nil, false)
	if got.State != ProbeUnverified {
		t.Fatalf("state = %s, want Unverified", got.State)
	}
	if !errors.Is(got.Err, cause) {
		t.Fatalf("lost the failure cause: %v", got.Err)
	}
	if got.Path != "" {
		t.Fatalf("unverified outcome must not claim a path, got %q", got.Path)
	}
}

func TestClassifyProbeRetainsBothCauses(t *testing.T) {
	routeErr := errors.New("routing failed")
	proxyErr := errors.New("proxy failed")
	got := ClassifyProbe(routeErr, proxyErr, true)
	if !errors.Is(got.Err, routeErr) || !errors.Is(got.Err, proxyErr) {
		t.Fatalf("both independent causes must survive: %v", got.Err)
	}
	want := "system routing: routing failed; browser proxy: proxy failed"
	if got.Err.Error() != want {
		t.Fatalf("message = %q, want %q", got.Err.Error(), want)
	}
}

// TestNextProbeRetry pins the exact backoff sequence from the macOS TUI:
// 10s, 20s, 40s, then a permanent 60s cap. The probe never stops retrying.
func TestNextProbeRetry(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, 10 * time.Second},
		{1, 10 * time.Second},
		{2, 20 * time.Second},
		{3, 40 * time.Second},
		{4, time.Minute},
		{10, time.Minute},
		{-1, 10 * time.Second},
	}
	for _, tc := range cases {
		if got := NextProbeRetry(tc.failures); got != tc.want {
			t.Fatalf("NextProbeRetry(%d) = %s, want %s", tc.failures, got, tc.want)
		}
	}
}
