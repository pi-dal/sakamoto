// Package tui provides a connection-centered terminal UI for sing-box.
package tui

import (
	"context"
	"fmt"
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
	aboutPage
)

var tabs = []string{"Home", "Config", "Data", "Settings", "About"}

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
	path string // observed usable path, never inferred from a server IP
	err  error
}
type actionMsg struct {
	text       string
	err        error
	apiRotated bool
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
	nextNetworkProbe                                time.Time
	networkProbeFailures                            int
	exitLabel                                       string
	serviceErr                                      error
	hits                                            []hit // Rebuilt each view; mouse targets visible elements only.
	prevHits                                        []hit
	hoverX, hoverY                                  int
	menuRow, detailRow                              int // -1 means closed; Home menu and node details.
	configDetail, detailScroll                      int // -1 means Config list; >=0 selects a section.
	aboutCopyright                                  bool
	aboutScroll                                     int
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
	go pump(ctx, path, p)
	_, err := p.Run()
	return err
}

func pump(ctx context.Context, path string, p *tea.Program) {
	for ctx.Err() == nil {
		cfg, err := config.Load(path)
		var c *sbclient.Client
		if err == nil {
			c, err = sbclient.Dial(ctx, cfg.API.URL, cfg.API.Secret)
		}
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
			p.Send(errMsg(fmt.Errorf("core disconnected; reconnecting")))
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
			return fmt.Errorf("allowed values: %s", strings.Join(allowed, " / "))
		}}
	}
	port := cfgRow{label: "Listen port", value: func() string { return fmt.Sprint(c.MixedInbound.Port) }, edit: func(s string) error {
		v, err := strconv.Atoi(s)
		if err != nil || v < 1 || v > 65535 {
			return fmt.Errorf("port must be between 1 and 65535")
		}
		c.MixedInbound.Port = v
		return nil
	}}
	duration := func(label string, p *time.Duration) cfgRow {
		return cfgRow{label: label, value: func() string { return p.String() }, edit: func(s string) error {
			v, err := time.ParseDuration(s)
			if err != nil || v < time.Second {
				return fmt.Errorf("interval must be at least 1 second (e.g. 30s or 10m)")
			}
			*p = v
			return nil
		}}
	}
	m.cfgRows = []cfgRow{
		{label: "TUN · Network"},
		toggle("Chain SOCKS exit", &c.ChainEnabled),
		toggle("Optimize Tailscale", &c.TailscaleOptimize),
		toggle("Sync node sources to iCloud", &c.ICloud.Enabled),
		{label: "iCloud directory", value: func() string { return c.ICloud.Directory }, edit: func(s string) error {
			if !filepath.IsAbs(s) {
				return fmt.Errorf("iCloud path must be absolute")
			}
			c.ICloud.Directory = s
			return nil
		}},
		{label: "iCloud source files", value: func() string { return strings.Join(c.ICloud.Files, ",") }, edit: func(s string) error {
			parts := splitNonEmpty(s, ",")
			if len(parts) == 0 {
				return fmt.Errorf("at least one source filename is required")
			}
			for _, p := range parts {
				if p != filepath.Base(p) || p == "config.json" || p == "sakamoto.yaml" || p == "auto-proxy.json" || p == "api-rotation.pending.json" || p == "proxy-restore.json" || strings.HasSuffix(p, ".srs") || strings.HasSuffix(p, ".log") {
					return fmt.Errorf("only source filenames are allowed; generated files and API keys cannot be synced")
				}
			}
			c.ICloud.Files = parts
			return nil
		}},
		toggle("Strict routing", &c.StrictRoute),
		choice("TUN stack", &c.TunStack, "system", "gvisor", "mixed"),
		{label: "Experimental · Unmatched traffic"},
		choice("Unmatched policy", &c.Experiment.Mode, "off", "on", "auto"),
		{label: "Direct failure threshold", value: func() string { return fmt.Sprint(c.Experiment.Threshold) }, edit: func(s string) error {
			v, err := strconv.Atoi(s)
			if err != nil || v < 1 || v > 20 {
				return fmt.Errorf("failure threshold must be between 1 and 20")
			}
			c.Experiment.Threshold = v
			return nil
		}},
		{label: "UDP · Privacy"},
		toggle("Block STUN / WebRTC", &c.BlockSTUN),
		toggle("Block QUIC (UDP 443)", &c.BlockQUIC),
		{label: "Local proxy"},
		toggle("Enable HTTP/SOCKS", &c.MixedInbound.Enabled),
		toggle("System proxy (browser)", &c.SystemProxy.Enabled),
		{label: "Network service", value: func() string { return c.SystemProxy.Service }, edit: func(s string) error {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("enter a macOS network service name")
			}
			c.SystemProxy.Service = s
			return nil
		}},
		toggle("Allow LAN clients", &c.MixedInbound.AllowLAN),
		port,
		{label: "Advanced"},
		choice("Log level", &c.LogLevel, "error", "warn", "info", "debug"),
		{label: "Latency test URL", value: func() string { return c.URLTest.URL }, edit: func(s string) error {
			v, err := url.Parse(s)
			if err != nil || (v.Scheme != "http" && v.Scheme != "https") || v.Host == "" {
				return fmt.Errorf("enter a valid HTTP(S) URL")
			}
			c.URLTest.URL = s
			return nil
		}},
		{label: "Test interval", value: func() string { return c.URLTest.Interval }, edit: func(s string) error {
			v, err := time.ParseDuration(s)
			if err != nil || v < time.Second {
				return fmt.Errorf("enter an interval of at least 1 second")
			}
			c.URLTest.Interval = s
			return nil
		}},
		{label: "Latency tolerance", value: func() string { return fmt.Sprint(c.URLTest.Tolerance) }, edit: func(s string) error {
			v, err := strconv.Atoi(s)
			if err != nil || v < 0 || v > 10000 {
				return fmt.Errorf("tolerance must be between 0 and 10000 ms")
			}
			c.URLTest.Tolerance = v
			return nil
		}},
		{label: "uTLS fingerprint", value: func() string { return c.UTLSFingerprint }, edit: func(s string) error { c.UTLSFingerprint = s; return nil }},
		duration("Health check interval", &c.CheckInterval),
		{label: "Recovery threshold", value: func() string { return fmt.Sprint(c.RecoverAfter) }, edit: func(s string) error {
			v, err := strconv.Atoi(s)
			if err != nil || v < 1 || v > 20 {
				return fmt.Errorf("consecutive healthy checks must be between 1 and 20")
			}
			c.RecoverAfter = v
			return nil
		}},
		{label: "Subscriptions", value: func() string { return fmt.Sprint(len(c.Subscriptions)) }},
		toggle("Automatic fallback", &c.FallbackEnabled),
		{label: "Fallback chain", value: func() string { return strings.Join(c.Fallbacks["MainProxy"], " → ") }, edit: func(s string) error {
			parts := splitNonEmpty(s, ",")
			if len(parts) == 0 {
				return fmt.Errorf("at least one automatic group is required")
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
