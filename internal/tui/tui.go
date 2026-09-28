// Package tui 提供以连接状态为中心的 sing-box 终端界面。
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	defer resp.Body.Close()
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
			if err != nil || !(v.Scheme == "http" || v.Scheme == "https") || v.Host == "" {
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

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = v.Width
		m.height = v.Height
	case tickMsg:
		if m.batch != nil {
			m.batch.expire(time.Now())
			m.refreshTestProgress()
		}
		return m, tea.Batch(tick(), queryService)
	case serviceMsg:
		previous, wasSR := m.serviceState, m.shadowrocket
		m.serviceErr = v.err
		m.shadowrocket = v.shadowrocket
		if v.err != nil {
			m.serviceState = "unavailable"
		} else {
			m.serviceState = v.state
		}
		if m.serviceState == "disconnected" {
			m.lastTest = nil
			m.networkState = ""
			m.netChecking = false
			m.conflictStopping = false
		}
		if m.serviceState == "connected" && m.shadowrocket && !m.conflictStopping {
			m.conflictStopping = true
			m.notice = "双 TUN 冲突：正在停用 sakamoto，保留 Shadowrocket VPN"
			return m, func() tea.Msg {
				_, err := svc.Send("disconnect")
				return actionMsg{text: "已停用 sakamoto；请先断开 Shadowrocket VPN 再连接", err: err}
			}
		}
		if m.serviceState == "connected" && !m.shadowrocket && (previous != "connected" || wasSR) && !m.netChecking {
			m.netChecking = true
			m.networkState = "检查中"
			expected := m.exitLabel
			return m, func() tea.Msg { return diagnoseNetwork(expected) }
		}
	case networkMsg:
		m.netChecking = false
		if v.err != nil {
			m.networkState = "不可用"
			m.notice = "TUN 已启动但系统流量不通：" + v.err.Error()
		} else {
			m.networkState = "可用 · 出口 " + v.ip
			m.notice = "网络已验证：出口 " + v.ip
		}
	case connectedMsg:
		m.conn = v.client
		if ms, err := m.conn.ClashModeStatus(context.Background()); err == nil {
			m.mode = ms.CurrentMode
		}
	case errMsg:
		m.conn = nil
		m.status = nil
		m.groups = offlineGroups(m.cfgPath)
		m.rebuildRows()
		if m.serviceState == "connected" {
			m.notice = "控制核心重连中"
		}
	case groupsMsg:
		m.groups = (*daemon.Groups)(v).Group
		m.rebuildRows()
		if m.batch != nil {
			m.batch.observe(m.groups)
			m.refreshTestProgress()
		}
	case statusMsg:
		m.status = (*daemon.Status)(v)
	case connsMsg:
		ev := (*daemon.ConnectionEvents)(v)
		if ev.Reset_ {
			m.conns = map[string]*daemon.Connection{}
		}
		for _, e := range ev.Events {
			if e.Type == daemon.ConnectionEventType_CONNECTION_EVENT_CLOSED {
				delete(m.conns, e.Id)
			} else if e.Connection != nil {
				m.conns[e.Id] = e.Connection
			}
		}
	case logMsg:
		l := (*daemon.Log)(v)
		if l.Reset_ {
			m.logs = nil
		}
		for _, x := range l.Messages {
			m.logs = append(m.logs, x.Message)
		}
		if len(m.logs) > 100 {
			m.logs = m.logs[len(m.logs)-100:]
		}
	case testDispatchMsg:
		if m.batch == v.batch {
			for tag := range v.errors {
				m.batch.results[tag] = -1
			}
			m.refreshTestProgress()
		}
	case importMsg:
		m.importBusy = false
		if v.err != nil {
			m.notice = "导入失败：" + v.err.Error()
			return m, nil
		}
		m.importing = false
		m.importKind = ""
		m.input = ""
		m.cfg.ConfPath = v.source
		m.exitLabel = exitFromConfig(m.cfgPath)
		if m.conn == nil {
			m.groups = offlineGroups(m.cfgPath)
			m.rebuildRows()
		}
		m.notice = "已导入并生成 sing-box 配置；断开再连接后应用"
		return m, nil
	case actionMsg:
		if v.err != nil {
			m.notice = v.err.Error()
		} else {
			m.notice = v.text
		}
		m.exitLabel = exitFromConfig(m.cfgPath)
		if m.conn == nil {
			m.groups = offlineGroups(m.cfgPath)
			m.rebuildRows()
		}
		return m, queryService
	case tea.MouseMsg:
		if m.importBusy {
			return m, nil
		}
		if v.Action == tea.MouseActionMotion {
			m.hoverX, m.hoverY = v.X, v.Y
		}
		if v.Action == tea.MouseActionPress && v.Button == tea.MouseButtonLeft {
			return m, m.click(v.X, v.Y)
		}
		if v.Action == tea.MouseActionPress && v.Button == tea.MouseButtonRight {
			m.rightClick(v.X, v.Y)
		}
		if v.Action == tea.MouseActionPress && v.Button == tea.MouseButtonWheelDown {
			m.scrollBy(3)
		}
		if v.Action == tea.MouseActionPress && v.Button == tea.MouseButtonWheelUp {
			m.scrollBy(-3)
		}
	case tea.KeyMsg:
		if m.importing {
			return m, m.handleImportInput(v)
		}
		if m.editIndex >= 0 {
			return m, m.handleInput(v)
		}
		switch v.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.menuRow, m.detailRow, m.configDetail = -1, -1, -1
			m.selectedConn = ""
			m.pendingDelete = ""
		case "tab":
			m.menuRow, m.detailRow = -1, -1
			m.page = (m.page + 1) % 4
		case "shift+tab":
			m.menuRow, m.detailRow = -1, -1
			m.page = (m.page + 3) % 4
		case "1", "2", "3", "4":
			m.menuRow, m.detailRow = -1, -1
			m.page = int(v.String()[0] - '1')
		case "j", "down":
			if m.page == settingsPage {
				m.settingsScroll++
				m.cfgCursor = min(len(m.cfgRows)-1, m.cfgCursor+1)
			} else {
				m.move(1)
			}
		case "k", "up":
			if m.page == settingsPage {
				m.cfgCursor = max(0, m.cfgCursor-1)
			} else {
				m.move(-1)
			}
		case "enter":
			if m.page == settingsPage {
				return m, m.activateSetting(m.cfgCursor)
			}
			if m.page == homePage {
				return m, m.selectCurrent()
			}
			if m.page == configPage {
				m.configDetail = 0
				return m, nil
			}
		case "c", " ":
			return m, m.toggleConnection()
		case "t":
			return m, m.testCurrent()
		case "u":
			return m, m.testAll()
		case "m":
			return m, m.cycleMode()
		case "g":
			return m, m.generate()
		case "a":
			if m.page == configPage {
				m.importing = true
				m.importKind = "conf"
				m.input = ""
				m.notice = "输入 Shadowrocket .conf URL 或本地路径"
				return m, nil
			}
		case "n":
			if m.page == configPage {
				m.importing = true
				m.importKind = "node"
				m.input = ""
				m.notice = "添加节点分享链接"
				return m, nil
			}
		case "d":
			if m.page == configPage && m.configDetail == 3 {
				return m, m.deleteSelectedNode()
			}
		case "e":
			if m.page == configPage {
				return m, m.editConfig()
			}
		case "i":
			if m.page == homePage {
				m.detailRow = m.cursor
				m.menuRow = -1
			}
		}
	}
	return m, nil
}

