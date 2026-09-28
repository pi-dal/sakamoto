// Package tui 提供以连接状态为中心的 sing-box 终端界面。
package tui

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/gen"
	"github.com/pi-dal/sakamoto/internal/sbclient"
	"github.com/pi-dal/sakamoto/internal/svc"
	"github.com/sagernet/sing-box/daemon"
)

var (
	accent = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	good   = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	bad    = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	muted  = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	focus  = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	tab    = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6")).Bold(true)
)

const (
	homePage = iota
	configPage
	dataPage
	settingsPage
)

var tabs = []string{"Home", "Config", "Data", "Settings"}

type groupsMsg *daemon.Groups
type statusMsg *daemon.Status
type connsMsg *daemon.ConnectionEvents
type logMsg *daemon.Log
type connectedMsg struct{ client *sbclient.Client }
type errMsg error
type tickMsg struct{}
type serviceMsg struct {
	state        string
	err          error
	shadowrocket bool
}
type networkMsg struct {
	ip  string
	err error
}
type actionMsg struct {
	text string
	err  error
}
type testDispatchMsg struct {
	batch  *testBatch
	errors map[string]error
}
type importMsg struct {
	source string
	err    error
}

type cfgRow struct {
	label  string
	value  func() string
	toggle func()
	edit   func(string) error
}
type row struct {
	group *daemon.Group
	item  *daemon.GroupItem
}
type hit struct {
	x0, x1, y int
	action    string
	index     int
}

type model struct {
	cfg                                             *config.Config
	cfgPath                                         string
	conn                                            *sbclient.Client
	groups                                          []*daemon.Group
	status                                          *daemon.Status
	conns                                           map[string]*daemon.Connection
	logs                                            []string
	rows                                            []row
	cfgRows                                         []cfgRow
	page, cursor, cfgCursor, scroll, settingsScroll int
	width, height                                   int
	serviceState, mode, notice                      string
	shadowrocket                                    bool
	conflictStopping                                bool
	lastTest                                        map[string]int32
	networkState                                    string
	netChecking                                     bool
	exitLabel                                       string
	serviceErr                                      error
	hits                                            []hit // 每次 View 重建，鼠标只作用于可见元素
	prevHits                                        []hit
	hoverX, hoverY                                  int
	menuRow, detailRow                              int // -1=关闭，Home 右键菜单/节点详情
	configDetail, detailScroll                      int // -1=Config 列表，≥0=对应分节详情
	sourceCursor, nodeCursor                        int
	pendingDelete                                   string
	editNode                                        gen.NodeEntry
	revealInput                                     bool
	editing, input                                  string
	importing, importBusy                           bool
	importKind                                      string
	editIndex                                       int
	dataIDs                                         []string
	selectedConn                                    string
	batch                                           *testBatch
}

func Run(ctx context.Context, cfg *config.Config, path string) error {
	m := &model{cfg: cfg, cfgPath: path, conns: map[string]*daemon.Connection{}, height: 24, width: 80,
		menuRow: -1, detailRow: -1, configDetail: -1, editIndex: -1, hoverX: -1, hoverY: -1}
	m.exitLabel = exitFromConfig(path)
	m.groups = offlineGroups(path)
	m.rebuildRows()
	m.buildSettings()
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion(), tea.WithContext(ctx))
	go pump(ctx, cfg, p)
	_, err := p.Run()
	return err
}

