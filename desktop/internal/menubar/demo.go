package menubar

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/pi-dal/sakamoto/internal/core"
	"github.com/pi-dal/sakamoto/internal/desktop"
	"github.com/sagernet/sing-box/daemon"
	"google.golang.org/protobuf/proto"
)

// Demo never reads runtime files, opens an API connection or controls a tunnel.
type Demo struct {
	mu      sync.Mutex
	state   desktop.Snapshot
	changes chan struct{}
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
	return &Demo{changes: make(chan struct{}, 1), state: desktop.Snapshot{
		Observing: true, Service: core.ServiceRunning, Phase: core.PhaseReachable, Probe: core.ProbeReachable, ProbePath: core.PathBoth,
		ConfigReady: true, APIReady: true, DNS: "protected", Mode: "Rule", Modes: []string{"Rule", "Global", "Direct"}, Tests: map[string]core.NodeStatus{},
		Traffic: desktop.Traffic{Available: true, Downlink: 6 * 1024 * 1024, Uplink: 384 * 1024, DownlinkTotal: 235 * 1024 * 1024, UplinkTotal: 12 * 1024 * 1024},
		Groups: []*daemon.Group{
			{Tag: "MainProxy", Selectable: true, Selected: "RealityAuto", Items: []*daemon.GroupItem{{Tag: "RealityAuto"}, {Tag: "OthersAuto"}, {Tag: "ManualPick"}}},
			{Tag: "RealityAuto", Selected: "Demo Tokyo", Items: items},
			{Tag: "OthersAuto", Selected: "Demo Singapore", Items: items[2:]},
			{Tag: "ManualPick", Selectable: true, Selected: "Demo Tokyo", Items: items},
		},
	}}
}
func (d *Demo) Snapshot() desktop.Snapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.state
	s.Groups = nil
	for _, g := range d.state.Groups {
		s.Groups = append(s.Groups, proto.Clone(g).(*daemon.Group))
	}
	s.Modes = append([]string(nil), s.Modes...)
	s.Tests = map[string]core.NodeStatus{}
	for k, v := range d.state.Tests {
		s.Tests[k] = v
	}
	return s
}
func (d *Demo) Changes() <-chan struct{} { return d.changes }
func (d *Demo) SetVisible(bool)          {}
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
		s.Health = desktop.HealthReport{State: "healthy", Action: "none", Verified: true, CheckedAt: time.Now(), Summary: summary}
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
		s.Health = desktop.HealthReport{State: state, Action: action, Verified: true, CheckedAt: time.Now(), Summary: summary}
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
