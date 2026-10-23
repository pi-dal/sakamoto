package desktop

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/core"
	"github.com/pi-dal/sakamoto/internal/sbclient"
	"github.com/sagernet/sing-box/daemon"
	"google.golang.org/protobuf/proto"
)

// Options bounds observation and allows isolated end-to-end service fixtures.
type Options struct {
	ExternalCore  bool
	PollInterval  time.Duration
	ProbeURLs     []string
	ConflictCheck func(context.Context) bool
}

type Traffic struct {
	Available                                    bool
	Uplink, Downlink, UplinkTotal, DownlinkTotal int64
}

// Snapshot contains display data, never API credentials or server addresses.
type Snapshot struct {
	Observing             bool
	Health                HealthReport
	Recovery              RecoveryReport
	ExternalCore          bool
	Service               core.ServiceState
	Phase                 core.SessionPhase
	Probe                 core.ProbeState
	ProbePath             string
	Conflict              bool
	DNS                   string
	ConfigReady, APIReady bool
	RuntimeDir            string
	Mode                  string
	Modes                 []string
	Groups                []*daemon.Group
	Traffic               Traffic
	Busy                  string
	Notice                core.Notice
	Tests                 map[string]core.NodeStatus
}

// Controller owns observations and operations independently of any panel.
// Run is called once. Closing its context never sends a disconnect command.
type Controller struct {
	path              string
	opts              Options
	mu                sync.Mutex
	operation         sync.Mutex
	state             Snapshot
	changes           chan struct{}
	visible           bool
	visibilityChanged chan struct{}
	observerCancel    context.CancelFunc
	refreshRequested  chan struct{}
	epoch             uint64
	testStarted       map[string]time.Time
}

func New(path string, opts Options) *Controller {
	if opts.PollInterval <= 0 {
		opts.PollInterval = 5 * time.Second
	}
	if opts.ConflictCheck == nil {
		opts.ConflictCheck = ShadowrocketConnected
	}
	return &Controller{path: path, opts: opts, changes: make(chan struct{}, 1), visibilityChanged: make(chan struct{}), refreshRequested: make(chan struct{}, 1), testStarted: map[string]time.Time{}, state: Snapshot{ExternalCore: opts.ExternalCore, Service: core.ServiceUnavailable, Probe: core.ProbeIdle, RuntimeDir: filepath.Dir(path), Tests: map[string]core.NodeStatus{}}}
}

func (c *Controller) Changes() <-chan struct{} { return c.changes }

func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.state
	s.Phase = core.PhaseOf(s.Service, s.Probe, s.Conflict)
	if s.Busy == "Connecting" && s.Service == core.ServiceStopped {
		s.Phase = core.PhaseStarting
	}
	if s.Busy == "Disconnecting" && s.Service == core.ServiceRunning {
		s.Phase = core.PhaseStopping
	}
	s.Modes = append([]string(nil), s.Modes...)
	s.Groups = make([]*daemon.Group, len(c.state.Groups))
	for i, g := range c.state.Groups {
		s.Groups[i] = proto.Clone(g).(*daemon.Group)
	}
	s.Tests = make(map[string]core.NodeStatus, len(c.state.Tests))
	for tag, state := range c.state.Tests {
		s.Tests[tag] = state
	}
	return s
}

func (c *Controller) update(fn func(*Snapshot)) {
	c.mu.Lock()
	fn(&c.state)
	c.mu.Unlock()
	select {
	case c.changes <- struct{}{}:
	default:
	}
}
func (c *Controller) updateSession(epoch uint64, fn func(*Snapshot)) {
	c.update(func(s *Snapshot) {
		if c.epoch == epoch && c.visible && s.Service == core.ServiceRunning {
			fn(s)
		}
	})
}
func (c *Controller) SetVisible(visible bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.visible != visible {
		c.visible = visible
		if !visible {
			if c.observerCancel != nil {
				c.observerCancel()
			}
			c.epoch++
			c.state.Observing = false
			c.state.APIReady = false
			c.state.Probe = core.ProbeIdle
			c.state.ProbePath = ""
			c.state.Traffic = Traffic{}
		}
		close(c.visibilityChanged)
		c.visibilityChanged = make(chan struct{})
		select {
		case c.changes <- struct{}{}:
		default:
		}
	}
}
func (c *Controller) visibility() (bool, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.visible, c.visibilityChanged
}

func wait(ctx context.Context, duration time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(duration):
		return true
	}
}