func (m *model) scrollBy(n int) {
	if m.page == homePage {
		m.scroll = max(0, min(max(0, len(m.rows)-1), m.scroll+n))
		// 保持光标在滚动窗口中，避免 View 将列表弹回原位置。
		for i := m.scroll; i < len(m.rows); i++ {
			if m.rows[i].item != nil {
				m.cursor = i
				break
			}
		}
	}
	if m.page == settingsPage {
		m.settingsScroll = max(0, min(max(0, len(m.cfgRows)-1), m.settingsScroll+n))
		m.cfgCursor = min(len(m.cfgRows)-1, m.settingsScroll)
	}
	if m.page == configPage && m.configDetail >= 0 {
		m.detailScroll = max(0, m.detailScroll+n)
	}
}
func (m *model) click(x, y int) tea.Cmd {
	for _, h := range m.hits {
		if y != h.y || x < h.x0 || x >= h.x1 {
			continue
		}
		switch h.action {
		case "tab":
			m.page = h.index
		case "connect":
			return m.toggleConnection()
		case "testall":
			return m.testAll()
		case "node":
			m.cursor = h.index
			m.menuRow = -1
			return m.selectCurrent()
		case "setting":
			m.cfgCursor = h.index
			return m.activateSetting(h.index)
		case "menu-use":
			m.cursor = m.menuRow
			m.menuRow = -1
			return m.selectCurrent()
		case "menu-test":
			m.cursor = m.menuRow
			m.menuRow = -1
			return m.testCurrent()
		case "menu-detail":
			m.detailRow = m.menuRow
			m.menuRow = -1
		case "back":
			m.detailRow, m.menuRow, m.configDetail = -1, -1, -1
			m.selectedConn = ""
		case "section":
			m.configDetail = h.index
			m.detailScroll = 0
		case "source":
			m.sourceCursor = h.index
			m.pendingDelete = ""
		case "node-source":
			m.nodeCursor = h.index
			m.pendingDelete = ""
		case "add-node":
			m.importing = true
			m.importKind = "node"
			m.input = ""
			m.revealInput = false
			m.notice = "粘贴节点分享链接（输入已隐藏）"
		case "edit-node":
			m.beginNodeEdit()
		case "delete-node":
			return m.deleteSelectedNode()
		case "edit-sub":
			m.beginSubscriptionEdit()
		case "add-sub":
			m.importing = true
			m.importKind = "subscription"
			m.input = ""
			m.notice = "输入订阅：名称|URL"
		case "delete-sub":
			return m.deleteSelectedSource()
		case "generate":
			return m.generate()
		case "import":
			m.importing = true
			m.importKind = "conf"
			m.input = ""
			m.notice = "输入 Shadowrocket .conf 的 HTTPS 地址或本地路径"
		case "edit-config":
			return m.editConfig()
		case "conn":
			if h.index >= 0 && h.index < len(m.dataIDs) {
				m.selectedConn = m.dataIDs[h.index]
			}
		case "close-conn":
			return m.closeSelectedConnection()
		}
		return nil
	}
	return nil
}
func (m *model) rightClick(x, y int) {
	if m.page != homePage {
		return
	}
	for _, h := range m.hits {
		if h.action == "node" && y == h.y && x >= h.x0 && x < h.x1 {
			m.menuRow = h.index
			m.detailRow = -1
			m.cursor = h.index
			return
		}
	}
	m.menuRow = -1
}
func (m *model) hovered(action string, index int) bool {
	for _, h := range m.prevHits {
		if h.action == action && h.index == index && m.hoverY == h.y && m.hoverX >= h.x0 && m.hoverX < h.x1 {
			return true
		}
	}
	return false
}
func (m *model) activateSetting(i int) tea.Cmd {
	if i < 0 || i >= len(m.cfgRows) {
		return nil
	}
	r := m.cfgRows[i]
	if r.toggle != nil {
		return m.toggleSetting(i)
	}
	if r.edit != nil {
		m.editIndex = i
		m.editing = r.label
		m.input = r.value()
	}
	return nil
}
func (m *model) handleImportInput(k tea.KeyMsg) tea.Cmd {
	if m.importBusy {
		return nil
	}
	switch k.String() {
	case "esc":
		m.importing = false
		m.importKind = ""
		m.input = ""
		m.notice = "已取消输入"
		return nil
	case "ctrl+u":
		m.input = ""
		return nil
	case "ctrl+r":
		if m.importKind == "node" || m.importKind == "node-edit" || m.importKind == "subscription" || m.importKind == "subscription-edit" {
			m.revealInput = !m.revealInput
		}
		return nil
	case "enter":
		source := strings.TrimSpace(m.input)
		if m.importKind == "node-edit" {
			if err := gen.ChangeNode(m.cfg.NodesFile, m.editNode.Line, m.editNode.Raw, source); err != nil {
				m.notice = "编辑失败：" + err.Error()
				return nil
			}
			m.importing = false
			m.importKind = ""
			m.input = ""
			m.notice = "节点已编辑；在 Config 点击更新生成后重连生效"
			return nil
		}
		if m.importKind == "node" {
			if source == "" {
				m.notice = "节点链接不能为空"
				return nil
			}
			if err := gen.ChangeNode(m.cfg.NodesFile, 0, "", source); err != nil {
				m.notice = "保存节点失败：" + err.Error()
				return nil
			}
			m.importing = false
			m.importKind = ""
			m.input = ""
			m.notice = "节点已添加；按 g 重新生成配置"
			return nil
		}
		if m.importKind == "subscription" || m.importKind == "subscription-edit" {
			parts := strings.SplitN(source, "|", 2)
			if len(parts) != 2 {
				m.notice = "格式：名称|订阅 URL"
				return nil
			}
			if err := gen.ValidSource(strings.TrimSpace(parts[1])); err != nil || !strings.HasPrefix(strings.TrimSpace(parts[1]), "http") {
				m.notice = "订阅必须是 HTTP(S) URL"
				return nil
			}
			if strings.TrimSpace(parts[0]) == "" {
				m.notice = "订阅名称不能为空"
				return nil
			}
			old := append([]config.SubSource(nil), m.cfg.Subscriptions...)
			entry := config.SubSource{Name: strings.TrimSpace(parts[0]), URL: strings.TrimSpace(parts[1])}
			if m.importKind == "subscription-edit" {
				if m.sourceCursor < 0 || m.sourceCursor >= len(m.cfg.Subscriptions) {
					m.notice = "订阅不存在"
					return nil
				}
				m.cfg.Subscriptions[m.sourceCursor] = entry
			} else {
				m.cfg.Subscriptions = append(m.cfg.Subscriptions, entry)
			}
			if err := m.cfg.Save(m.cfgPath); err != nil {
				m.cfg.Subscriptions = old
				m.notice = "保存订阅失败：" + err.Error()
				return nil
			}
			m.importing = false
			m.importKind = ""
			m.input = ""
			m.notice = "订阅已添加；按 g 生成节点"
			return nil
		}
		if strings.HasPrefix(source, "~/") {
			home, _ := os.UserHomeDir()
			source = filepath.Join(home, source[2:])
		}
		if err := gen.ValidSource(source); err != nil {
			m.notice = err.Error()
			return nil
		}
		m.importBusy = true
		m.notice = "正在拉取主配置和 include 文件…"
		path := m.cfgPath
		return func() tea.Msg { return importMsg{source: source, err: importSource(path, source)} }
	case "backspace":
		if len(m.input) > 0 {
			r := []rune(m.input)
			m.input = string(r[:len(r)-1])
		}
	default:
		if k.Type == tea.KeyRunes && len(m.input)+len(string(k.Runes)) <= 8192 {
			m.input += string(k.Runes)
		}
	}
	return nil
}