func pump(ctx context.Context, cfg *config.Config, p *tea.Program) {
	for ctx.Err() == nil {
		c, err := sbclient.Dial(ctx, cfg.API.URL, cfg.API.Secret)
		if err != nil {
			p.Send(errMsg(err))
		} else {
			p.Send(connectedMsg{c})
			g, ge := c.SubscribeGroups(ctx)
			s, se := c.SubscribeStatus(ctx, 1000)
			cs, ce := c.SubscribeConnections(ctx, 2000)
			ls, le := c.SubscribeLog(ctx)
		stream:
			for {
				select {
				case v, ok := <-g:
					if !ok {
						break stream
					}
					p.Send(groupsMsg(v))
				case v, ok := <-s:
					if !ok {
						break stream
					}
					p.Send(statusMsg(v))
				case v, ok := <-cs:
					if !ok {
						break stream
					}
					p.Send(connsMsg(v))
				case v, ok := <-ls:
					if !ok {
						break stream
					}
					p.Send(logMsg(v))
				case <-ge:
					break stream
				case <-se:
					break stream
				case <-ce:
					break stream
				case <-le:
					break stream
				case <-ctx.Done():
					break stream
				}
			}
			c.Close()
			p.Send(errMsg(fmt.Errorf("控制核心已断开，正在重连")))
		}
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

func shadowrocketVPNConnected() bool {
	out, err := exec.Command("/usr/sbin/scutil", "--nc", "list").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "(Connected)") && strings.Contains(line, "com.liguangming.Shadowrocket") {
			return true
		}
	}
	return false
}
func queryService() tea.Msg {
	shadowrocket := shadowrocketVPNConnected()
	s, err := svc.Send("status")
	if err != nil {
		return serviceMsg{err: err, shadowrocket: shadowrocket}
	}
	return serviceMsg{state: strings.Fields(s)[0], shadowrocket: shadowrocket}
}
func diagnoseNetwork(expected string) tea.Msg {
	client := &http.Client{Timeout: 6 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return networkMsg{err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return networkMsg{err: fmt.Errorf("出口检测 HTTP %d", resp.StatusCode)}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 100))
	if err != nil {
		return networkMsg{err: err}
	}
	ip := strings.TrimSpace(string(b))
	if net.ParseIP(ip) == nil {
		return networkMsg{err: fmt.Errorf("出口检测未返回 IP")}
	}
	if host, _, err := net.SplitHostPort(expected); err == nil && net.ParseIP(host) != nil && host != ip {
		return networkMsg{err: fmt.Errorf("出口 IP %s，不是预期的 %s（检查系统路由）", ip, host)}
	}
	return networkMsg{ip: ip}
}
func tick() tea.Cmd            { return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return tickMsg{} }) }
func (m *model) Init() tea.Cmd { return tea.Batch(tick(), queryService) }

