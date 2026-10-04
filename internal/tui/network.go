package tui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/core"
)

// These are independent, small HTTPS endpoints. A single service failure must
// not be reported as a machine-wide loss of connectivity.
var networkProbeURLs = []string{
	"https://www.gstatic.com/generate_204",
	"https://www.apple.com/library/test/success.html",
}

func probeHTTP(ctx context.Context, proxy *url.URL, endpoints []string) error {
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 4 * time.Second}
	if proxy != nil {
		transport.Proxy = http.ProxyURL(proxy)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport}
	var failures []error
	for _, endpoint := range endpoints {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		failures = append(failures, fmt.Errorf("%s: HTTP %d", req.URL.Host, resp.StatusCode))
	}
	return errors.Join(failures...)
}

// classifyNetworkProbe adapts the shared core classification to the TUI msg
// type. Path and error text are product vocabulary owned by core so every
// front end reports identical probe results.
func classifyNetworkProbe(routeErr, mixedErr error, mixedEnabled bool) networkMsg {
	outcome := core.ClassifyProbe(routeErr, mixedErr, mixedEnabled)
	return networkMsg{path: outcome.Path, err: outcome.Err}
}

func diagnoseNetwork(cfg *config.Config) networkMsg {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	mixedEnabled := cfg.MixedInbound.Enabled && cfg.SystemProxy.Enabled
	// Two paths are probed independently and concurrently: a direct socket
	// through macOS routing/TUN, and the local mixed inbound used by browsers.
	routeCh := make(chan error, 1)
	go func() { routeCh <- probeHTTP(ctx, nil, networkProbeURLs) }()
	var mixedCh chan error
	if mixedEnabled {
		mixedCh = make(chan error, 1)
		proxy := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", cfg.MixedInbound.Port)}
		go func() { mixedCh <- probeHTTP(ctx, proxy, networkProbeURLs) }()
	}
	routeErr := <-routeCh
	var mixedErr error
	if mixedEnabled {
		mixedErr = <-mixedCh
	}
	return classifyNetworkProbe(routeErr, mixedErr, mixedEnabled)
}