func splitNonEmpty(s, sep string) []string {
	var out []string
	for _, x := range strings.Split(s, sep) {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}
func (m *model) beginNodeEdit() {
	entries, err := gen.ReadNodes(m.cfg.NodesFile)
	if err != nil {
		m.notice = "读取节点失败：" + err.Error()
		return
	}
	if m.nodeCursor >= len(entries) {
		m.notice = "请先选择节点"
		return
	}
	m.editNode = entries[m.nodeCursor]
	m.importing = true
	m.importKind = "node-edit"
	m.input = m.editNode.Raw
	m.revealInput = false
	m.notice = "替换节点链接（默认隐藏；Ctrl+U 清空，Ctrl+R 显示）"
}
func (m *model) deleteSelectedNode() tea.Cmd {
	entries, err := gen.ReadNodes(m.cfg.NodesFile)
	if err != nil {
		m.notice = err.Error()
		return nil
	}
	if m.nodeCursor >= len(entries) {
		m.notice = "请先选择节点"
		return nil
	}
	e := entries[m.nodeCursor]
	key := fmt.Sprintf("node:%d:%s", e.Line, e.Raw)
	if m.pendingDelete != key {
		m.pendingDelete = key
		m.notice = "再次点击删除，确认移除节点：" + e.Tag
		return nil
	}
	m.pendingDelete = ""
	if err := gen.ChangeNode(m.cfg.NodesFile, e.Line, e.Raw, ""); err != nil {
		m.notice = err.Error()
	} else {
		m.notice = "已删除节点 " + e.Tag + "；更新生成后重连生效"
		if m.nodeCursor > 0 {
			m.nodeCursor--
		}
	}
	return nil
}
func (m *model) beginSubscriptionEdit() {
	if m.sourceCursor >= len(m.cfg.Subscriptions) {
		m.notice = "请先选择订阅"
		return
	}
	s := m.cfg.Subscriptions[m.sourceCursor]
	m.importing = true
	m.importKind = "subscription-edit"
	m.input = s.Name + "|" + s.URL
	m.notice = "修改订阅（名称|URL，链接可能包含敏感凭据）"
}
func (m *model) deleteSelectedSource() tea.Cmd {
	if m.sourceCursor >= len(m.cfg.Subscriptions) {
		m.notice = "请先选择订阅"
		return nil
	}
	s := m.cfg.Subscriptions[m.sourceCursor]
	key := "subscription:" + s.Name + ":" + s.URL
	if m.pendingDelete != key {
		m.pendingDelete = key
		m.notice = "再次点击删除，确认移除订阅：" + s.Name
		return nil
	}
	m.pendingDelete = ""
	old := append([]config.SubSource(nil), m.cfg.Subscriptions...)
	m.cfg.Subscriptions = append(m.cfg.Subscriptions[:m.sourceCursor], m.cfg.Subscriptions[m.sourceCursor+1:]...)
	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.cfg.Subscriptions = old
		m.notice = "删除失败：" + err.Error()
	} else {
		m.notice = "已删除订阅 " + s.Name + "；更新生成后生效"
		if m.sourceCursor > 0 {
			m.sourceCursor--
		}
	}
	return nil
}

func (m *model) handleInput(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.editIndex = -1
		m.editing = ""
		m.input = ""
		return nil
	case "enter":
		r := m.cfgRows[m.editIndex]
		old := r.value()
		if err := r.edit(strings.TrimSpace(m.input)); err != nil {
			m.notice = err.Error()
			return nil
		}
		if err := m.cfg.Save(m.cfgPath); err != nil {
			_ = r.edit(old)
			m.notice = "保存失败：" + err.Error()
			return nil
		}
		m.notice = "已保存；在 Config 更新配置并重新连接后生效"
		m.editIndex = -1
		m.editing = ""
		m.input = ""
	case "backspace":
		if len(m.input) > 0 {
			rs := []rune(m.input)
			m.input = string(rs[:len(rs)-1])
		}
	default:
		if k.Type == tea.KeyRunes && len([]rune(m.input))+len(k.Runes) < 250 {
			m.input += string(k.Runes)
		}
	}
	return nil
}
func (m *model) closeSelectedConnection() tea.Cmd {
	if m.conn == nil || m.selectedConn == "" {
		return nil
	}
	c, id := m.conn, m.selectedConn
	return func() tea.Msg {
		err := c.CloseConnection(context.Background(), id)
		return actionMsg{text: "连接已关闭", err: err}
	}
}

