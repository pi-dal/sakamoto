package mobileexperiment

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/pi-dal/sakamoto/internal/experiment"
	"github.com/sagernet/sing-box/daemon"
)

// LearningProber is implemented by the app using its captured native core ID.
// No app networking stack or listener is needed for verification.
type LearningProber interface {
	ProbeOrigin(domain string, port int32, outbound string, timeoutMillis int32) (int32, error)
}

type LearningCheck struct {
	result experiment.LearningResult
	ctx    context.Context
	cancel context.CancelFunc
}

func NewLearningCheck(candidateJSON string) (*LearningCheck, error) {
	var candidate experiment.Candidate
	if err := json.Unmarshal([]byte(candidateJSON), &candidate); err != nil {
		return nil, err
	}
	if experiment.Domain(candidate.Domain) != candidate.Domain || candidate.Failures < 1 || candidate.Required < 1 || candidate.Required > 20 {
		return nil, fmt.Errorf("invalid observed learning candidate")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &LearningCheck{result: experiment.LearningResult{Candidate: candidate, Time: time.Now(), State: "skipped"}, ctx: ctx, cancel: cancel}, nil
}

func (c *LearningCheck) Cancel() { c.cancel() }
func (c *LearningCheck) Ready() bool {
	return c.result.State == "candidate_ready" && c.ctx.Err() == nil
}
func (c *LearningCheck) Finish(state, detail string) {
	c.result.State = state
	c.result.Detail = detail
}
func (c *LearningCheck) ReportJSON() string { raw, _ := json.Marshal(c.result); return string(raw) }

func (c *LearningCheck) probe(ctx context.Context, prober LearningProber, outbound string) experiment.ProbeResult {
	deadline, _ := ctx.Deadline()
	millis := min(int64(8000), time.Until(deadline).Milliseconds())
	if ctx.Err() != nil || millis < 1 {
		return experiment.ProbeResult{Error: "verification canceled or timed out"}
	}
	port := int32(80)
	if c.result.Port == "443" {
		port = 443
	}
	status, err := prober.ProbeOrigin(c.result.Domain, port, outbound, int32(millis))
	if err != nil {
		return experiment.ProbeResult{Error: "origin connection, TLS or response failed"}
	}
	ok := (status >= 200 && status < 400) || status == 401 || status == 404 || status == 405
	result := experiment.ProbeResult{HTTPStatus: int(status), Reachable: ok}
	if !ok {
		result.Error = fmt.Sprintf("origin returned HTTP %d", status)
	}
	return result
}

// Compare actively confirms one observed failure, within a single deadline.
// Both exits must check the same origin, and the proxy must improve it.
func (c *LearningCheck) Compare(prober LearningProber, proxyTag string) {
	if c.result.Port != "80" && c.result.Port != "443" {
		c.Finish("skipped", "Destination is not an HTTP(S) origin on port 80/443")
		return
	}
	if proxyTag == "" || proxyTag == "direct" {
		c.Finish("skipped", "A distinct configured proxy exit is required")
		return
	}
	ctx, cancel := context.WithTimeout(c.ctx, 25*time.Second)
	defer cancel()
	var direct, proxy experiment.ProbeResult
	var workers sync.WaitGroup
	workers.Add(1)
	go func() { defer workers.Done(); direct = c.probe(ctx, prober, "direct") }()
	proxy = c.probe(ctx, prober, proxyTag)
	workers.Wait()
	c.result.Direct = direct
	c.result.Proxy = proxy
	if ctx.Err() != nil {
		c.Finish("skipped", "Verification canceled or timed out")
		return
	}
	if direct.Reachable {
		c.Finish("direct_recovered", "Direct origin check succeeded; direct routing retained")
		return
	}
	if !proxy.Reachable {
		c.Finish("not_improved", "Proxy origin check did not succeed; no route saved")
		return
	}
	c.result.Failures++
	for c.result.Failures < c.result.Required {
		select {
		case <-ctx.Done():
			c.Finish("skipped", "Verification canceled or timed out")
			return
		case <-time.After(time.Second):
		}
		c.result.Direct = c.probe(ctx, prober, "direct")
		if c.result.Direct.Reachable {
			c.Finish("direct_recovered", "Direct origin recovered during confirmation; direct routing retained")
			return
		}
		if ctx.Err() != nil {
			c.Finish("skipped", "Verification canceled or timed out")
			return
		}
		c.result.Failures++
	}
	c.Finish("candidate_ready", "Direct checks failed and proxy origin check succeeded; verifying installed route")
}

// Verify is called only after the dedicated live rule slot has changed.
// Empty outbound asks the core to use the ordinary router and rule order.
func (c *LearningCheck) Verify(prober LearningProber) bool {
	ctx, cancel := context.WithTimeout(c.ctx, 8*time.Second)
	defer cancel()
	c.result.Proxy = c.probe(ctx, prober, "")
	if ctx.Err() == nil && c.result.Proxy.Reachable {
		c.Finish("verified", "Direct checks failed; proxy origin succeeded before and after applying the route")
		return true
	}
	c.Finish("rolled_back", "Installed route failed origin verification; previous rules restored")
	return false
}

// ObserveFailureJSON preserves the observed port and permits bounded active
// confirmation, sharing the desktop tracker rather than requiring user retries.
func (t *ExperimentTracker) ObserveFailureJSON(message string, nowMillis int64, proxyHealthy, autoMode, cfRegion bool) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.UnixMilli(nowMillis)
	if autoMode {
		if candidate := t.tracker.ObserveFailure(message, now, proxyHealthy); candidate != nil {
			return candidateJSON(candidate)
		}
	}
	if cfRegion {
		return candidateJSON(t.tracker.ObserveRegionBlock(message, now, proxyHealthy))
	}
	return ""
}

func (t *ExperimentTracker) ObserveConnectionJSON(id, domain, destination, network, outbound, rule string, closed bool, downlink, nowMillis int64) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	kind := daemon.ConnectionEventType_CONNECTION_EVENT_NEW
	if closed {
		kind = daemon.ConnectionEventType_CONNECTION_EVENT_CLOSED
	}
	return candidateJSON(t.tracker.ObserveConnection(&daemon.ConnectionEvent{Id: id, Type: kind, Connection: &daemon.Connection{Domain: domain, Destination: destination, Network: network, Outbound: outbound, Rule: rule, DownlinkTotal: downlink}}, time.UnixMilli(nowMillis)))
}

func candidateJSON(candidate *experiment.Candidate) string {
	if candidate == nil {
		return ""
	}
	raw, _ := json.Marshal(candidate)
	return string(raw)
}
