package mobileexperiment

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/pi-dal/sakamoto/internal/experiment"
	"github.com/sagernet/sing-box/daemon"
)

// ExperimentTracker exposes the host's exact evidence rules to mobile clients.
// HTTP responses are not intercepted: only correlated explicit core signals count.
type ExperimentTracker struct {
	mu      sync.Mutex
	tracker *experiment.Tracker
}

func NewExperimentTracker(threshold int32) (*ExperimentTracker, error) {
	if threshold < 1 || threshold > 20 {
		return nil, fmt.Errorf("threshold must be 1–20")
	}
	return &ExperimentTracker{tracker: experiment.NewTracker(int(threshold))}, nil
}

func (t *ExperimentTracker) Connection(id, domain, destination, network, outbound, rule string, closed bool, downlink int64, nowMillis int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	kind := daemon.ConnectionEventType_CONNECTION_EVENT_NEW
	if closed {
		kind = daemon.ConnectionEventType_CONNECTION_EVENT_CLOSED
	}
	t.tracker.Connection(&daemon.ConnectionEvent{Id: id, Type: kind, Connection: &daemon.Connection{
		Domain: domain, Destination: destination, Network: network, Outbound: outbound, Rule: rule, DownlinkTotal: downlink,
	}}, time.UnixMilli(nowMillis))
}

func (t *ExperimentTracker) ObserveLog(message string, nowMillis int64, proxyHealthy, autoMode, cfRegion bool) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.UnixMilli(nowMillis)
	if autoMode {
		if domain := t.tracker.Failure(message, now, proxyHealthy); domain != "" {
			return domain
		}
	}
	if cfRegion {
		return t.tracker.CloudflareRegionBlock(message, now, proxyHealthy)
	}
	return ""
}

func ProxyOutbound(content string) (string, error) {
	return experiment.ProxyOutbound([]byte(content))
}

func RulesJSON(content, domainsJSON string) (string, error) {
	var domains []string
	if err := json.Unmarshal([]byte(domainsJSON), &domains); err != nil {
		return "", err
	}
	if len(domains) > 1000 {
		return "", fmt.Errorf("learned domain limit reached")
	}
	for _, domain := range domains {
		if experiment.Domain(domain) != domain {
			return "", fmt.Errorf("invalid learned domain")
		}
	}
	outbound, err := experiment.ProxyOutbound([]byte(content))
	if err != nil {
		return "", err
	}
	result, err := experiment.AddRule([]byte(content), domains, outbound)
	return string(result), err
}