// Run parks without timers or I/O while hidden. Explicit actions use their own
// bounded context and can complete independently of a visible panel.
func (c *Controller) Run(ctx context.Context) {
	for ctx.Err() == nil {
		visible, changed := c.visibility()
		if !visible {
			select {
			case <-ctx.Done():
				return
			case <-changed:
				continue
			}
		}
		c.runVisible(ctx)
	}
}

func (c *Controller) runVisible(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	c.mu.Lock()
	if !c.visible {
		c.mu.Unlock()
		cancel()
		return
	}
	c.observerCancel = cancel
	c.mu.Unlock()
	var sessionCancel context.CancelFunc
	var sessionKey string
	var workers sync.WaitGroup
	defer func() {
		cancel()
		if sessionCancel != nil {
			sessionCancel()
		}
		workers.Wait()
		c.mu.Lock()
		c.observerCancel = nil
		c.mu.Unlock()
	}()
	for ctx.Err() == nil {
		cfg, key := c.refresh(ctx)
		if ctx.Err() != nil {
			return
		}
		if key != sessionKey {
			if sessionCancel != nil {
				sessionCancel()
				sessionCancel = nil
			}
			c.update(func(s *Snapshot) {
				c.epoch++
				s.APIReady = false
				s.Mode = ""
				s.Modes = nil
				s.Groups = nil
				s.Traffic = Traffic{}
				s.Probe = core.ProbeIdle
				s.ProbePath = ""
				s.Tests = map[string]core.NodeStatus{}
				c.testStarted = map[string]time.Time{}
			})
			sessionKey = key
			if key != "" {
				c.mu.Lock()
				epoch := c.epoch
				c.mu.Unlock()
				sessionCancel = c.startObservers(ctx, cfg, epoch, &workers)
			}
		}
		c.expireTests()
		timer := time.NewTimer(c.opts.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-c.refreshRequested:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (c *Controller) startObservers(ctx context.Context, cfg *config.Config, epoch uint64, workers *sync.WaitGroup) context.CancelFunc {
	session, cancel := context.WithCancel(ctx)
	workers.Add(2)
	go func() { defer workers.Done(); c.watchAPI(session, cfg, epoch) }()
	go func() { defer workers.Done(); c.watchProbe(session, cfg, epoch) }()
	return cancel
}

func (c *Controller) refresh(ctx context.Context) (*config.Config, string) {
	raw, fileErr := os.ReadFile(c.path)
	cfg, loadErr := config.Load(c.path)
	ready := fileErr == nil && loadErr == nil
	if c.opts.ExternalCore {
		return c.refreshExternal(ctx, cfg, ready, raw)
	}
	reply, err := runtimeStatus(ctx, c.path)
	service := core.ServiceUnavailable
	dns, pid := "", ""
	fields := strings.Fields(reply)
	if err == nil && len(fields) > 0 {
		switch fields[0] {
		case "connected":
			service = core.ServiceRunning
		case "disconnected":
			service = core.ServiceStopped
		}
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "dns=") {
				dns = strings.TrimPrefix(f, "dns=")
			}
			if strings.HasPrefix(f, "pid=") {
				pid = f
			}
		}
	}
	if ctx.Err() != nil {
		return cfg, ""
	}
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	conflict := c.opts.ConflictCheck(checkCtx)
	cancel()
	c.update(func(s *Snapshot) {
		if !c.visible || ctx.Err() != nil {
			return
		}
		s.Observing = true
		s.Service, s.DNS, s.Conflict, s.ConfigReady = service, dns, conflict, ready
		if service != core.ServiceRunning {
			s.APIReady = false
			s.Groups = nil
			s.Mode = ""
			s.Modes = nil
			s.Probe = core.ProbeIdle
			s.ProbePath = ""
			s.Traffic = Traffic{}
		}
	})
	if service != core.ServiceRunning || !ready {
		return cfg, ""
	}
	return cfg, fmt.Sprintf("%x:%s", sha256.Sum256(raw), pid)
}

func (c *Controller) refreshExternal(ctx context.Context, cfg *config.Config, ready bool, raw []byte) (*config.Config, string) {
	service := core.ServiceUnavailable
	if ready {
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		client, err := sbclient.Dial(probeCtx, cfg.API.URL, cfg.API.Secret)
		cancel()
		if err == nil {
			service = core.ServiceRunning
			client.Close()
		}
	}
	c.update(func(s *Snapshot) {
		if !c.visible || ctx.Err() != nil {
			return
		}
		s.Observing = true
		s.Service, s.ConfigReady, s.Conflict = service, ready, false
		if service != core.ServiceRunning {
			s.APIReady = false
			s.Groups = nil
			s.Mode = ""
			s.Modes = nil
			s.Probe = core.ProbeIdle
			s.ProbePath = ""
			s.Traffic = Traffic{}
		}
	})
	if service != core.ServiceRunning {
		return cfg, ""
	}
	return cfg, fmt.Sprintf("external:%x", sha256.Sum256(raw))
}

func (c *Controller) watchProbe(ctx context.Context, cfg *config.Config, epoch uint64) {
	failures := 0
	for ctx.Err() == nil {
		c.updateSession(epoch, func(s *Snapshot) {
			if s.Probe != core.ProbeReachable {
				s.Probe = core.ProbeChecking
			}
		})
		outcome := ProbeNetwork(ctx, cfg, c.opts.ProbeURLs)
		if ctx.Err() != nil {
			return
		}
		c.updateSession(epoch, func(s *Snapshot) { s.Probe = outcome.State; s.ProbePath = outcome.Path })
		delay := core.ProbeSuccessRefresh
		if outcome.State == core.ProbeReachable {
			failures = 0
		} else {
			failures++
			delay = core.NextProbeRetry(failures)
		}
		if !wait(ctx, delay) {
			return
		}
	}
}

func (c *Controller) watchAPI(ctx context.Context, cfg *config.Config, epoch uint64) {
	for ctx.Err() == nil {
		dialCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		client, err := sbclient.Dial(dialCtx, cfg.API.URL, cfg.API.Secret)
		cancel()
		if err == nil {
			c.streamAPI(ctx, client, epoch)
			client.Close()
		}
		c.updateSession(epoch, func(s *Snapshot) {
			s.APIReady = false
			s.Groups = nil
			s.Mode = ""
			s.Modes = nil
			s.Traffic = Traffic{}
		})
		if !wait(ctx, time.Second) {
			return
		}
	}
}

func (c *Controller) streamAPI(ctx context.Context, client *sbclient.Client, epoch uint64) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	groups, groupErrors := client.SubscribeGroups(ctx)
	modes, modeErrors := client.SubscribeClashMode(ctx)
	modeCtx, modeCancel := context.WithTimeout(ctx, 4*time.Second)
	mode, err := client.ClashModeStatus(modeCtx)
	modeCancel()
	if err != nil {
		return
	}
	c.updateSession(epoch, func(s *Snapshot) { s.Mode = mode.CurrentMode; s.Modes = append([]string(nil), mode.ModeList...) })
	trafficDone := make(chan struct{})
	go func() { defer close(trafficDone); c.watchTraffic(ctx, client, epoch) }()
	defer func() { cancel(); <-trafficDone }()
	for {
		select {
		case <-ctx.Done():
			return
		case <-groupErrors:
			return
		case <-modeErrors:
			return
		case value, ok := <-modes:
			if !ok {
				return
			}
			c.updateSession(epoch, func(s *Snapshot) { s.Mode = value.Mode })
		case value, ok := <-groups:
			if !ok {
				return
			}
			c.updateSession(epoch, func(s *Snapshot) {
				s.APIReady = true
				s.Groups = value.Group
				for _, g := range value.Group {
					for _, item := range g.Items {
						if start, pending := c.testStarted[item.Tag]; pending && item.UrlTestTime > 0 && item.UrlTestTime >= start.Unix() {
							s.Tests[item.Tag] = core.StatusFromLatency(item.UrlTestDelay, false)
							if item.UrlTestDelay == 0 {
								s.Tests[item.Tag] = core.NodeFailed
							}
							delete(c.testStarted, item.Tag)
						}
					}
				}
			})
		}
	}
}

