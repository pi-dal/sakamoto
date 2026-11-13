package menubar

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/core"
	"github.com/pi-dal/sakamoto/internal/desktop"
	"github.com/pi-dal/sakamoto/internal/gen"
	"github.com/sagernet/sing-box/daemon"
	"google.golang.org/protobuf/proto"
)

// Demo never reads runtime files, opens an API connection or controls a tunnel.
type Demo struct {
	mu                 sync.Mutex
	state              desktop.Snapshot
	changes            chan struct{}
	configuration      desktop.Configuration
	configurationSaved bool
	management         desktop.Management
	learning           desktop.Learning
	automaticGroups    desktop.AutomaticGroups
}

func NewDemo() *Demo {
	now := time.Now().Unix()
	items := []*daemon.GroupItem{
		{Tag: "Demo Tokyo", Type: "test", UrlTestDelay: 42, UrlTestTime: now},
		{Tag: "Demo Osaka", Type: "test"},
		{Tag: "Demo Singapore", Type: "test", UrlTestDelay: 86, UrlTestTime: now},
		{Tag: "Demo Frankfurt", Type: "test", UrlTestDelay: -1, UrlTestTime: now},
		{Tag: "Demo San Francisco", Type: "test", UrlTestDelay: 145, UrlTestTime: now},
	}
	return &Demo{changes: make(chan struct{}, 1),
		automaticGroups: desktop.AutomaticGroups{Generated: true, Groups: []desktop.AutomaticGroup{
			{Tag: "RealityAuto", Nodes: []desktop.AutomaticGroupNode{{Tag: "Demo Tokyo", Present: true}, {Tag: "Demo Osaka", Present: true}, {Tag: "Demo Absent", Excluded: true}}},
			{Tag: "OthersAuto", Nodes: []desktop.AutomaticGroupNode{{Tag: "Demo Singapore", Present: true}, {Tag: "Demo Frankfurt", Present: true}, {Tag: "Demo San Francisco", Present: true}}},
		}},
		management: desktop.Management{Policies: []config.PolicyRule{{Match: "existing.example", Action: "direct"}}},
		learning:   desktop.Learning{Mode: "auto", LearnedCount: 2, Checks: []desktop.LearningCheck{{Domain: "failed.example", State: "no_improvement", Detail: "Direct origin check failed", Time: time.Now()}}, Failures: []desktop.LearningCheck{{Domain: "failed.example", State: "no_improvement", Time: time.Now()}}},
		configuration: desktop.Configuration{Fields: []desktop.ConfigurationField{
			{Section: "Network", Key: "chain_enabled", Label: "Chain exit", Kind: desktop.ConfigBool, Value: "on"},
			{Section: "Network", Key: "chain_exit", Label: "Chain exit node", Kind: desktop.ConfigText, Value: "Demo Tokyo"},
			{Section: "Network", Key: "strict_route", Label: "Strict routing", Kind: desktop.ConfigBool, Value: "off"},
			{Section: "Network", Key: "tun_stack", Label: "TUN stack", Kind: desktop.ConfigChoice, Value: "gvisor", Choices: []string{"system", "gvisor", "mixed"}},
			{Section: "DNS", Key: "dns_enabled", Label: "Protected system DNS", Kind: desktop.ConfigBool, Value: "off"},
			{Section: "DNS", Key: "dns_service", Label: "DNS network service", Kind: desktop.ConfigText, Value: "Wi-Fi"},
			{Section: "Traffic policy", Key: "experiment_mode", Label: "Unmatched policy", Kind: desktop.ConfigChoice, Value: "off", Choices: []string{"off", "on", "auto"}},
			{Section: "Advanced", Key: "log_level", Label: "Log level", Kind: desktop.ConfigChoice, Value: "info", Choices: []string{"error", "warn", "info", "debug"}},
			{Section: "Fallback", Key: "fallback_enabled", Label: "Automatic fallback", Kind: desktop.ConfigBool, Value: "on"},
		}, SubscriptionCount: 2, PolicyCount: 1, NodesPath: "~/.sakamoto/nodes.txt"}, state: desktop.Snapshot{
			Observing: true, Service: core.ServiceRunning, Phase: core.PhaseReachable, Probe: core.ProbeReachable, ProbePath: core.PathBoth,
			ConfigReady: true, APIReady: true, DNS: "protected", Health: desktop.HealthReport{FallbackAvailable: true}, Mode: "Rule", Modes: []string{"Rule", "Global", "Direct"}, Tests: map[string]core.NodeStatus{},
			Traffic:          desktop.Traffic{Available: true, Downlink: 6 * 1024 * 1024, Uplink: 384 * 1024, DownlinkTotal: 235 * 1024 * 1024, UplinkTotal: 12 * 1024 * 1024},
			ConnectionsState: desktop.ConnectionsLive,
			Connections: []desktop.Connection{
				{ID: "demo-web", Target: "example.com", Destination: "192.0.2.1:443", Process: "Safari", Network: "tcp", Outbound: "Demo Tokyo", DownlinkTotal: 12 * 1024 * 1024, UplinkTotal: 128 * 1024},
				{ID: "demo-update", Target: "updates.example.org", Destination: "192.0.2.2:443", Process: "Software Update", Network: "tcp", Outbound: "direct", DownlinkTotal: 864 * 1024, UplinkTotal: 32 * 1024},
				{ID: "demo-ipv6", Target: "[2001:db8::1]:443", Destination: "[2001:db8::1]:443", Network: "udp", Outbound: "Demo Singapore", DownlinkTotal: 48 * 1024, UplinkTotal: 16 * 1024},
			},
			Groups: []*daemon.Group{
				{Tag: "MainProxy", Selectable: true, Selected: "RealityAuto", Items: []*daemon.GroupItem{{Tag: "RealityAuto"}, {Tag: "OthersAuto"}, {Tag: "ManualPick"}}},
				{Tag: "RealityAuto", Selected: "Demo Tokyo", Items: items},
				{Tag: "OthersAuto", Selected: "Demo Singapore", Items: items[2:]},
				{Tag: "ManualPick", Selectable: true, Selected: "Demo Tokyo", Items: items},
			},
		}}
}
func (d *Demo) Configuration() (desktop.Configuration, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	value := d.configuration
	value.Fields = append([]desktop.ConfigurationField(nil), d.configuration.Fields...)
	return value, nil
}
func (d *Demo) Management() (desktop.Management, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := d.management
	m.Nodes = append([]desktop.ManualNode(nil), m.Nodes...)
	m.Subscriptions = append([]config.SubSource(nil), m.Subscriptions...)
	m.Policies = append([]config.PolicyRule(nil), m.Policies...)
	return m, nil
}
func (d *Demo) AutomaticGroups() (desktop.AutomaticGroups, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	value := d.automaticGroups
	value.Groups = slices.Clone(value.Groups)
	for i := range value.Groups {
		value.Groups[i].Nodes = slices.Clone(value.Groups[i].Nodes)
	}
	return value, nil
}
func (d *Demo) SaveAutomaticGroup(_ context.Context, _ desktop.AutomaticGroups, tag string, excluded []string) error {
	return d.update(func(s *desktop.Snapshot) {
		for i := range d.automaticGroups.Groups {
			if d.automaticGroups.Groups[i].Tag == tag {
				for j := range d.automaticGroups.Groups[i].Nodes {
					node := &d.automaticGroups.Groups[i].Nodes[j]
					node.Excluded = slices.Contains(excluded, node.Tag)
				}
			}
		}
		s.Notice = core.Successf("Configuration saved. Regenerate to apply.")
	})
}
func (d *Demo) Learning() (desktop.Learning, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v := d.learning
	v.Checks = append([]desktop.LearningCheck(nil), v.Checks...)
	v.Failures = append([]desktop.LearningCheck(nil), v.Failures...)
	return v, nil
}
func (d *Demo) SaveNode(_ context.Context, original desktop.ManualNode, raw string) error {
	var entry desktop.ManualNode
	if raw != "" {
		var err error
		entry, err = gen.ParseNodeLink(raw)
		if err != nil {
			return err
		}
	}
	return d.update(func(s *desktop.Snapshot) {
		if original.Line == 0 {
			entry.Line = len(d.management.Nodes) + 1
			d.management.Nodes = append(d.management.Nodes, entry)
		} else {
			for i, v := range d.management.Nodes {
				if v.Line == original.Line {
					if raw == "" {
						d.management.Nodes = append(d.management.Nodes[:i], d.management.Nodes[i+1:]...)
					} else {
						entry.Line = v.Line
						d.management.Nodes[i] = entry
					}
					break
				}
			}
		}
		s.Notice = core.Successf("Configuration saved. Regenerate to apply.")
	})
}
func (d *Demo) SaveSubscriptions(_ context.Context, _ desktop.Management, values []config.SubSource) error {
	return d.update(func(s *desktop.Snapshot) {
		d.management.Subscriptions = append([]config.SubSource(nil), values...)
		s.Notice = core.Successf("Configuration saved. Regenerate to apply.")
	})
}
func (d *Demo) SavePolicies(_ context.Context, _ desktop.Management, values []config.PolicyRule) error {
	return d.update(func(s *desktop.Snapshot) {
		d.management.Policies = append([]config.PolicyRule(nil), values...)
		s.Notice = core.Successf("Configuration saved. Regenerate to apply.")
	})
}
func (d *Demo) AddRecentFailures(_ context.Context, domains []string) error {
	return d.update(func(s *desktop.Snapshot) {
		for _, domain := range domains {
			found := false
			for _, v := range d.management.Policies {
				if v.Match == domain {
					found = true
				}
			}
			if !found {
				d.management.Policies = append(d.management.Policies, config.PolicyRule{Match: domain, Action: "proxy"})
			}
		}
		s.Notice = core.Successf("Proxy rules saved. Regenerate to apply.")
	})
}