func (m *model) toggleConnection() tea.Cmd {
	cmd := "connect"
	if m.serviceState == "connected" {
		cmd = "disconnect"
	}
	if cmd == "connect" && (m.shadowrocket || shadowrocketVPNConnected()) {
		m.shadowrocket = true
		m.notice = "Shadowrocket VPN 仍已连接：先在 Shadowrocket 里断开，再点击连接"
		return nil
	}
	m.notice = "正在" + map[string]string{"connect": "连接…", "disconnect": "断开…"}[cmd]
	return func() tea.Msg { out, err := svc.Send(cmd); return actionMsg{text: strings.TrimSpace(out), err: err} }
}
func (m *model) selectCurrent() tea.Cmd {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	r := m.rows[m.cursor]
	if r.item == nil {
		return nil
	}
	if m.conn == nil {
		m.notice = "先连接，再选择节点"
		return nil
	}
	c, g, t := m.conn, r.group.Tag, r.item.Tag
	if r.group.Selectable {
		return func() tea.Msg {
			err := c.SelectOutbound(context.Background(), g, t)
			return actionMsg{text: "已选择 " + t, err: err}
		}
	}
	// 自动组内节点也能点：先在 ManualPick 选节点，再将 MainProxy 切到手动组。
	available := false
	for _, group := range m.groups {
		if group.Tag == "ManualPick" {
			for _, item := range group.Items {
				if item.Tag == t {
					available = true
				}
			}
		}
	}
	if !available {
		m.notice = "此节点不在手动选择组中"
		return nil
	}
	return func() tea.Msg {
		if err := c.SelectOutbound(context.Background(), "ManualPick", t); err != nil {
			return actionMsg{err: err}
		}
		err := c.SelectOutbound(context.Background(), "MainProxy", "ManualPick")
		return actionMsg{text: "已手动选择 " + t, err: err}
	}
}
func (m *model) testCurrent() tea.Cmd {
	if m.conn == nil || m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	r := m.rows[m.cursor]
	if r.item == nil {
		return nil
	}
	c, t := m.conn, r.item.Tag
	return func() tea.Msg {
		err := c.URLTest(context.Background(), t)
		return actionMsg{text: "测速中：" + t, err: err}
	}
}
func (m *model) refreshTestProgress() {
	if m.batch == nil {
		return
	}
	m.notice = m.batch.message()
	if m.batch.done() {
		m.lastTest = make(map[string]int32, len(m.batch.results))
		for tag, result := range m.batch.results {
			m.lastTest[tag] = result
		}
		m.batch = nil
	}
}
func (m *model) testAll() tea.Cmd {
	if m.conn == nil {
		m.notice = "先连接，再测速"
		return nil
	}
	if m.batch != nil {
		m.notice = "测速进行中：" + m.batch.message()
		return nil
	}
	batch := newTestBatch(m.groups, time.Now())
	if len(batch.tags) == 0 {
		m.notice = "没有可测速的代理节点"
		return nil
	}
	m.batch = batch
	m.notice = batch.message()
	c := m.conn
	return func() tea.Msg {
		errors := map[string]error{}
		for _, tag := range batch.tags {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			err := c.URLTest(ctx, tag)
			cancel()
			if err != nil {
				errors[tag] = err
			}
			time.Sleep(40 * time.Millisecond) // 避免同时冲击所有节点/测试站
		}
		return testDispatchMsg{batch: batch, errors: errors}
	}
}
func (m *model) cycleMode() tea.Cmd {
	if m.conn == nil {
		return nil
	}
	next := map[string]string{"rule": "global", "global": "direct", "direct": "rule"}[m.mode]
	if next == "" {
		next = "rule"
	}
	c := m.conn
	return func() tea.Msg {
		err := c.SetClashMode(context.Background(), next)
		return actionMsg{text: "模式：" + next, err: err}
	}
}
func (m *model) toggleSetting(i int) tea.Cmd {
	if i < 0 || i >= len(m.cfgRows) || m.cfgRows[i].toggle == nil {
		return nil
	}
	r := m.cfgRows[i]
	if r.label == "iCloud 同步节点源" && !m.cfg.ICloud.Enabled && m.pendingDelete != "icloud:confirm" {
		m.pendingDelete = "icloud:confirm"
		m.notice = "再次点击确认：将所选源文件（可能含节点密码）上传到 iCloud Drive"
		return nil
	}
	m.pendingDelete = ""
	r.toggle()
	if err := m.cfg.Save(m.cfgPath); err != nil {
		r.toggle()
		m.notice = "保存失败：" + err.Error()
		return nil
	}
	m.notice = "已保存 · 重新生成配置后，下次连接生效"
	return nil
}
func (m *model) generate() tea.Cmd {
	m.notice = "正在更新订阅并生成配置…"
	path := m.cfgPath
	return func() tea.Msg {
		cfg, err := config.Load(path)
		if err != nil {
			return actionMsg{err: err}
		}
		err = gen.Run(gen.Options{ConfPath: cfg.ConfPath, SRJSONPath: cfg.SRJSONPath, NodesFile: cfg.NodesFile, AllowHosts: strings.Join(cfg.AllowHosts, ","), Cfg: cfg, OutDir: filepath.Dir(path), Quiet: true})
		if err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: "配置已生成；断开再连接以应用"}
	}
}
func (m *model) editConfig() tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, m.cfgPath)
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return actionMsg{text: "配置文件已关闭；按 g 生成", err: err} })
}

func (m *model) activeNode() string {
	tag := "MainProxy"
	for i := 0; i < 5; i++ {
		found := false
		for _, g := range m.groups {
			if g.Tag == tag && g.Selected != "" {
				tag, found = g.Selected, true
				break
			}
		}
		if !found {
			break
		}
	}
	if tag == "MainProxy" {
		return "待连接"
	}
	if m.conn == nil {
		return "默认：" + tag + "（待连接）"
	}
	return tag
}

// offlineGroups 直接从生成的 config.json 读节点，核心关闭时仍显示可浏览的列表。
func offlineGroups(cfgPath string) []*daemon.Group {
	data, err := os.ReadFile(filepath.Join(filepath.Dir(cfgPath), "config.json"))
	if err != nil {
		return nil
	}
	var cfg struct {
		Outbounds []struct {
			Type      string   `json:"type"`
			Tag       string   `json:"tag"`
			Default   string   `json:"default"`
			Outbounds []string `json:"outbounds"`
		} `json:"outbounds"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return nil
	}
	types := map[string]string{}
	for _, o := range cfg.Outbounds {
		types[o.Tag] = o.Type
	}
	var groups []*daemon.Group
	for _, o := range cfg.Outbounds {
		if o.Type != "selector" && o.Type != "urltest" {
			continue
		}
		g := &daemon.Group{Tag: o.Tag, Type: o.Type, Selectable: o.Type == "selector", Selected: o.Default}
		if g.Selected == "" && o.Type == "selector" && len(o.Outbounds) > 0 {
			g.Selected = o.Outbounds[0]
		}
		for _, t := range o.Outbounds {
			g.Items = append(g.Items, &daemon.GroupItem{Tag: t, Type: types[t]})
		}
		groups = append(groups, g)
	}
	return groups
}

func exitFromConfig(cfgPath string) string {
	path := filepath.Join(filepath.Dir(cfgPath), "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "待生成"
	}
	var cfg struct {
		Route struct {
			Final string `json:"final"`
		} `json:"route"`
		Outbounds []struct {
			Tag        string `json:"tag"`
			Server     string `json:"server"`
			ServerPort int    `json:"server_port"`
		} `json:"outbounds"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return "配置错误"
	}
	for _, outbound := range cfg.Outbounds {
		if outbound.Tag == cfg.Route.Final && outbound.Server != "" {
			return fmt.Sprintf("%s:%d", outbound.Server, outbound.ServerPort)
		}
	}
	return cfg.Route.Final
}

