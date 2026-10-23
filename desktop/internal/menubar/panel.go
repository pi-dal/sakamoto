package menubar

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/pi-dal/sakamoto/internal/core"
	"github.com/pi-dal/sakamoto/internal/desktop"
	"github.com/sagernet/sing-box/daemon"
)

const (
	PanelWidth  = 400
	PanelHeight = 640
)

// Backend is shared by the live controller and an explicitly labeled demo.
type Backend interface {
	Snapshot() desktop.Snapshot
	Changes() <-chan struct{}
	SetVisible(bool)
	Connect(context.Context) error
	Disconnect(context.Context) error
	SetMode(context.Context, string) error
	SelectNode(context.Context, string, string) error
	Test(context.Context, string) error
	CheckHealth(context.Context) error
	Repair(context.Context, bool) error
	RecoverRoute(context.Context) error
}

type Actions struct {
	Dispatch      func(func(context.Context) error)
	OpenTUI       func() error
	OpenRecovery  func() error
	RevealConfig  func() error
	SetAppearance func(string) error
	SetLogin      func(bool) error
	Quit          func()
	Appearance    string
	OpenAtLogin   bool
	Demo          bool
}

// Panel owns view state only. Explicit actions and the independent service can
// outlive it; live observations are scoped to visibility.
type Panel struct {
	backend        Backend
	actions        Actions
	snapshot       desktop.Snapshot
	settings       bool
	recovery       bool
	confirmRestart bool
	query, group   string
	listFilter     string
	list           ui.ListState
	appearance     string
	openAtLogin    bool
	localNotice    string
}

func NewPanel(backend Backend, actions Actions) *Panel {
	p := &Panel{backend: backend, actions: actions, appearance: actions.Appearance, openAtLogin: actions.OpenAtLogin}
	if p.appearance == "" {
		p.appearance = "System"
	}
	p.Refresh()
	return p
}

func (p *Panel) Refresh() {
	p.snapshot = p.backend.Snapshot()
	found := false
	for _, g := range p.snapshot.Groups {
		if g.Tag == p.group {
			found = true
		}
	}
	if found {
		return
	}
	p.group = ""
	for _, g := range p.snapshot.Groups {
		if g.Tag == "MainProxy" {
			p.group = g.Selected
		}
	}
	for _, g := range p.snapshot.Groups {
		if g.Tag == p.group {
			return
		}
	}
	if len(p.snapshot.Groups) > 0 {
		p.group = p.snapshot.Groups[0].Tag
	}
}

func (p *Panel) submitAction(action func(context.Context) error) {
	p.localNotice = ""
	if p.actions.Dispatch != nil {
		p.actions.Dispatch(action)
	}
}
func (p *Panel) local(action func() error) {
	if action == nil {
		return
	}
	if err := action(); err != nil {
		p.localNotice = "Could not complete this action. Try again."
	} else {
		p.localNotice = ""
	}
}

func (p *Panel) View(c *ui.Context) {
	c.SetTheme(radixTheme(c.Theme().Dark))
	t := c.Theme()
	c.Root().Background(t.Background)
	body := ui.Column(c).Fill().Background(t.Background)
	body.Padding(20).Gap(14).Children(func() {
		p.header(c)
		if p.recovery {
			p.recoveryView(c)
		} else if p.settings {
			p.settingsView(c)
		} else {
			p.home(c)
		}
		p.footer(c)
	})
}

func (p *Panel) header(c *ui.Context) {
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		if p.settings || p.recovery {
			panelIconButton(c, lucide.ArrowLeft(), "Back", false).OnClick(func() { p.settings = false; p.recovery = false; p.confirmRestart = false; p.localNotice = "" })
			title := "Settings"
			if p.recovery {
				title = "Check & repair"
			}
			ui.Text(c, title).FontSize(fontTitle).Bold().Grow(1)
		} else {
			ui.Text(c, "sakamoto").FontSize(fontTitle).Bold().Grow(1)
		}
		if p.actions.Demo {
			ui.Text(c, "Demo session").FontSize(fontSmall).TextColor(c.Theme().TextMuted)
		}
		if !p.settings && !p.recovery {
			panelIconButton(c, lucide.Settings(), "Settings", false).OnClick(func() { p.settings = true })
		}
	})
}

