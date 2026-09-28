// Package watch 实现 sing-box 缺失的 fallback 组语义：
// selector 的有序降级链，由控制面（gRPC api）驱动。
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

// Event 上报给上层（TUI 或日志）。
type Event struct {
	Time    time.Time
	Message string
	Level   string // "info" | "switch" | "error"
}

type Watcher struct {
	cfg     *config.Config
	cfgPath string
	dialFn  func(ctx context.Context) (*sbclient.Client, error)

	events   chan Event
	healthy  map[string]int // 候选 tag → 连续健康次数（回切计票）
	lastSnap *daemon.Groups
}

func New(cfg *config.Config, dialFn func(ctx context.Context) (*sbclient.Client, error)) *Watcher {
	return &Watcher{
		cfg:     cfg,
		cfgPath: config.DefaultPath(),
		dialFn:  dialFn,
		events:  make(chan Event, 64),
		healthy: map[string]int{},
	}
}

func (w *Watcher) SetConfigPath(path string) { w.cfgPath = path }

// Events 只读事件流（switch/error 事件 TUI 可展示）。
func (w *Watcher) Events() <-chan Event { return w.events }

func (w *Watcher) emit(level, format string, args ...any) {
	select {
	case w.events <- Event{Time: time.Now(), Level: level, Message: fmt.Sprintf(format, args...)}:
	default: // 满了就丢，不阻塞主循环
	}
}

// Run 阻塞运行；断流自动重连，ctx 取消即退出。
func (w *Watcher) Run(ctx context.Context) error {
	go w.proxyLoop(ctx)
	backoff := time.Second
	for {
		err := w.runOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w.emit("error", "连接断开: %v — %s 后重连", err, backoff)
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
					w.emit("error", "iCloud 同步失败: %v", syncErr)
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
				w.emit("error", "系统代理同步失败: %v", err)
			} else {
				_, afterErr := os.Stat(stateFile)
				if os.IsNotExist(beforeErr) && afterErr == nil {
					w.emit("info", "系统 HTTP/HTTPS 代理已启用，浏览器改走 %d", cfg.MixedInbound.Port)
				}
				if beforeErr == nil && os.IsNotExist(afterErr) {
					w.emit("info", "系统代理已恢复原设置")
				}
			}
		} else {
			w.emit("error", "读取控制配置失败: %v", err)
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
	w.emit("info", "已连接 sing-box api")

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
	if w.cfg.Experiment.Mode == "auto" {
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
			if w.cfg.Experiment.Mode == "auto" && events != nil {
				for _, ev := range events.Events {
					tracker.Connection(ev, time.Now())
				}
			}
		case entry, ok := <-logCh:
			if !ok {
				return fmt.Errorf("auto log stream closed")
			}
			if w.cfg.Experiment.Mode == "auto" && entry != nil {
				for _, msg := range entry.Messages {
					if msg.Level != daemon.LogLevel_ERROR {
						continue
					}
					if domain := tracker.Failure(msg.Message, time.Now(), w.proxyHealthy()); domain != "" {
						w.learnDomain(ctx, domain)
					}
				}
			}
		case g, ok := <-groupsCh:
			if !ok {
				return fmt.Errorf("groups stream closed")
			}
			w.lastSnap = g // 持续读取测试结果，不能 Sleep 堵住订阅流
		case <-tick.C:
			if settleCh != nil || w.lastSnap == nil {
				continue
			}
			if latest, err := config.Load(w.cfgPath); err == nil {
				if latest.Experiment.Mode == "auto" && autoCancel == nil {
					startAuto()
				}
				if latest.Experiment.Mode != "auto" && autoCancel != nil {
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
			started = time.Now().Unix()
			for _, chain := range w.cfg.Fallbacks {
				for _, tag := range chain {
					if err := c.URLTest(ctx, tag); err != nil {
						w.emit("error", "测速 %s 失败: %v", tag, err)
					}
				}
			}
			settleDuration := w.cfg.TestSettle
			if settleDuration < 10*time.Second {
				settleDuration = 10 * time.Second
			} // allow slow group URL tests to publish fresh results
			settle = time.NewTimer(settleDuration)
			settleCh = settle.C
		case <-settleCh:
			settleCh = nil
			w.evaluate(ctx, c, started)
		}
	}
}

// evaluate 使用结算期间推送的新快照判断有序回落，不测试旧缓存。
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
			continue // 手动选到了链外成员（如 ManualPick），尊重人工选择
		}
		// 只认本轮测速后的结果；旧延迟不得阻止自动降级。
		desired, desiredIdx := pickFallback(snap, chain, started)
		if desired == "" {
			w.emit("error", "%s: 降级链全部不可用", selTag)
			continue
		}
		if desired == cur {
			for k := range w.healthy {
				w.healthy[k] = 0
			}
			continue
		}
		if desiredIdx > curIdx {
			// 当前死亡 → 立即降级
			w.emit("switch", "%s: %s 不可用 → 切 %s", selTag, cur, desired)
			if err := c.SelectOutbound(ctx, selTag, desired); err != nil {
				w.emit("error", "%s: 切换失败: %v", selTag, err)
			}
		} else {
			// 更优候选恢复 → 计票回切
			w.healthy[desired]++
			if w.healthy[desired] >= w.cfg.RecoverAfter {
				w.emit("switch", "%s: %s 已恢复 → 回切（%d 次连续健康）", selTag, desired, w.healthy[desired])
				if err := c.SelectOutbound(ctx, selTag, desired); err != nil {
					w.emit("error", "%s: 回切失败: %v", selTag, err)
				}
				w.healthy[desired] = 0
			} else {
				w.emit("info", "%s: %s 恢复中（%d/%d）", selTag, desired, w.healthy[desired], w.cfg.RecoverAfter)
			}
		}
	}
}

// proxyHealthy requires a recent successful URL test of the selected exit
// group; during a general outage a direct timeout must not poison routing.
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

func (w *Watcher) learnDomain(ctx context.Context, domain string) {
	if ctx.Err() != nil {
		return
	}
	dir := filepath.Dir(w.cfgPath)
	candidate, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		w.emit("error", "auto 读取配置失败: %v", err)
		return
	}
	outbound, err := experiment.ProxyOutbound(candidate)
	if err != nil {
		w.emit("error", "auto 查找代理出口失败: %v", err)
		return
	}
	rollback, err := experiment.Stage(dir, domain, outbound)
	if err != nil {
		w.emit("error", "auto 规则校验失败: %v", err)
		return
	}
	if rollback == nil {
		return
	}
	w.emit("info", "%s: 直连连续失败，已写入代理规则，正在重连应用", domain)
	reconnectErr := svc.Reconnect()
	if reconnectErr == nil {
		probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		for probeCtx.Err() == nil {
			c, e := w.dialFn(probeCtx)
			if e == nil {
				c.Close()
				w.emit("switch", "%s: 自动走代理已生效", domain)
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
		w.emit("error", "auto 回滚失败: %v", restoreErr)
		return
	}
	if restoreErr := svc.Reconnect(); restoreErr != nil {
		w.emit("error", "auto 恢复旧连接失败: %v", restoreErr)
	}
	w.emit("error", "%s: auto 未生效，已回滚: %v", domain, reconnectErr)
}

func pickFallback(snap *daemon.Groups, chain []string, started int64) (string, int) {
	for i, tag := range chain {
		if aliveFresh(snap, tag, started) {
			return tag, i
		}
	}
	return "", -1
}

// aliveFresh: tag 是组时任一成员在本轮成功即可；组本身的旧延迟不算数。
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