func (m *model) addHit(x0, x1, y int, action string, index int) {
	m.hits = append(m.hits, hit{x0, x1, y, action, index})
}
func framedLine(text string, inner int) string {
	text = ansi.Truncate(text, inner, "…")
	return "│ " + text + strings.Repeat(" ", max(0, inner-lipgloss.Width(text))) + " │\n"
}
func padLine(text string, width int) string {
	text = ansi.Truncate(text, width, "…")
	return text + strings.Repeat(" ", max(0, width-lipgloss.Width(text)))
}
func (m *model) homeAside() []string {
	mode := "待连接"
	if m.mode != "" {
		mode = m.mode
	}
	selection := "自动"
	for _, g := range m.groups {
		if g.Tag == "MainProxy" && g.Selected != "" {
			selection = g.Selected
		}
	}
	latency := "未测试"
	for _, g := range m.groups {
		if g.Tag == selection {
			for _, i := range g.Items {
				if i.Tag == g.Selected && i.UrlTestDelay > 0 {
					latency = fmt.Sprintf("%d ms", i.UrlTestDelay)
				}
			}
		}
	}
	fallback := "关闭"
	if m.cfg.FallbackEnabled {
		fallback = strings.Join(m.cfg.Fallbacks["MainProxy"], " → ")
	}
	return []string{
		accent.Render("当前代理"), "", "入口  " + trunc(m.activeNode(), 26), "策略  " + selection, "延迟  " + latency,
		"", accent.Render("链式出口"), "", trunc(m.exitLabel, 30), "",
		accent.Render("网络"), "", "规则  " + mode, "回落  " + fallback, "Tailscale  " + map[bool]string{true: "自动优化", false: "关闭"}[m.cfg.TailscaleOptimize],
	}
}
func (m *model) View() string {
	m.prevHits, m.hits = m.hits, nil
	if m.width < 64 || m.height < 16 {
		return fmt.Sprintf("sakamoto · 终端至少需要 64×16（当前 %d×%d）\n", m.width, m.height)
	}
	w, inner := m.width, m.width-4
	var b strings.Builder
	b.WriteString("┌" + strings.Repeat("─", w-2) + "┐\n")
	stateLabel := "● 已断开"
	state := bad.Render(stateLabel)
	if m.serviceState == "connected" {
		stateLabel = "● TUN 已启动"
		state = good.Render(stateLabel)
	}
	if m.serviceState == "connected" && strings.HasPrefix(m.networkState, "可用") {
		stateLabel = "● 已验证可用"
		state = good.Render(stateLabel)
	}
	if m.serviceState == "connected" && m.networkState == "不可用" {
		stateLabel = "⚠ 网络不可用"
		state = bad.Render(stateLabel)
	}
	if m.serviceState == "unavailable" {
		stateLabel = "● 后台不可用"
		state = bad.Render(stateLabel)
	}
	if m.serviceState == "connected" && m.shadowrocket {
		stateLabel = "⚠ VPN 冲突"
		state = bad.Render(stateLabel)
	}
	label := "[ 连接 ]"
	if m.serviceState == "connected" {
		label = "[ 断开 ]"
	}
	button := good.Render(label)
	if m.hovered("connect", 0) {
		button = tab.Render(label)
	}
	action := "[ 全部测速 ]"
	speed := muted.Render(action)
	if m.hovered("testall", 0) {
		speed = focus.Render(action)
	}
	statusLine := " " + accent.Render("sakamoto") + "  " + state + "    " + button + "  " + speed
	if m.status != nil {
		statusLine += "    " + muted.Render(fmt.Sprintf("↑%s/s  ↓%s/s", fmtB(m.status.Uplink), fmtB(m.status.Downlink)))
	}
	b.WriteString(framedLine(statusLine, inner))
	x := 3 + lipgloss.Width("sakamoto") + 2 + lipgloss.Width(stateLabel) + 4
	m.addHit(x, x+lipgloss.Width(label), 1, "connect", 0)
	x += lipgloss.Width(label) + 2
	m.addHit(x, x+lipgloss.Width(action), 1, "testall", 0)
	path := "入口 " + m.activeNode()
	if m.cfg.ChainEnabled {
		path += "  →  SOCKS " + m.exitLabel
	}
	b.WriteString(framedLine(" "+muted.Render(path), inner))
	b.WriteString("└" + strings.Repeat("─", w-2) + "┘\n")
	// 清晰的 boxed tabs，选中项反色；命中区与字符单元格一致。
	x = 2
	b.WriteString("  ")
	for i, name := range tabs {
		text := "[ " + name + " ]"
		style := muted
		if m.page == i {
			style = tab
		} else if m.hovered("tab", i) {
			style = focus
		}
		b.WriteString(style.Render(text))
		m.addHit(x, x+lipgloss.Width(text), 4, "tab", i)
		x += lipgloss.Width(text) + 2
		b.WriteString("  ")
	}
	b.WriteByte('\n')
	title := tabs[m.page]
	b.WriteString("┌─ " + accent.Render(title) + " " + strings.Repeat("─", max(0, w-5-lipgloss.Width(title))) + "┐\n")
	var body strings.Builder
	startY := 6
	if m.page == homePage && m.menuRow >= 0 && m.menuRow < len(m.rows) && m.rows[m.menuRow].item != nil {
		name := trunc(m.rows[m.menuRow].item.Tag, 20)
		prefix := "操作 " + name + "  "
		body.WriteString(prefix)
		x = 2 + lipgloss.Width(prefix)
		for _, bt := range []struct{ text, action string }{{"[使用]", "menu-use"}, {"[测速]", "menu-test"}, {"[详情]", "menu-detail"}, {"[返回]", "back"}} {
			text := bt.text + " "
			body.WriteString(muted.Render(text))
			m.addHit(x, x+lipgloss.Width(bt.text), startY, bt.action, 0)
			x += lipgloss.Width(text)
		}
		body.WriteByte('\n')
		startY++
	}
	if m.notice != "" {
		body.WriteString(" " + muted.Render(m.notice) + "\n")
		startY++
	}
	if m.serviceState == "unavailable" {
		body.WriteString(" " + bad.Render("后台未安装；查看 README 的一次性安装步骤") + "\n")
		startY++
	}
	if m.shadowrocket {
		message := "Shadowrocket VPN 正在接管网络；要用 sakamoto，请先在 Shadowrocket 里断开。"
		if m.serviceState == "connected" {
			message = "双 TUN 冲突：先断开 sakamoto，再断开 Shadowrocket，然后重新连接 sakamoto。"
		}
		body.WriteString(" " + bad.Render(message) + "\n")
		startY++
	}
	bodyStart := len(m.hits)
	switch m.page {
	case homePage:
		if m.detailRow >= 0 {
			m.renderNodeDetail(&body, startY)
		} else {
			m.renderHome(&body, startY)
		}
	case configPage:
		m.renderConfig(&body, startY)
	case dataPage:
		m.renderData(&body, startY)
	case settingsPage:
		m.renderSettings(&body, startY)
	}
	// 内容点击区域在新布局中统一右移两格（边框 + 内边距）。
	visibleBottom := m.height - 2
	splitPane := m.page == homePage && m.menuRow < 0 && m.detailRow < 0 && w >= 100
	leftWidth := inner - 38
	for i := bodyStart; i < len(m.hits); i++ {
		m.hits[i].x0 += 2
		m.hits[i].x1 += 2
		if splitPane && m.hits[i].action == "node" {
			m.hits[i].x1 = min(m.hits[i].x1, 2+leftWidth)
		}
	}
	filtered := m.hits[:0]
	for _, h := range m.hits {
		if h.y < visibleBottom && h.x0 < w {
			filtered = append(filtered, h)
		}
	}
	m.hits = filtered
	content := strings.Split(strings.TrimSuffix(body.String(), "\n"), "\n")
	count := m.height - 8
	var aside []string
	if splitPane {
		aside = m.homeAside()
	}
	for i := 0; i < count; i++ {
		line := ""
		if i < len(content) {
			line = content[i]
		}
		if splitPane {
			right := ""
			if i < len(aside) {
				right = aside[i]
			}
			line = padLine(line, leftWidth) + " │ " + padLine(right, 35)
		}
		b.WriteString(framedLine(line, inner))
	}
	b.WriteString("└" + strings.Repeat("─", w-2) + "┘\n")
	foot := " 鼠标点击 · 滚轮滚动   Tab 切页   Esc 返回   ? 帮助   q 退出"
	b.WriteString(ansi.Truncate(muted.Render(foot), w, "…"))
	return b.String()
}
func (m *model) renderHome(b *strings.Builder, startY int) {
	if len(m.rows) == 0 {
		b.WriteString(" 暂无节点。请检查订阅，或在 Config 页生成配置。\n")
		return
	}
	available := max(3, m.height-startY-4)
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	m.scroll = max(0, min(m.scroll, len(m.rows)-1))
	// 组与组之间留一行；光标滚动计算同时计入这行间距。
	lineCount := func(from, to int) int {
		n := 0
		for i := from; i <= to && i < len(m.rows); i++ {
			if i > from && m.rows[i].item == nil {
				n++
			}
			n++
		}
		return n
	}
	for m.scroll < m.cursor && lineCount(m.scroll, m.cursor) > available {
		m.scroll++
	}
	y := startY
	for i := m.scroll; i < len(m.rows) && y < startY+available; i++ {
		r := m.rows[i]
		if r.item == nil {
			if i > m.scroll {
				if y+2 > startY+available {
					break
				}
				b.WriteByte('\n')
				y++
			}
			fmt.Fprintf(b, " %s\n", accent.Render(r.group.Tag+"   "+r.group.Selected))
			y++
			continue
		}
		marker := " "
		if r.group.Selected == r.item.Tag {
			marker = "●"
		}
		name := r.item.Tag
		if lipgloss.Width(name) > 38 {
			name = trunc(name, 37)
		}
		latency := "待测"
		if r.item.UrlTestDelay > 0 {
			latency = fmt.Sprintf("可达 %dms", r.item.UrlTestDelay)
		}
		if m.lastTest[r.item.Tag] < 0 {
			latency = "失败/超时"
		}
		if m.batch != nil {
			if v, ok := m.batch.results[r.item.Tag]; ok {
				if v < 0 {
					latency = "失败/超时"
				} else {
					latency = fmt.Sprintf("可达 %dms", v)
				}
			} else if _, ok := m.batch.baseline[r.item.Tag]; ok {
				latency = "测试中…"
			}
		}
		line := fmt.Sprintf(" %s %-38s %-10s %s", marker, name, r.item.Type, latency)
		if i == m.cursor || m.hovered("node", i) {
			line = focus.Render(">" + line)
		} else if r.item.UrlTestDelay == 0 {
			line = muted.Render(" " + line)
		} else {
			line = " " + line
		}
		b.WriteString(line + "\n")
		m.addHit(0, max(m.width, 80), y, "node", i)
		y++
	}
	fmt.Fprintf(b, "\n %s\n", muted.Render(fmt.Sprintf("节点 %d/%d · ● 仅表示已选中 · 测速可达≠流量已接管 · 滚轮浏览", m.cursor+1, len(m.rows))))
}