func (d *Demo) ConfigurationChanged() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.configurationSaved
}
func (d *Demo) SaveConfiguration(_ context.Context, value desktop.Configuration) error {
	return d.update(func(s *desktop.Snapshot) {
		d.configuration = value
		d.configuration.Fields = append([]desktop.ConfigurationField(nil), value.Fields...)
		d.configurationSaved = true
		s.Notice = core.Successf("Configuration saved")
	})
}

func (d *Demo) Snapshot() desktop.Snapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.state
	s.Groups = nil
	for _, g := range d.state.Groups {
		s.Groups = append(s.Groups, proto.Clone(g).(*daemon.Group))
	}
	s.Connections = append([]desktop.Connection(nil), s.Connections...)
	s.Modes = append([]string(nil), s.Modes...)
	s.Tests = map[string]core.NodeStatus{}
	for k, v := range d.state.Tests {
		s.Tests[k] = v
	}
	return s
}
func (d *Demo) Changes() <-chan struct{}   { return d.changes }
func (d *Demo) SetVisible(bool)            {}
func (d *Demo) SetConnectionsVisible(bool) {}
func (d *Demo) update(fn func(*desktop.Snapshot)) error {
	d.mu.Lock()
	fn(&d.state)
	d.mu.Unlock()
	select {
	case d.changes <- struct{}{}:
	default:
	}
	return nil
}
func (d *Demo) Connect(context.Context) error {
	return d.update(func(s *desktop.Snapshot) {
		s.Service = core.ServiceRunning
		s.Probe = core.ProbeReachable
		s.Phase = core.PhaseReachable
		s.APIReady = true
		s.Notice = core.Successf("Demo connected")
	})
}
func (d *Demo) Disconnect(context.Context) error {
	return d.update(func(s *desktop.Snapshot) {
		s.Service = core.ServiceStopped
		s.Probe = core.ProbeIdle
		s.Phase = core.PhaseDisconnected
		s.APIReady = false
		s.Notice = core.Successf("Demo disconnected")
	})
}
func (d *Demo) SetMode(_ context.Context, mode string) error {
	return d.update(func(s *desktop.Snapshot) { s.Mode = mode; s.Notice = core.Successf("Mode: %s", mode) })
}
func (d *Demo) SelectNode(_ context.Context, group, tag string) error {
	return d.update(func(s *desktop.Snapshot) {
		for _, g := range s.Groups {
			if g.Tag == group && g.Selectable {
				g.Selected = tag
				s.Notice = core.Successf("Selected %s", tag)
				return
			}
		}
		for _, g := range s.Groups {
			if g.Tag == "ManualPick" {
				g.Selected = tag
			}
			if g.Tag == "MainProxy" {
				g.Selected = "ManualPick"
			}
		}
		s.Notice = core.Successf("Selected %s", tag)
	})
}
func (d *Demo) CheckHealth(context.Context) error {
	return d.update(func(s *desktop.Snapshot) {
		summary := "Core API and DNS verified."
		if s.ExternalCore {
			summary = "Core API verified."
		}
		s.Health = desktop.HealthReport{State: "healthy", Action: "none", Verified: true, CheckedAt: time.Now(), Summary: summary, FallbackAvailable: !s.ExternalCore}
		s.Notice = core.Successf("%s", s.Health.Summary)
	})
}
func (d *Demo) Repair(_ context.Context, restart bool) error {
	if d.Snapshot().ExternalCore {
		return errors.New("the external service owns repair")
	}
	return d.update(func(s *desktop.Snapshot) {
		action, summary := "none", "Core API and DNS verified."
		if restart {
			action, summary = "restarted", "Core reconnected. API and DNS verified."
		}
		state := "healthy"
		if restart {
			state = "repaired"
		}
		s.Health = desktop.HealthReport{State: state, Action: action, Verified: true, CheckedAt: time.Now(), Summary: summary, FallbackAvailable: true}
		s.Notice = core.Successf("%s", summary)
	})
}
func (d *Demo) RepairConfiguration(_ context.Context, fallback bool) error {
	if d.Snapshot().ExternalCore {
		return errors.New("the external service owns configuration recovery")
	}
	return d.update(func(s *desktop.Snapshot) {
		action, summary := "regenerated", "Configuration regenerated. Core API and DNS verified."
		if fallback {
			action, summary = "rolled_back", "Restored the last working configuration. Core API and DNS verified."
		}
		s.Health = desktop.HealthReport{State: "repaired", Action: action, Verified: true, CheckedAt: time.Now(), Summary: summary, FallbackAvailable: true}
		s.Notice = core.Successf("%s", summary)
	})
}
func (d *Demo) RecoverRoute(context.Context) error {
	if d.Snapshot().ExternalCore {
		return errors.New("the external service owns route recovery")
	}
	return d.update(func(s *desktop.Snapshot) {
		s.Recovery = desktop.RecoveryReport{State: "queued", Summary: "Route tests queued.", RequestedAt: time.Now()}
		s.Notice = core.Progressf("%s", s.Recovery.Summary)
	})
}

func (d *Demo) Test(_ context.Context, tag string) error {
	return d.update(func(s *desktop.Snapshot) {
		for _, g := range s.Groups {
			for _, item := range g.Items {
				if item.Tag == tag {
					item.UrlTestDelay = 38
					item.UrlTestTime = time.Now().Unix()
				}
			}
		}
		s.Tests[tag] = core.NodeReachable
		s.Notice = core.Successf("Demo latency measured")
	})
}
