package tui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestProbeHTTPUsesAlternativeEndpointAndLocalProxy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := probeHTTP(ctx, nil, []string{server.URL + "/fail", server.URL + "/ok"}); err != nil {
		t.Fatal(err)
	}
	if err := probeHTTP(ctx, nil, []string{server.URL + "/fail"}); err == nil {
		t.Fatal("unavailable endpoint treated as success")
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "test.invalid" {
			t.Errorf("proxy did not receive origin: %q", r.URL.Host)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := probeHTTP(ctx, proxyURL, []string{"http://test.invalid/generate_204"}); err != nil {
		t.Fatal("mixed HTTP proxy path failed", err)
	}
}
func TestClassifyNetworkProbeNeverCallsSingleFailureAnOutage(t *testing.T) {
	e := errors.New("transient failure")
	for _, tc := range []struct {
		route, mixed error
		enabled      bool
		path         string
		fails        bool
	}{
		{nil, nil, true, "system routing and browser proxy", false},
		{e, nil, true, "browser proxy (system routing unverified)", false},
		{nil, e, true, "system routing (browser proxy unverified)", false},
		{e, e, true, "", true},
		{nil, nil, false, "system routing", false},
		{e, nil, false, "", true},
	} {
		result := classifyNetworkProbe(tc.route, tc.mixed, tc.enabled)
		if result.path != tc.path || (result.err != nil) != tc.fails {
			t.Fatalf("probe classification: %+v", result)
		}
	}
}
func TestFailedProbeRetriesAndNeverPermanentlyMarksNetworkDown(t *testing.T) {
	m := testModel(t)
	m.serviceState = "connected"
	m.netChecking = true
	m.onNetwork(networkMsg{err: errors.New("one probe timed out")})
	if m.networkState != "Unverified" || m.networkProbeFailures != 1 || !m.nextNetworkProbe.After(time.Now()) {
		t.Fatal("failed probe should schedule retry")
	}
	if strings.Contains(m.View(), "Network unavailable") {
		t.Fatal("UI still declares network unusable after one probe failure")
	}
	if cmd := m.onService(serviceMsg{state: "connected"}); cmd != nil {
		t.Fatal("retried before backoff elapsed")
	}
	m.nextNetworkProbe = time.Now().Add(-time.Second)
	if cmd := m.onService(serviceMsg{state: "connected"}); cmd == nil || !m.netChecking {
		t.Fatal("failed probe not retried")
	}
	m.onNetwork(networkMsg{path: "browser proxy (system routing unverified)"})
	if !strings.HasPrefix(m.networkState, "Available") || m.networkProbeFailures != 0 || !m.nextNetworkProbe.After(time.Now()) {
		t.Fatal("successful retry did not recover status")
	}
	m.onService(serviceMsg{state: "disconnected"})
	m.onNetwork(networkMsg{err: errors.New("late failure")})
	if m.networkState != "" {
		t.Fatal("late result overwrote disconnected state")
	}
}