func (m *model) renderNodeDetail(b *strings.Builder, startY int) {
	if m.detailRow < 0 || m.detailRow >= len(m.rows) || m.rows[m.detailRow].item == nil {
		m.detailRow = -1
		return
	}
	r := m.rows[m.detailRow]
	name := r.item.Tag
	fmt.Fprintf(b, " %s\n\n", accent.Render(name))
	fmt.Fprintf(b, " 分组        %s\n 协议        %s\n", r.group.Tag, r.item.Type)
	delay := "未测试"
	if r.item.UrlTestDelay > 0 {
		delay = fmt.Sprintf("%d ms", r.item.UrlTestDelay)
	}
	fmt.Fprintf(b, " 延迟        %s\n", delay)
	// 只显示服务地址，不泄露 UUID/口令。
	data, _ := os.ReadFile(filepath.Join(filepath.Dir(m.cfgPath), "config.json"))
	var cfg struct {
		Outbounds []struct {
			Tag, Server string
			ServerPort  int `json:"server_port"`
		} `json:"outbounds"`
	}
	server := "—"
	if json.Unmarshal(data, &cfg) == nil {
		for _, o := range cfg.Outbounds {
			if o.Tag == name && o.Server != "" {
				server = fmt.Sprintf("%s:%d", o.Server, o.ServerPort)
				break
			}
		}
	}
	fmt.Fprintf(b, " 服务器      %s\n", server)
	b.WriteString("\n [ 返回 ]")
	m.addHit(1, lipgloss.Width(" [ 返回 ]")+1, startY+7, "back", 0)
	b.WriteString("\n")
}

var configSections = []string{"常规设置", "分流规则", "代理组", "节点与订阅", "DNS", "迁移限制"}