func (c *Controller) watchTraffic(ctx context.Context, client *sbclient.Client, epoch uint64) {
	for ctx.Err() == nil {
		visible, changed := c.visibility()
		if !visible {
			select {
			case <-ctx.Done():
				return
			case <-changed:
				continue
			}
		}
		streamCtx, cancel := context.WithCancel(ctx)
		values, failures := client.SubscribeStatus(streamCtx, 2*time.Second)
	stream:
		for {
			select {
			case <-ctx.Done():
				cancel()
				return
			case <-changed:
				break stream
			case <-failures:
				break stream
			case value, ok := <-values:
				if !ok {
					break stream
				}
				c.updateSession(epoch, func(s *Snapshot) {
					s.Traffic = Traffic{value.TrafficAvailable, value.Uplink, value.Downlink, value.UplinkTotal, value.DownlinkTotal}
				})
			}
		}
		cancel()
		c.updateSession(epoch, func(s *Snapshot) { s.Traffic = Traffic{} })
		if !wait(ctx, 100*time.Millisecond) {
			return
		}
	}
}

func (c *Controller) expireTests() {
	c.update(func(s *Snapshot) {
		for tag, start := range c.testStarted {
			if time.Since(start) > sbclient.URLTestNodeWait {
				s.Tests[tag] = core.NodeFailed
				delete(c.testStarted, tag)
			}
		}
	})
}

