package tui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
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

func classifyNetworkProbe(routeErr, mixedErr error, mixedEnabled bool) networkMsg {
	if routeErr == nil {
		if mixedEnabled {
			if mixedErr == nil {
				return networkMsg{path: "系统路由与浏览器入口"}
			}
			return networkMsg{path: "系统路由（浏览器入口待确认）"}
		}
		return networkMsg{path: "系统路由"}
	}
	if mixedEnabled && mixedErr == nil {
		return networkMsg{path: "浏览器入口（系统路由待确认）"}
	}
	if mixedEnabled {
		return networkMsg{err: fmt.Errorf("系统路由: %v；浏览器入口: %v", routeErr, mixedErr)}
	}
	return networkMsg{err: fmt.Errorf("系统路由: %w", routeErr)}
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
