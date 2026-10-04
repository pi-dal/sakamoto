// Package watch implements ordered fallback groups through sing-box's gRPC API.
package watch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/experiment"
	"github.com/pi-dal/sakamoto/internal/icloud"
	"github.com/pi-dal/sakamoto/internal/sbclient"
	"github.com/pi-dal/sakamoto/internal/svc"
	"github.com/pi-dal/sakamoto/internal/sysproxy"
	"github.com/sagernet/sing-box/daemon"
)

// Event reports a watcher transition to the TUI or logs.
type Event struct {
	Time    time.Time
	Message string
	Level   string // "info" | "switch" | "error"
}

type Watcher struct {
	cfg     *config.Config
	cfgPath string
	dialFn  func(ctx context.Context) (*sbclient.Client, error)

	events       chan Event
	healthy      map[string]int // Candidate tag to consecutive healthy checks for recovery.
	lastSnap     *daemon.Groups
	recoveryCh   chan recoveryRequest
	lastRecovery time.Time
}

func New(cfg *config.Config, dialFn func(ctx context.Context) (*sbclient.Client, error)) *Watcher {
	return &Watcher{
		cfg:        cfg,
		cfgPath:    config.DefaultPath(),
		dialFn:     dialFn,
		events:     make(chan Event, 64),
		healthy:    map[string]int{},
		recoveryCh: make(chan recoveryRequest, 1),
	}
}

func (w *Watcher) SetConfigPath(path string) { w.cfgPath = path }

// Events returns the read-only transition stream.
func (w *Watcher) Events() <-chan Event { return w.events }

func (w *Watcher) emit(level, format string, args ...any) {
	select {
	case w.events <- Event{Time: time.Now(), Level: level, Message: fmt.Sprintf(format, args...)}:
	default: // Drop when full instead of blocking the main loop.
	}
}