func (p *Panel) home(c *ui.Context) {
	s, t := p.snapshot, c.Theme()
	label, detail, icon, color := phasePresentation(s, t)
	ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
		components.Icon(c, icon).Size(22, 22).TextColor(color)
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, label).Bold()
			if detail != "" {
				ui.Text(c, detail).FontSize(fontSmall).TextColor(t.TextMuted)
			}
		})
		if !s.ExternalCore {
			text := "Connect"
			variant := components.ButtonPrimary
			if s.Service == core.ServiceRunning {
				text = "Disconnect"
				variant = components.ButtonSecondary
			}
			if s.Busy == "Connecting" || s.Busy == "Disconnecting" {
				text = s.Busy + "…"
			}
			disabled := !s.Observing || s.Busy != "" || s.Service == core.ServiceUnavailable || (s.Service != core.ServiceRunning && (!s.ConfigReady || s.Conflict))
			panelButton(c, text, variant, disabled).MinWidth(88).OnClick(func() {
				if s.Service == core.ServiceRunning {
					p.submitAction(p.backend.Disconnect)
				} else {
					p.submitAction(p.backend.Connect)
				}
			})
		}
	})
	if s.ExternalCore {
		ui.Text(c, "Managed externally").FontSize(fontSmall).TextColor(t.TextMuted)
	}
	p.modeControl(c)
	p.route(c)
	p.traffic(c)
	panelHairline(c)
	p.nodes(c)
}

func phasePresentation(s desktop.Snapshot, t *ui.Theme) (string, string, *ui.SVG, ui.Color) {
	if !s.Observing {
		return "Refreshing status…", "", lucide.Activity(), t.TextMuted
	}
	if s.ExternalCore {
		if !s.ConfigReady {
			return "Configuration needed", "Check the configuration in Settings.", lucide.CircleAlert(), t.Warning
		}
		if s.Service != core.ServiceRunning {
			return "Core unavailable", "Check the service and API settings.", lucide.WifiOff(), t.TextMuted
		}
		if !s.APIReady {
			return "Connecting to core…", "", lucide.Activity(), t.Accent
		}
		if s.Probe == core.ProbeReachable {
			detail := "Network reachable"
			if s.ProbePath == core.PathSystemOnly || s.ProbePath == core.PathProxyOnly {
				detail = "Reachable via " + s.ProbePath
			}
			return "Core online", detail, lucide.CircleCheck(), t.Success
		}
		if s.Probe == core.ProbeUnverified {
			return "Core online", "Network unverified. Retrying…", lucide.CircleAlert(), t.Warning
		}
		return "Core online", "Checking network…", lucide.Activity(), t.Accent
	}
	if !s.ConfigReady && s.Service != core.ServiceRunning {
		return "Configuration needed", "Open the TUI from Settings.", lucide.CircleAlert(), t.Warning
	}
	switch s.Phase {
	case core.PhaseReachable:
		detail := ""
		if s.ProbePath == core.PathSystemOnly || s.ProbePath == core.PathProxyOnly {
			detail = s.ProbePath
		}
		return "Network reachable", detail, lucide.CircleCheck(), t.Success
	case core.PhaseStarting:
		return "Connecting…", "", lucide.Power(), t.Accent
	case core.PhaseStopping:
		return "Disconnecting…", "", lucide.Power(), t.TextMuted
	case core.PhaseTUNRunning:
		return "Tunnel running", "Checking network…", lucide.Activity(), t.Accent
	case core.PhaseUnverified:
		return "Tunnel running", "Network unverified. Retrying…", lucide.CircleAlert(), t.Warning
	case core.PhaseConflict:
		return "VPN conflict", "Disconnect Shadowrocket to continue.", lucide.TriangleAlert(), t.Danger
	case core.PhaseUnavailable:
		return "Service unavailable", "Open Check & repair.", lucide.CircleAlert(), t.Warning
	default:
		if s.Conflict {
			return "Disconnected", "Disconnect Shadowrocket to connect.", lucide.WifiOff(), t.Warning
		}
		return "Disconnected", "", lucide.WifiOff(), t.TextMuted
	}
}

func (p *Panel) modeControl(c *ui.Context) {
	s := p.snapshot
	labels, values := []string{}, []string{}
	for _, mode := range []string{"Rule", "Global", "Direct"} {
		for _, value := range s.Modes {
			if strings.EqualFold(mode, value) {
				labels = append(labels, mode)
				values = append(values, value)
				break
			}
		}
	}
	if len(labels) == 0 {
		return
	}
	selected := -1
	for i, value := range values {
		if strings.EqualFold(value, s.Mode) {
			selected = i
		}
	}
	if panelSegmented(c.Key("routing"), &selected, labels, !s.APIReady || s.Busy != "").Label("Routing mode").Changed() && selected >= 0 && selected < len(values) {
		mode := values[selected]
		p.submitAction(func(ctx context.Context) error { return p.backend.SetMode(ctx, mode) })
	}
}