func (m *model) buildSettings() {
	c := m.cfg
	on := func(v bool) string {
		if v {
			return "On"
		}
		return "Off"
	}
	toggle := func(label string, v *bool) cfgRow {
		return cfgRow{label: label, value: func() string { return on(*v) }, toggle: func() { *v = !*v }}
	}
	choice := func(label string, p *string, allowed ...string) cfgRow {
		return cfgRow{label: label, value: func() string { return *p }, edit: func(v string) error {
			for _, x := range allowed {
				if strings.EqualFold(v, x) {
					*p = x
					return nil
				}
			}
			return fmt.Errorf("可选值：%s", strings.Join(allowed, " / "))
		}}
	}
	port := cfgRow{label: "监听端口", value: func() string { return fmt.Sprint(c.MixedInbound.Port) }, edit: func(s string) error {
		v, err := strconv.Atoi(s)
		if err != nil || v < 1 || v > 65535 {
			return fmt.Errorf("端口必须是 1–65535")
		}
		c.MixedInbound.Port = v
		return nil
	}}
	duration := func(label string, p *time.Duration) cfgRow {
		return cfgRow{label: label, value: func() string { return p.String() }, edit: func(s string) error {
			v, err := time.ParseDuration(s)
			if err != nil || v < time.Second {
				return fmt.Errorf("间隔至少 1 秒，例如 30s、10m")
			}
			*p = v
			return nil
		}}
	}
	m.cfgRows = []cfgRow{
		{label: "TUN · 网络"},
		toggle("链式 SOCKS 出口", &c.ChainEnabled),
		toggle("Tailscale 自动优化", &c.TailscaleOptimize),
		toggle("iCloud 同步节点源", &c.ICloud.Enabled),
		{label: "iCloud 目录", value: func() string { return c.ICloud.Directory }, edit: func(s string) error {
			if !filepath.IsAbs(s) {
				return fmt.Errorf("iCloud 路径必须是绝对路径")
			}
			c.ICloud.Directory = s
			return nil
		}},
		{label: "iCloud 同步文件", value: func() string { return strings.Join(c.ICloud.Files, ",") }, edit: func(s string) error {
			parts := splitNonEmpty(s, ",")
			if len(parts) == 0 {
				return fmt.Errorf("至少一个源文件")
			}
			for _, p := range parts {
				if p != filepath.Base(p) || p == "config.json" || p == "sakamoto.yaml" || strings.HasSuffix(p, ".srs") || strings.HasSuffix(p, ".log") {
					return fmt.Errorf("仅允许源文件名，不能同步生成文件或 API 密钥")
				}
			}
			c.ICloud.Files = parts
			return nil
		}},
		toggle("强制路由", &c.StrictRoute),
		choice("TUN 栈", &c.TunStack, "system", "gvisor", "mixed"),
		{label: "UDP · 隐私"},
		toggle("阻止 STUN / WebRTC", &c.BlockSTUN),
		toggle("阻止 QUIC (UDP 443)", &c.BlockQUIC),
		{label: "本地代理"},
		toggle("开启 HTTP/SOCKS", &c.MixedInbound.Enabled),
		toggle("系统代理（浏览器）", &c.SystemProxy.Enabled),
		{label: "网络服务", value: func() string { return c.SystemProxy.Service }, edit: func(s string) error {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("请输入 macOS 网络服务名")
			}
			c.SystemProxy.Service = s
			return nil
		}},
		toggle("允许局域网连接", &c.MixedInbound.AllowLAN),
		port,
		{label: "高级"},
		choice("日志等级", &c.LogLevel, "error", "warn", "info", "debug"),
		{label: "测速网址", value: func() string { return c.URLTest.URL }, edit: func(s string) error {
			v, err := url.Parse(s)
			if err != nil || (v.Scheme != "http" && v.Scheme != "https") || v.Host == "" {
				return fmt.Errorf("请输入有效 HTTP(S) 网址")
			}
			c.URLTest.URL = s
			return nil
		}},
		{label: "测速间隔", value: func() string { return c.URLTest.Interval }, edit: func(s string) error {
			v, err := time.ParseDuration(s)
			if err != nil || v < time.Second {
				return fmt.Errorf("请输入至少 1 秒的间隔")
			}
			c.URLTest.Interval = s
			return nil
		}},
		{label: "测速容差", value: func() string { return fmt.Sprint(c.URLTest.Tolerance) }, edit: func(s string) error {
			v, err := strconv.Atoi(s)
			if err != nil || v < 0 || v > 10000 {
				return fmt.Errorf("容差需要在 0–10000 ms")
			}
			c.URLTest.Tolerance = v
			return nil
		}},
		{label: "uTLS 指纹", value: func() string { return c.UTLSFingerprint }, edit: func(s string) error { c.UTLSFingerprint = s; return nil }},
		duration("健康检查间隔", &c.CheckInterval),
		{label: "回切阈值", value: func() string { return fmt.Sprint(c.RecoverAfter) }, edit: func(s string) error {
			v, err := strconv.Atoi(s)
			if err != nil || v < 1 || v > 20 {
				return fmt.Errorf("连续健康次数需 1–20")
			}
			c.RecoverAfter = v
			return nil
		}},
		{label: "订阅数", value: func() string { return fmt.Sprint(len(c.Subscriptions)) }},
		toggle("Reality 自动回落", &c.FallbackEnabled),
		{label: "自动回落链", value: func() string { return strings.Join(c.Fallbacks["MainProxy"], " → ") }, edit: func(s string) error {
			parts := splitNonEmpty(s, ",")
			if len(parts) == 0 {
				return fmt.Errorf("至少需要一个自动组")
			}
			c.Fallbacks["MainProxy"] = parts
			return nil
		}},
	}
}

func (m *model) rebuildRows() {
	m.rows = nil
	order := map[string]int{"MainProxy": 0, "RealityAuto": 1, "OthersAuto": 2, "ManualPick": 3}
	groups := append([]*daemon.Group(nil), m.groups...)
	sort.SliceStable(groups, func(i, j int) bool {
		a, ok := order[groups[i].Tag]
		if !ok {
			a = 10
		}
		b, ok := order[groups[j].Tag]
		if !ok {
			b = 10
		}
		if a != b {
			return a < b
		}
		return groups[i].Tag < groups[j].Tag
	})
	for _, g := range groups {
		m.rows = append(m.rows, row{group: g})
		for _, it := range g.Items {
			m.rows = append(m.rows, row{group: g, item: it})
		}
	}
	if m.cursor >= len(m.rows) {
		m.cursor = 0
	}
	m.seek(1)
}
func (m *model) seek(dir int) {
	if len(m.rows) == 0 {
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.rows[m.cursor].item != nil {
		return
	}
	for i := m.cursor; i >= 0 && i < len(m.rows); i += dir {
		if m.rows[i].item != nil {
			m.cursor = i
			return
		}
	}
}
func (m *model) move(dir int) {
	for i := m.cursor + dir; i >= 0 && i < len(m.rows); i += dir {
		if m.rows[i].item != nil {
			m.cursor = i
			return
		}
	}
}
