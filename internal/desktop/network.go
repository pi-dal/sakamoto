package desktop

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

var defaultProbeURLs = []string{
	"https://www.gstatic.com/generate_204",
	"https://www.apple.com/library/test/success.html",
}

// ProbeNetwork checks independent endpoints without using the system HTTP proxy.
// A local browser-proxy probe is separate from the system-routing observation.
func ProbeNetwork(ctx context.Context, cfg *config.Config, endpoints []string) core.ProbeOutcome {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if len(endpoints) == 0 {
		endpoints = defaultProbeURLs
	}
	route := make(chan error, 1)
	go func() { route <- probeHTTP(ctx, nil, endpoints) }()
	proxyEnabled := cfg.MixedInbound.Enabled && cfg.SystemProxy.Enabled
	var proxyErr error
	if proxyEnabled {
		proxy := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", cfg.MixedInbound.Port)}
		proxyErr = probeHTTP(ctx, proxy, endpoints)
	}
	return core.ClassifyProbe(<-route, proxyErr, proxyEnabled)
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
		failures = append(failures, fmt.Errorf("probe returned HTTP %d", resp.StatusCode))
	}
	return errors.Join(failures...)
}