func (p *Panel) route(c *ui.Context) {
	groups := map[string]*daemon.Group{}
	for _, g := range p.snapshot.Groups {
		groups[g.Tag] = g
	}
	chain := []string{}
	tag := "MainProxy"
	for len(chain) < 6 {
		g := groups[tag]
		if g == nil {
			break
		}
		tag = g.Selected
		if tag == "" {
			break
		}
		chain = append(chain, tag)
	}
	if len(chain) == 0 {
		return
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Route").FontSize(fontSmall).TextColor(c.Theme().TextMuted)
		ui.Text(c, strings.Join(chain, " → ")).Grow(1).MinWidth(0).FontSize(fontSmall).SingleLine().TextColor(c.Theme().Text).Label("Active route: " + strings.Join(chain, ", "))
	})
}

func (p *Panel) traffic(c *ui.Context) {
	t := p.snapshot.Traffic
	if !t.Available {
		return
	}
	ui.Row(c).Gap(12).Children(func() {
		for _, value := range []struct{ label, value string }{{"Download", formatBytes(t.Downlink/2) + "/s"}, {"Upload", formatBytes(t.Uplink/2) + "/s"}, {"Transferred", formatBytes(t.DownlinkTotal + t.UplinkTotal)}} {
			ui.Column(c).Width(0).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, value.label).FontSize(fontSmall).TextColor(c.Theme().TextMuted)
				ui.Text(c, value.value).SingleLine()
			})
		}
	})
}

func formatBytes(value int64) string {
	if value < 0 {
		value = 0
	}
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	if value < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(value)/1024)
	}
	if value < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(value)/(1024*1024))
	}
	return fmt.Sprintf("%.1f GB", float64(value)/(1024*1024*1024))
}

func (p *Panel) nodes(c *ui.Context) {
	s, t := p.snapshot, c.Theme()
	if !s.APIReady {
		ui.Column(c).Grow(1).Gap(6).Padding(12, 0).Children(func() {
			ui.Text(c, "Nodes").Bold()
			message := "Connect to load nodes."
			if s.ExternalCore {
				message = "Start the core service to load nodes."
			}
			if s.Service == core.ServiceRunning {
				message = "Waiting for core API…"
			}
			ui.Text(c, message).FontSize(fontSmall).TextColor(t.TextMuted)
		})
		return
	}
	ui.Column(c).Grow(1).Gap(8).Children(func() {
		groupTags := []string{}
		var group *daemon.Group
		for _, g := range s.Groups {
			if len(g.Items) > 0 {
				groupTags = append(groupTags, g.Tag)
			}
			if g.Tag == p.group {
				group = g
			}
		}
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "Nodes").Bold().Grow(1)
			if len(groupTags) > 1 {
				panelSelect(c.Key("group"), &p.group, groupTags).Label("Node group").MaxWidth(210)
			} else if len(groupTags) == 1 {
				ui.Text(c, groupTags[0]).FontSize(fontSmall).TextColor(t.TextMuted).MaxWidth(210).SingleLine()
			}
		})
		panelSearch(c.Key("query"), &p.query)
		group = nil
		for _, g := range s.Groups {
			if g.Tag == p.group {
				group = g
				break
			}
		}
		items := []*daemon.GroupItem{}
		if group != nil {
			for _, item := range group.Items {
				if strings.Contains(strings.ToLower(item.Tag), strings.ToLower(p.query)) {
					items = append(items, item)
				}
			}
		}
		filter := p.group + "\x00" + p.query
		if filter != p.listFilter {
			p.listFilter = filter
			p.list.ScrollTo(0, ui.Start)
		}
		p.list.Key = func(i int) any { return p.group + "/" + items[i].Tag }
		p.list.Label = func(i int) string { return items[i].Tag }
		ui.List(c.Key("nodes"), &p.list, len(items), func(i int) { p.node(c, group, items[i]) }).Grow(1).MinHeight(90).Children(func() {
			if len(items) == 0 {
				message := "No nodes available"
				if p.query != "" {
					message = "No matching nodes"
				}
				ui.Text(c, message).FontSize(fontSmall).TextColor(t.TextMuted).Padding(12, 0)
			}
		})
	})
}