func (m *model) sectionDetails(i int) []string {
	data, _ := os.ReadFile(filepath.Join(filepath.Dir(m.cfgPath), "config.json"))
	var c struct {
		Route struct {
			Final   string `json:"final"`
			RuleSet []struct {
				Tag string `json:"tag"`
			} `json:"rule_set"`
		} `json:"route"`
		DNS struct {
			Servers []struct{ Tag, Type, Server string } `json:"servers"`
		} `json:"dns"`
		Outbounds []struct{ Tag, Type string } `json:"outbounds"`
	}
	_ = json.Unmarshal(data, &c)
	switch i {
	case 0:
		var result []string
		confPath := m.cfg.ConfPath
		if u, err := url.Parse(confPath); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			confPath = filepath.Join(filepath.Dir(m.cfgPath), "imports", "macOS.conf")
		}
		f, err := os.ReadFile(confPath)
		if err != nil {
			return []string{"源配置尚未缓存；请先完成导入或更新"}
		}
		section := ""
		for _, line := range strings.Split(string(f), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[") {
				section = line
				continue
			}
			if section == "[General]" && line != "" && !strings.HasPrefix(line, "#") {
				// 避免过长的列表淹没界面；完整值可在设置文件中查阅。
				result = append(result, trunc(line, max(25, m.width-5)))
			}
		}
		return result
	case 1:
		result := []string{"最终策略：" + c.Route.Final}
		for _, r := range c.Route.RuleSet {
			result = append(result, "规则集："+r.Tag)
		}
		return result
	case 2:
		var result []string
		for _, o := range c.Outbounds {
			if o.Type == "selector" || o.Type == "urltest" {
				result = append(result, o.Tag+" · "+o.Type)
			}
		}
		return result
	case 3:
		result := []string{"手动节点文件：" + m.cfg.NodesFile}
		for _, s := range m.cfg.Subscriptions {
			result = append(result, "订阅："+s.Name)
		}
		return result
	case 4:
		var result []string
		for _, s := range c.DNS.Servers {
			result = append(result, s.Tag+" · "+s.Type+" · "+s.Server)
		}
		return result
	default:
		return []string{"HTTP URL/Header/Body 重写、MITM 与 Spotify 脚本无 sing-box 等价功能。", "生成配置时会报告这些差异，不会悄悄丢弃。"}
	}
}
func (m *model) renderConfig(b *strings.Builder, startY int) {
	if m.importing {
		title := "导入 Shadowrocket .conf"
		hint := "自动合并 include 并生成 sing-box 配置"
		switch m.importKind {
		case "node":
			title = "添加节点"
			hint = "粘贴一个分享链接；凭据默认隐藏"
		case "node-edit":
			title = "编辑节点"
			hint = "Ctrl+U 清空后粘贴新链接；Ctrl+R 临时显示"
		case "subscription":
			title = "添加订阅"
			hint = "格式：名称|HTTPS URL"
		case "subscription-edit":
			title = "编辑订阅"
			hint = "格式：名称|HTTPS URL（Ctrl+R 临时显示）"
		}
		b.WriteString(" " + accent.Render(title) + "\n\n")
		display := m.input
		if (m.importKind == "node" || m.importKind == "node-edit") && !m.revealInput {
			display = fmt.Sprintf("●●●（%d 字符）", len([]rune(m.input)))
		} else if (m.importKind == "subscription" || m.importKind == "subscription-edit") && !m.revealInput {
			if name, link, ok := strings.Cut(display, "|"); ok {
				display = name + "|" + fmt.Sprintf("●●●（%d 字符）", len([]rune(link)))
			}
		} else if u, err := url.Parse(display); err == nil && u.RawQuery != "" {
			display = strings.SplitN(display, "?", 2)[0] + "?…"
		}
		if display == "" {
			display = "粘贴地址或链接"
		}
		b.WriteString(" 内容  " + trunc(display, max(25, m.width-12)) + "▏\n\n")
		b.WriteString(" " + hint + "\n Enter 确认   Esc 取消\n")
		return
	}
	// Shadowrocket 的编译产物更新后，旧明文快照不能伪装成最新 macOS.conf。
	if warning := staleShadowrocketSource(m.cfg.ConfPath); warning != "" {
		b.WriteString(" " + bad.Render(warning) + "\n")
		startY++
	}
	if m.configDetail == 3 {
		m.renderSources(b, startY)
		return
	}
	if m.configDetail >= 0 && m.configDetail < len(configSections) {
		fmt.Fprintf(b, " %s\n", accent.Render(configSections[m.configDetail]))
		items := m.sectionDetails(m.configDetail)
		available := max(2, m.height-startY-4)
		m.detailScroll = min(m.detailScroll, max(0, len(items)-available))
		for j := m.detailScroll; j < len(items) && j < m.detailScroll+available; j++ {
			fmt.Fprintf(b, "   %s\n", muted.Render(trunc(items[j], max(25, m.width-5))))
		}
		y := startY + 1 + min(available, max(0, len(items)-m.detailScroll)) + 1
		b.WriteString("\n [ 返回配置 ]\n")
		m.addHit(1, 1+lipgloss.Width("[ 返回配置 ]"), y, "back", 0)
		return
	}
	b.WriteString(" 配置\n")
	for i, name := range configSections {
		y := startY + 1 + i
		line := "   " + name + "  ›"
		if m.hovered("section", i) {
			line = focus.Render(line)
		}
		b.WriteString(line + "\n")
		m.addHit(0, max(40, m.width), y, "section", i)
	}
	y := startY + 1 + len(configSections) + 1
	b.WriteString("\n 订阅来源\n")
	y++
	if len(m.cfg.Subscriptions) == 0 {
		b.WriteString("   （无订阅；节点保留在 nodes.txt）\n")
		y++
	} else {
		for i, s := range m.cfg.Subscriptions {
			name := s.Name
			if name == "" {
				name = "未命名"
			}
			line := fmt.Sprintf("   %s  %s", name, redactURL(s.URL))
			if i == m.sourceCursor {
				line = focus.Render(">" + line)
			}
			b.WriteString(line + "\n")
			m.addHit(0, max(60, m.width), y, "source", i)
			y++
		}
	}
	b.WriteString("\n")
	x := 1
	for _, bt := range []struct{ text, action string }{{"[ 导入配置 ]", "import"}, {"[ 添加订阅 ]", "add-sub"}, {"[ 删除订阅 ]", "delete-sub"}, {"[ 更新生成 ]", "generate"}, {"[ 编辑设置 ]", "edit-config"}} {
		b.WriteString(" " + muted.Render(bt.text))
		m.addHit(x, x+lipgloss.Width(bt.text), y, bt.action, 0)
		x += lipgloss.Width(bt.text) + 2
	}
}
func (m *model) renderSources(b *strings.Builder, startY int) {
	b.WriteString(" " + accent.Render("节点与订阅") + "  [ 返回 ]\n")
	m.addHit(2+lipgloss.Width("节点与订阅")+2, 2+lipgloss.Width("节点与订阅")+2+lipgloss.Width("[ 返回 ]"), startY, "back", 0)
	y := startY + 1
	groups := [][]struct{ text, action string }{
		{{"[ 添加节点 ]", "add-node"}, {"[ 编辑节点 ]", "edit-node"}, {"[ 删除节点 ]", "delete-node"}},
		{{"[ 添加订阅 ]", "add-sub"}, {"[ 编辑订阅 ]", "edit-sub"}, {"[ 删除订阅 ]", "delete-sub"}},
	}
	for _, buttons := range groups {
		x := 1
		for _, btn := range buttons {
			b.WriteString(" " + muted.Render(btn.text))
			m.addHit(x, x+lipgloss.Width(btn.text), y, btn.action, 0)
			x += lipgloss.Width(btn.text) + 2
		}
		b.WriteByte('\n')
		y++
	}
	b.WriteString("\n 订阅\n")
	y += 2
	if len(m.cfg.Subscriptions) == 0 {
		b.WriteString("   无（手动节点仍可用）\n")
		y++
	} else {
		for i, s := range m.cfg.Subscriptions {
			if y >= m.height-5 {
				break
			}
			line := fmt.Sprintf("   %-17s %s", s.Name, redactURL(s.URL))
			if i == m.sourceCursor || m.hovered("source", i) {
				line = focus.Render(">" + line)
			}
			b.WriteString(line + "\n")
			m.addHit(0, max(40, m.width), y, "source", i)
			y++
		}
	}
	b.WriteString("\n 手动节点（点击后用上方按钮编辑/删除）\n")
	y += 2
	entries, err := gen.ReadNodes(m.cfg.NodesFile)
	if err != nil {
		b.WriteString("   读取失败：" + err.Error() + "\n")
		return
	}
	if len(entries) == 0 {
		b.WriteString("   暂无节点\n")
		return
	}
	available := max(1, m.height-2-y)
	m.detailScroll = min(max(0, m.detailScroll), max(0, len(entries)-available))
	if m.nodeCursor < m.detailScroll {
		m.detailScroll = m.nodeCursor
	}
	if m.nodeCursor >= m.detailScroll+available {
		m.detailScroll = m.nodeCursor - available + 1
	}
	for i := m.detailScroll; i < len(entries) && y < m.height-2; i++ {
		e := entries[i]
		line := fmt.Sprintf("   %-44s %-12s", trunc(e.Tag, 43), e.Type)
		if i == m.nodeCursor || m.hovered("node-source", i) {
			line = focus.Render(">" + line)
		}
		b.WriteString(line + "\n")
		m.addHit(0, max(45, m.width), y, "node-source", i)
		y++
	}
}