// Run reconnects on stream failure and exits when ctx is canceled.
func (w *Watcher) Run(ctx context.Context) error {
	ready := make(chan error, 1)
	go w.serveRecovery(ctx, ready)
	select {
	case err := <-ready:
		if err != nil {
			return err
		} // A second watcher must not control selectors.
	case <-ctx.Done():
		return ctx.Err()
	}
	go w.proxyLoop(ctx)
	backoff := time.Second
	for {
		err := w.runOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w.emit("error", "API disconnected: %v; reconnecting in %s", err, backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (w *Watcher) proxyLoop(ctx context.Context) {
	stateFile := filepath.Join(filepath.Dir(w.cfgPath), "proxy-restore.json")
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	var lastSync time.Time
	for {
		cfg, err := config.Load(w.cfgPath)
		if err == nil {
			if cfg.ICloud.Enabled && time.Since(lastSync) >= time.Minute {
				lastSync = time.Now()
				updates, syncErr := icloud.Sync(filepath.Dir(w.cfgPath), cfg)
				if syncErr != nil {
					w.emit("error", "iCloud sync failed: %v", syncErr)
				} else {
					for _, item := range updates {
						w.emit("info", "iCloud: %s", item)
					}
				}
			}
			status, e := svc.Send("status")
			connected := e == nil && strings.HasPrefix(status, "connected")
			_, beforeErr := os.Stat(stateFile)
			if err = sysproxy.Sync(stateFile, cfg, connected, sysproxy.Networksetup); err != nil {
				w.emit("error", "system proxy sync failed: %v", err)
			} else {
				_, afterErr := os.Stat(stateFile)
				if os.IsNotExist(beforeErr) && afterErr == nil {
					w.emit("info", "system HTTP/HTTPS proxy enabled for browsers on port %d", cfg.MixedInbound.Port)
				}
				if beforeErr == nil && os.IsNotExist(afterErr) {
					w.emit("info", "previous system proxy settings restored")
				}
			}
		} else {
			w.emit("error", "could not read sidecar config: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Watcher) runOnce(ctx context.Context) error {
	c, err := w.dialFn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	w.lastSnap = nil // Never evaluate a stale snapshot from a prior API connection.
	w.emit("info", "connected to sing-box API")

	groupsCh, errCh := c.SubscribeGroups(ctx)
	var connectionCh <-chan *daemon.ConnectionEvents
	var connectionErrors <-chan error
	var logCh <-chan *daemon.Log
	var logErrors <-chan error
	var autoCancel context.CancelFunc
	defer func() {
		if autoCancel != nil {
			autoCancel()
		}
	}()
	tracker := experiment.NewTracker(w.cfg.Experiment.Threshold)
	startAuto := func() {
		if autoCancel != nil {
			return
		}
		var subCtx context.Context
		subCtx, autoCancel = context.WithCancel(ctx)
		connectionCh, connectionErrors = c.SubscribeConnections(subCtx, 500)
		logCh, logErrors = c.SubscribeLog(subCtx)
	}
	if w.experimentEnabled() {
		startAuto()
	}
	tick := time.NewTicker(w.cfg.CheckInterval)
	defer tick.Stop()
	var settle *time.Timer
	var settleCh <-chan time.Time
	var started int64
	defer func() {
		if settle != nil {
			settle.Stop()
		}
	}()
	startTests := func() {
		started = time.Now().Unix()
		for _, chain := range w.cfg.Fallbacks {
			for _, tag := range chain {
				testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := c.URLTest(testCtx, tag)
				cancel()
				if err != nil {
					w.emit("error", "URL test %s failed: %v", tag, err)
				}
			}
		}
		settleDuration := w.cfg.TestSettle
		if settleDuration < 10*time.Second {
			settleDuration = 10 * time.Second
		}
		settle = time.NewTimer(settleDuration)
		settleCh = settle.C
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			return err
		case err := <-connectionErrors:
			return err
		case err := <-logErrors:
			return err
		case events, ok := <-connectionCh:
			if !ok {
				return fmt.Errorf("auto connection stream closed")
			}
			if w.experimentEnabled() && events != nil {
				for _, ev := range events.Events {
					tracker.Connection(ev, time.Now())
				}
			}
		case entry, ok := <-logCh:
			if !ok {
				return fmt.Errorf("auto log stream closed")
			}
			if w.experimentEnabled() && entry != nil {
				for _, msg := range entry.Messages {
					if msg.Level != daemon.LogLevel_ERROR && !w.cfg.Experiment.CFRegionBlock {
						continue
					}
					if w.cfg.Experiment.Mode == "auto" {
						if domain := tracker.Failure(msg.Message, time.Now(), w.proxyHealthy()); domain != "" {
							w.learnDomain(ctx, domain, "repeated direct timeouts")
						}
					}
					if w.cfg.Experiment.CFRegionBlock {
						if domain := tracker.CloudflareRegionBlock(msg.Message, time.Now(), w.proxyHealthy()); domain != "" {
							w.learnDomain(ctx, domain, "Cloudflare region block")
						}
					}
				}
			}
		case g, ok := <-groupsCh:
			if !ok {
				return fmt.Errorf("groups stream closed")
			}
			w.lastSnap = g // Consume results continuously; sleeping would stall the stream.
		case req := <-w.recoveryCh:
			if req.ctx.Err() != nil {
				continue
			}
			latest, err := config.Load(w.cfgPath)
			if err != nil {
				req.reply <- "unavailable"
				continue
			}
			w.cfg = latest
			decision := recoveryDecision(w.cfg.FallbackEnabled, w.cfg.Fallbacks["MainProxy"], w.lastSnap, settleCh != nil, w.lastRecovery, time.Now())
			if decision == "queued" {
				w.lastRecovery = time.Now()
				req.reply <- "queued" // Acknowledge before tests block the event loop.
				startTests()
				w.emit("info", "on-demand URL tests queued for MainProxy")
				continue
			}
			req.reply <- decision
		case <-tick.C:
			if settleCh != nil || w.lastSnap == nil {
				continue
			}
			if latest, err := config.Load(w.cfgPath); err == nil {
				if experimentEnabled(latest) && autoCancel == nil {
					startAuto()
				}
				if !experimentEnabled(latest) && autoCancel != nil {
					autoCancel()
					autoCancel = nil
					connectionCh = nil
					connectionErrors = nil
					logCh = nil
					logErrors = nil
				}
				if latest.Experiment.Threshold != w.cfg.Experiment.Threshold {
					tracker = experiment.NewTracker(latest.Experiment.Threshold)
				}
				w.cfg = latest
			}
			if !w.cfg.FallbackEnabled || len(w.cfg.Fallbacks) == 0 {
				continue
			}
			startTests()
		case <-settleCh:
			settleCh = nil
			w.evaluate(ctx, c, started)
		}
	}
}

// evaluate uses fresh group snapshots rather than stale cached delays.
func (w *Watcher) evaluate(ctx context.Context, c *sbclient.Client, started int64) {
	snap := w.lastSnap
	if snap == nil {
		return
	}
	for selTag, chain := range w.cfg.Fallbacks {
		sel := findGroup(snap, selTag)
		if sel == nil {
			continue
		}
		cur := sel.Selected
		curIdx := indexOf(chain, cur)
		if curIdx < 0 {
			continue // Respect a manual selection outside the fallback chain.
		}
		// Only current-round tests may prevent failover.
		desired, desiredIdx := pickFallback(snap, chain, started)
		if desired == "" {
			w.emit("error", "%s: no healthy group in fallback chain", selTag)
			continue
		}
		if desired == cur {
			for k := range w.healthy {
				w.healthy[k] = 0
			}
			continue
		}
		if desiredIdx > curIdx {
			// Fail over immediately when the current choice is unhealthy.
			w.emit("switch", "%s: %s unhealthy; switching to %s", selTag, cur, desired)
			if err := c.SelectOutbound(ctx, selTag, desired); err != nil {
				w.emit("error", "%s: switch failed: %v", selTag, err)
			}
		} else {
			// Count consecutive healthy checks before restoring a preferred group.
			w.healthy[desired]++
			if w.healthy[desired] >= w.cfg.RecoverAfter {
				w.emit("switch", "%s: %s recovered after %d healthy checks; switching back", selTag, desired, w.healthy[desired])
				if err := c.SelectOutbound(ctx, selTag, desired); err != nil {
					w.emit("error", "%s: switch-back failed: %v", selTag, err)
				}
				w.healthy[desired] = 0
			} else {
				w.emit("info", "%s: %s recovering (%d/%d)", selTag, desired, w.healthy[desired], w.cfg.RecoverAfter)
			}
		}
	}
}

// proxyHealthy requires a recent successful URL test of the selected exit
// group; during a general outage a direct timeout must not poison routing.
func (w *Watcher) experimentEnabled() bool {
	return experimentEnabled(w.cfg)
}

func experimentEnabled(cfg *config.Config) bool {
	return cfg.Experiment.Mode == "auto" || cfg.Experiment.CFRegionBlock
}

func (w *Watcher) proxyHealthy() bool {
	if w.lastSnap == nil {
		return false
	}
	main := findGroup(w.lastSnap, "MainProxy")
	if main == nil || main.Selected == "" {
		return false
	}
	return aliveFresh(w.lastSnap, main.Selected, time.Now().Add(-2*time.Minute).Unix())
}

func (w *Watcher) learnDomain(ctx context.Context, domain, reason string) {
	if ctx.Err() != nil {
		return
	}
	dir := filepath.Dir(w.cfgPath)
	candidate, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		w.emit("error", "auto: could not read config: %v", err)
		return
	}
	outbound, err := experiment.ProxyOutbound(candidate)
	if err != nil {
		w.emit("error", "auto: could not find proxy exit: %v", err)
		return
	}
	rollback, err := experiment.Stage(dir, domain, outbound)
	if err != nil {
		w.emit("error", "auto: route validation failed: %v", err)
		return
	}
	if rollback == nil {
		return
	}
	w.emit("info", "%s: %s; proxy rule saved, reconnecting to apply", domain, reason)
	reconnectErr := svc.Reconnect()
	if reconnectErr == nil {
		probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		for probeCtx.Err() == nil {
			c, e := w.dialFn(probeCtx)
			if e == nil {
				c.Close()
				w.emit("switch", "%s: automatic proxy route is active", domain)
				return
			}
			select {
			case <-probeCtx.Done():
			case <-time.After(300 * time.Millisecond):
			}
		}
		reconnectErr = fmt.Errorf("API did not recover after auto route update")
	}
	if restoreErr := rollback(); restoreErr != nil {
		w.emit("error", "auto: rollback failed: %v", restoreErr)
		return
	}
	if restoreErr := svc.Reconnect(); restoreErr != nil {
		w.emit("error", "auto: reconnect with previous config failed: %v", restoreErr)
	}
	w.emit("error", "%s: auto route failed and was rolled back: %v", domain, reconnectErr)
}

func pickFallback(snap *daemon.Groups, chain []string, started int64) (string, int) {
	for i, tag := range chain {
		if aliveFresh(snap, tag, started) {
			return tag, i
		}
	}
	return "", -1
}

// aliveFresh accepts a fresh successful member test, not a group's stale delay.
func aliveFresh(snap *daemon.Groups, tag string, started int64) bool {
	if g := findGroup(snap, tag); g != nil {
		for _, it := range g.Items {
			if it.UrlTestDelay > 0 && it.UrlTestTime >= started {
				return true
			}
		}
		return false
	}
	for _, g := range snap.Group {
		for _, it := range g.Items {
			if it.Tag == tag && it.UrlTestDelay > 0 && it.UrlTestTime >= started {
				return true
			}
		}
	}
	return false
}

func findGroup(snap *daemon.Groups, tag string) *daemon.Group {
	for _, g := range snap.Group {
		if g.Tag == tag {
			return g
		}
	}
	return nil
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