func (p *Panel) node(c *ui.Context, group *daemon.Group, item *daemon.GroupItem) {
	t := c.Theme()
	state := core.StatusFromLatency(item.UrlTestDelay, false)
	if observed, ok := p.snapshot.Tests[item.Tag]; ok {
		state = observed
	}
	status := "Untested"
	switch state {
	case core.NodeTesting:
		status = "Testing…"
	case core.NodeReachable:
		status = fmt.Sprintf("%d ms", item.UrlTestDelay)
	case core.NodeFailed:
		status = "Test failed"
	}
	ui.Row(c.Key(group.Tag + "/" + item.Tag)).AlignItems(ui.Center).Gap(4).Children(func() {
		panelButton(c, "", components.ButtonGhost, p.snapshot.Busy != "").Label("Use "+item.Tag).Grow(1).MinWidth(0).Height(44).Padding(4, 0).Border(0, ui.Transparent).Gap(8).Children(func() {
			if group.Selected == item.Tag {
				_, ink := selectedColors(c)
				components.Icon(c, lucide.Check()).Size(16, 16).TextColor(ink).Label("Selected in " + group.Tag)
			} else {
				ui.Box(c).Size(16, 16)
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				name := ui.Text(c, item.Tag).SingleLine()
				if group.Selected == item.Tag {
					name.Bold()
				}
				ui.Text(c, status).FontSize(fontSmall).TextColor(t.TextMuted)
			})
		}).OnClick(func() {
			p.submitAction(func(ctx context.Context) error { return p.backend.SelectNode(ctx, group.Tag, item.Tag) })
		})
		panelIconButton(c, lucide.Activity(), "Test "+item.Tag, p.snapshot.Busy != "" || state == core.NodeTesting).OnClick(func() { p.submitAction(func(ctx context.Context) error { return p.backend.Test(ctx, item.Tag) }) })
	})
}

func (p *Panel) settingsView(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).Gap(20).Children(func() {
		ui.Column(c).Gap(12).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
				ui.Text(c, "Appearance").Grow(1)
				if panelSelect(c.Key("appearance"), &p.appearance, []string{"System", "Light", "Dark"}).Label("Appearance").Changed() {
					p.local(func() error {
						if p.actions.SetAppearance != nil {
							return p.actions.SetAppearance(p.appearance)
						}
						return nil
					})
				}
			})
			value := p.openAtLogin
			if panelSwitch(c.Key("login"), &value, "Open at login", p.actions.Demo).Changed() {
				if p.actions.SetLogin != nil {
					if err := p.actions.SetLogin(value); err != nil {
						p.localNotice = "Could not change login startup. Check system startup settings."
					} else {
						p.openAtLogin = value
					}
				}
			}
		})
		panelHairline(c)
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "Configuration").Bold()
			if p.actions.OpenTUI != nil {
				panelButton(c, "Open TUI", components.ButtonSecondary, p.actions.Demo).OnClick(func() { p.local(p.actions.OpenTUI) })
			}
			panelButton(c, "Open configuration folder", components.ButtonSecondary, p.actions.Demo || p.actions.RevealConfig == nil).OnClick(func() { p.local(p.actions.RevealConfig) })
		})
		panelHairline(c)
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "Connection service").Bold()
			if p.snapshot.ExternalCore {
				ui.Text(c, "Managed externally").FontSize(fontSmall).TextColor(t.TextMuted)
			} else {
				dns := p.snapshot.DNS
				if dns == "" {
					dns = "unavailable"
				}
				ui.Text(c, "DNS: "+dns).FontSize(fontSmall).TextColor(t.TextMuted)
			}
		})
		ui.Text(c, "GPL-3.0-or-later").FontSize(fontSmall).TextColor(t.TextMuted)
	})
}

func (p *Panel) footer(c *ui.Context) {
	panelHairline(c)
	notice := ""
	if p.snapshot.Notice.Kind == core.NoticeError {
		notice = p.snapshot.Notice.Text
	}
	if p.recovery && (notice == p.snapshot.Health.Summary || notice == p.snapshot.Recovery.Summary) {
		notice = ""
	}
	if p.localNotice != "" {
		notice = p.localNotice
	}
	if notice != "" {
		ui.Text(c, notice).FontSize(fontSmall).TextColor(c.Theme().Danger).MaxHeight(44).Label(notice)
	}
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		if !p.recovery {
			panelButton(c, "Check & repair", components.ButtonGhost, false).Padding(6, 0).Border(0, ui.Transparent).OnClick(func() { p.settings = false; p.recovery = true; p.confirmRestart = false; p.localNotice = "" })
		}
		ui.Spacer(c)
		panelButton(c, "Quit app", components.ButtonGhost, false).Padding(6, 0).Border(0, ui.Transparent).OnClick(func() {
			if p.actions.Quit != nil {
				p.actions.Quit()
			}
		})
	})
}
