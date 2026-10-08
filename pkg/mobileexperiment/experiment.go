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

// OwnedRulesJSON only replaces a rule whose exact three-field shape and
// domain set match the caller's committed operational state. A user-created
// proxy-domain rule is never removed by the host's insertion helper.
func OwnedRulesJSON(content, previousJSON, domainsJSON string) (string, error) {
	var previous, domains []string
	if err := json.Unmarshal([]byte(previousJSON), &previous); err != nil {
		return "", err
	}
	if err := json.Unmarshal([]byte(domainsJSON), &domains); err != nil {
		return "", err
	}
	proxy, err := experiment.ProxyOutbound([]byte(content))
	if err != nil {
		return "", err
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return "", err
	}
	var route map[string]json.RawMessage
	if err := json.Unmarshal(root["route"], &route); err != nil {
		return "", err
	}
	var rules []map[string]json.RawMessage
	if err := json.Unmarshal(route["rules"], &rules); err != nil {
		return "", err
	}
	set := func(values []string) map[string]bool {
		result := map[string]bool{}
		for _, v := range values {
			result[v] = true
		}
		return result
	}
	old := set(previous)
	var next []map[string]json.RawMessage
	for _, rule := range rules {
		var action, outbound string
		_ = json.Unmarshal(rule["action"], &action)
		_ = json.Unmarshal(rule["outbound"], &outbound)
		if rule["domain"] != nil && action == "route" && outbound == proxy {
			var names []string
			_ = json.Unmarshal(rule["domain"], &names)
			owned := len(rule) == 3 && len(old) > 0 && len(set(names)) == len(old)
			for _, name := range names {
				if !old[name] {
					owned = false
				}
			}
			if !owned {
				return "", fmt.Errorf("user proxy domain rule overlaps automatic learning")
			}
			continue
		}
		next = append(next, rule)
	}
	route["rules"], err = json.Marshal(next)
	if err != nil {
		return "", err
	}
	root["route"], err = json.Marshal(route)
	if err != nil {
		return "", err
	}
	base, err := json.Marshal(root)
	if err != nil {
		return "", err
	}
	return RulesJSON(string(base), domainsJSON)
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