func redactURL(s string) string {
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host + "/…"
	}
	return "（本地来源）"
}
func staleShadowrocketSource(confPath string) string {
	home, _ := os.UserHomeDir()
	group := filepath.Join(home, "Library", "Group Containers", "group.com.liguangming.Shadowrocket")
	for _, pair := range [][2]string{
		{confPath, filepath.Join(group, "macOS.db.rule")},
		{filepath.Join(filepath.Dir(confPath), "sr_top500_banlist_ad_5_25.conf"), filepath.Join(group, "sr_top500_banlist_ad_5_25.db.rule")},
	} {
		src, err1 := os.Stat(pair[0])
		active, err2 := os.Stat(pair[1])
		if err1 == nil && err2 == nil && active.ModTime().After(src.ModTime().Add(time.Minute)) {
			return "Shadowrocket 配置已更新；请导出 macOS.conf 和 include 的广告规则"
		}
	}
	return ""
}

func (m *model) renderData(b *strings.Builder, startY int) {
	if m.status != nil {
		fmt.Fprintf(b, " ↑ %s（%s/s）    ↓ %s（%s/s）    连接 %d\n", fmtB(m.status.UplinkTotal), fmtB(m.status.Uplink), fmtB(m.status.DownlinkTotal), fmtB(m.status.Downlink), len(m.conns))
	} else {
		b.WriteString(" 连接后可查看实时用量和连接。\n")
	}
	if m.selectedConn != "" {
		c := m.conns[m.selectedConn]
		if c == nil {
			m.selectedConn = ""
		} else {
			fmt.Fprintf(b, "\n %s\n 目标：%s\n 来源：%s\n 策略：%s\n 链路：%s\n", accent.Render("连接详情"), c.Destination, c.Source, c.Outbound, strings.Join(c.ChainList, " → "))
			b.WriteString("\n [ 返回 ]  [ 关闭此连接 ]\n")
			m.addHit(1, 1+lipgloss.Width("[ 返回 ]"), startY+8, "back", 0)
			x := 1 + lipgloss.Width("[ 返回 ]  ")
			m.addHit(x, x+lipgloss.Width("[ 关闭此连接 ]"), startY+8, "close-conn", 0)
			return
		}
	}
	b.WriteString("\n 最近连接（点击查看详情）\n")
	m.dataIDs = m.dataIDs[:0]
	for id := range m.conns {
		m.dataIDs = append(m.dataIDs, id)
	}
	sort.Slice(m.dataIDs, func(i, j int) bool { return m.conns[m.dataIDs[i]].CreatedAt > m.conns[m.dataIDs[j]].CreatedAt })
	maxConn := min(len(m.dataIDs), max(2, min(8, m.height-startY-12)))
	for i, id := range m.dataIDs[:maxConn] {
		c := m.conns[id]
		name := c.Domain
		if name == "" {
			name = c.Destination
		}
		line := fmt.Sprintf("   %-38s → %s", trunc(name, 38), c.Outbound)
		if m.hovered("conn", i) {
			line = focus.Render(line)
		}
		b.WriteString(line + "\n")
		m.addHit(0, max(50, m.width), startY+3+i, "conn", i)
	}
	b.WriteString("\n 核心日志\n")
	start := max(0, len(m.logs)-min(6, max(2, m.height-startY-maxConn-6)))
	for _, line := range m.logs[start:] {
		fmt.Fprintf(b, "   %s\n", muted.Render(trunc(line, max(20, m.width-5))))
	}
}
func (m *model) renderSettings(b *strings.Builder, startY int) {
	available := max(3, m.height-startY-4)
	if m.cfgCursor < m.settingsScroll {
		m.settingsScroll = m.cfgCursor
	}
	if m.cfgCursor >= m.settingsScroll+available {
		m.settingsScroll = m.cfgCursor - available + 1
	}
	m.settingsScroll = max(0, min(m.settingsScroll, max(0, len(m.cfgRows)-available)))
	end := min(len(m.cfgRows), m.settingsScroll+available)
	for i := m.settingsScroll; i < end; i++ {
		r := m.cfgRows[i]
		y := startY + i - m.settingsScroll
		if r.value == nil {
			b.WriteString(" " + accent.Render(r.label) + "\n")
			continue
		}
		indicator := ""
		if r.toggle != nil {
			indicator = " [开关]"
		} else if r.edit != nil {
			indicator = " [编辑]"
		}
		line := fmt.Sprintf(" %-31s %s%s", r.label, r.value(), indicator)
		if i == m.cfgCursor || m.hovered("setting", i) {
			line = focus.Render(">" + line)
		}
		b.WriteString(line + "\n")
		if r.toggle != nil || r.edit != nil {
			m.addHit(0, max(m.width, 80), y, "setting", i)
		}
	}
	if m.editIndex >= 0 {
		fmt.Fprintf(b, "\n %s：%s▏\n Enter 保存 · Esc 取消\n", m.editing, m.input)
	} else {
		b.WriteString("\n 点击或 Enter 修改 · 滚轮浏览 · Config 更新后重连生效\n")
	}
}
func fmtB(v int64) string {
	if v >= 1<<20 {
		return fmt.Sprintf("%.1fM", float64(v)/(1<<20))
	}
	if v >= 1<<10 {
		return fmt.Sprintf("%.1fK", float64(v)/(1<<10))
	}
	return fmt.Sprintf("%dB", v)
}
func trunc(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) >= n {
		return string(r[:n-1]) + "…"
	}
	return s
}