func (c *Controller) perform(ctx context.Context, name string, fn func(context.Context) error) error {
	if !c.operation.TryLock() {
		return errors.New("another action is in progress")
	}
	defer c.operation.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	defer func() {
		select {
		case c.refreshRequested <- struct{}{}:
		default:
		}
	}()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	progress := core.Progressf("%s…", name)
	c.update(func(s *Snapshot) { s.Busy = name; s.Notice = progress })
	err := fn(ctx)
	c.update(func(s *Snapshot) {
		s.Busy = ""
		// Actions may install a safe, more specific result (for example queued
		// route tests). Never replace that acknowledgement with generic success.
		if s.Notice.Text != progress.Text || s.Notice.Kind != progress.Kind {
			return
		}
		if err != nil {
			s.Notice = core.Errorf("%s failed. Refresh the connection and try again.", name)
		} else {
			s.Notice = core.Successf("%s completed", name)
		}
	})
	return err
}

func (c *Controller) Connect(ctx context.Context) error {
	if c.opts.ExternalCore {
		return errors.New("the core is managed by an external service")
	}
	return c.perform(ctx, "Connecting", func(ctx context.Context) error {
		if !c.Snapshot().ConfigReady {
			return errors.New("configuration is missing or invalid")
		}
		if c.opts.ConflictCheck(ctx) {
			return errors.New("disconnect Shadowrocket first")
		}
		reply, err := runtimeConnect(c.path)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(reply, "connected") && !strings.HasPrefix(reply, "already running") {
			return errors.New("supervisor did not confirm connection")
		}
		return nil
	})
}
func (c *Controller) Disconnect(ctx context.Context) error {
	if c.opts.ExternalCore {
		return errors.New("the core is managed by an external service")
	}
	return c.perform(ctx, "Disconnecting", func(ctx context.Context) error {
		reply, err := runtimeDisconnect(ctx, c.path)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(reply, "disconnected") {
			return errors.New("supervisor did not confirm disconnection")
		}
		return nil
	})
}
func (c *Controller) withAPI(ctx context.Context, fn func(context.Context, *sbclient.Client) error) error {
	if !c.Snapshot().APIReady {
		return errors.New("native API is unavailable")
	}
	cfg, err := config.Load(c.path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := sbclient.Dial(ctx, cfg.API.URL, cfg.API.Secret)
	if err != nil {
		return err
	}
	defer client.Close()
	return fn(ctx, client)
}
func (c *Controller) SetMode(ctx context.Context, mode string) error {
	return c.perform(ctx, "Changing mode", func(ctx context.Context) error {
		return c.withAPI(ctx, func(ctx context.Context, client *sbclient.Client) error {
			observed, err := ApplyMode(ctx, client, mode)
			if err == nil {
				c.update(func(s *Snapshot) { s.Mode = observed })
			}
			return err
		})
	})
}
func (c *Controller) SelectNode(ctx context.Context, group, tag string) error {
	return c.perform(ctx, "Selecting node", func(ctx context.Context) error {
		return c.withAPI(ctx, func(ctx context.Context, client *sbclient.Client) error {
			groups, failures := client.SubscribeGroups(ctx)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case err := <-failures:
				return err
			case value, ok := <-groups:
				if !ok {
					return errors.New("group stream ended")
				}
				return SelectNode(ctx, client, value.Group, group, tag)
			}
		})
	})
}
func (c *Controller) Test(ctx context.Context, tag string) error {
	return c.perform(ctx, "Requesting latency test", func(ctx context.Context) error {
		return c.withAPI(ctx, func(ctx context.Context, client *sbclient.Client) error {
			found := false
			for _, g := range c.Snapshot().Groups {
				for _, item := range g.Items {
					if item.Tag == tag {
						found = true
					}
				}
			}
			if !found {
				return errors.New("node is unavailable")
			}
			start := time.Now().Truncate(time.Second).Add(time.Second)
			c.update(func(s *Snapshot) { c.testStarted[tag] = start; s.Tests[tag] = core.NodeTesting })
			if !wait(ctx, time.Until(start)) {
				return ctx.Err()
			}
			if err := client.URLTest(ctx, tag); err != nil {
				c.update(func(s *Snapshot) { delete(c.testStarted, tag); s.Tests[tag] = core.NodeFailed })
				return err
			}
			return nil
		})
	})
}
