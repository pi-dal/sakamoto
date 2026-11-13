package menubar

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/core"
	"github.com/pi-dal/sakamoto/internal/desktop"
)

func (p *Panel) managementLinks(c *ui.Context) {
	for _, page := range []string{"Learning", "Nodes & sources", "Proxy groups", "Routing policy"} {
		panelButton(c, page, components.ButtonSecondary, false).OnClick(func() { p.openManagement(page) })
	}
}
func (p *Panel) openManagement(page string) {
	p.managementReturnConfig = p.configuration
	p.managementNoticeBaseline = p.snapshot.Notice
	p.confirmConfig = ""
	p.managementPage = page
	p.managementEditor = ""
	p.managementQuery = ""
	p.selectedFailures = map[string]bool{}
	p.localNotice = ""
	if !p.loadManagement() {
		p.managementPage = ""
		return
	}
	p.settings = false
	p.configuration = false
	p.setConnections(false)
}
func (p *Panel) loadManagement() bool {
	if p.managementPage == "Proxy groups" {
		groups, err := p.backend.AutomaticGroups()
		if err != nil {
			p.localNotice = "Could not read proxy groups. Regenerate configuration and reopen this page."
			return false
		}
		p.automaticGroups = groups
		return true
	}
	m, err := p.backend.Management()
	if err != nil {
		p.localNotice = "Could not read saved sources. Check the configuration folder and reopen."
		return false
	}
	p.managementDraft = m
	if p.managementPage == "Learning" {
		l, err := p.backend.Learning()
		if err != nil {
			p.localNotice = "Could not read learning history. Check the learning files and refresh."
			return false
		}
		p.learningData = l
		allowed := map[string]bool{}
		for _, v := range l.Failures {
			allowed[v.Domain] = true
		}
		for d := range p.selectedFailures {
			if !allowed[d] {
				delete(p.selectedFailures, d)
			}
		}
	}
	return true
}
func (p *Panel) submitManagement(action func(context.Context) error) {
	p.managementNoticeBaseline = core.Notice{}
	p.submitAction(func(ctx context.Context) error {
		err := action(ctx)
		if err == nil {
			select {
			case p.managementSaved <- true:
			default:
			}
		}
		return err
	})
}
func (p *Panel) managementView(c *ui.Context) {
	ui.Scroll(c.Key("management-" + p.managementPage + "-" + p.managementEditor)).Grow(1).Gap(12).PaddingEnd(8).Children(func() {
		if p.managementPage == "Proxy groups" {
			p.automaticGroupsView(c)
			return
		}
		if p.managementEditor != "" {
			p.sourceEditor(c)
			return
		}
		if p.managementPage == "Learning" {
			p.learningView(c)
			return
		}
		p.managementApply(c)
		panelSearchLabel(c.Key("source-search"), &p.managementQuery, "Search saved sources")
		if p.managementPage == "Routing policy" {
			p.policyList(c)
		} else {
			p.sourceList(c)
		}
		p.managementNotice(c)
	})
}
func (p *Panel) managementApply(c *ui.Context) {
	busy := p.snapshot.Busy != ""
	ui.Text(c, "Saved changes need to be applied to the VPN.").FontSize(fontSmall).TextColor(c.Theme().TextMuted)
	if p.snapshot.ExternalCore {
		ui.Text(c, "Apply saved configuration through your external service manager.").FontSize(fontSmall).TextColor(c.Theme().TextMuted)
		return
	}
	if p.snapshot.Health.UpgradeRequired {
		ui.Text(c, "Update the background supervisor and watcher to apply saved configuration.").FontSize(fontSmall).TextColor(c.Theme().TextMuted)
		panelButton(c, "Update service and apply…", components.ButtonPrimary, busy || p.actions.Demo || p.actions.UpdateService == nil).OnClick(func() { p.local(p.actions.UpdateService) })
		return
	}
	if p.confirmConfig == "management" {
		text, label := "The VPN will connect using your saved configuration.", "Regenerate and connect"
		if p.snapshot.Service == core.ServiceRunning {
			text, label = "Active connections will be interrupted.", "Regenerate and reconnect"
		}
		ui.Text(c, text).FontSize(fontSmall)
		ui.Row(c).Gap(8).Children(func() {
			panelButton(c, "Cancel apply", components.ButtonSecondary, busy).OnClick(func() { p.confirmConfig = "" })
			panelButton(c, label, components.ButtonPrimary, busy).OnClick(func() {
				p.confirmConfig = ""
				action := func(ctx context.Context) error { return p.backend.RepairConfiguration(ctx, false) }
				if p.managementPage == "Proxy groups" {
					p.submitManagement(action)
				} else {
					p.submitAction(action)
				}
			})
		})
		return
	}
	panelButton(c, "Apply saved configuration…", components.ButtonPrimary, busy).OnClick(func() { p.confirmConfig = "management" })
}
func (p *Panel) managementNotice(c *ui.Context) {
	if p.snapshot.Notice == p.managementNoticeBaseline {
		return
	}
	if text := p.snapshot.Notice.Text; text != "" {
		color := c.Theme().TextMuted
		if p.snapshot.Notice.Kind == core.NoticeError {
			color = c.Theme().Danger
		}
		ui.Text(c, text).FontSize(fontSmall).TextColor(color)
	}
}
func (p *Panel) learningView(c *ui.Context) {
	t := c.Theme()
	ui.Text(c, "Saved mode: "+p.learningData.Mode).Bold()
	ui.Text(c, fmt.Sprintf("%d learned routes", p.learningData.LearnedCount)).FontSize(fontSmall).TextColor(t.TextMuted)
	ui.Text(c, "Auto verifies failed direct destinations through the proxy before learning a route. The watcher owns these checks.").FontSize(fontSmall).TextColor(t.TextMuted)
	ui.Row(c).Gap(8).Children(func() {
		panelButton(c, "Learning settings", components.ButtonSecondary, p.snapshot.Busy != "").OnClick(func() { p.managementPage = ""; p.openConfiguration() })
		panelButton(c, "Refresh checks", components.ButtonSecondary, p.snapshot.Busy != "").OnClick(func() { p.localNotice = ""; p.loadManagement() })
	})
	panelHairline(c)
	ui.Text(c, "Recent checks").Bold()
	ui.Text(c, "Select failed direct checks to save exact proxy rules. Existing overrides are kept.").FontSize(fontSmall).TextColor(t.TextMuted)
	ui.Text(c, fmt.Sprintf("%d selected", len(p.selectedFailures))).FontSize(fontSmall).TextColor(t.TextMuted)
	panelButton(c, "Add selected to Config", components.ButtonPrimary, len(p.selectedFailures) == 0 || p.snapshot.Busy != "").OnClick(func() {
		domains := []string{}
		for _, v := range p.learningData.Failures {
			if p.selectedFailures[v.Domain] {
				domains = append(domains, v.Domain)
			}
		}
		p.submitManagement(func(ctx context.Context) error { return p.backend.AddRecentFailures(ctx, domains) })
	})
	p.managementNotice(c)
	allowed := map[string]bool{}
	for _, v := range p.learningData.Failures {
		allowed[v.Domain] = true
	}
	rendered := map[string]bool{}
	for i, v := range p.learningData.Checks {
		ui.Column(c.Key(fmt.Sprintf("check-%d", i))).Gap(4).Children(func() {
			if allowed[v.Domain] && !rendered[v.Domain] {
				selected := p.selectedFailures[v.Domain]
				button := panelButton(c, "", components.ButtonGhost, p.snapshot.Busy != "").Label("Select " + v.Domain).Gap(8).Children(func() {
					if selected {
						components.Icon(c, lucide.Check()).Size(16, 16).Label("Selected")
					} else {
						ui.Box(c).Size(16, 16).Radius(3).Border(hairline, controlBorder(c, false))
					}
					ui.Text(c, v.Domain).Grow(1).MinWidth(0).SingleLine()
				})
				if selected {
					fill, ink := selectedColors(c)
					button.Background(fill).TextColor(ink)
				}
				button.OnClick(func() {
					if p.selectedFailures[v.Domain] {
						delete(p.selectedFailures, v.Domain)
					} else {
						p.selectedFailures[v.Domain] = true
					}
				})
				rendered[v.Domain] = true
			} else {
				ui.Text(c, v.Domain).SingleLine().Label(v.Domain)
			}
			ui.Text(c, strings.ReplaceAll(v.State, "_", " ")+" / "+v.Time.Format("Jan 2 15:04")).FontSize(fontSmall).TextColor(t.TextMuted)
			if v.Detail != "" {
				ui.Text(c, v.Detail).FontSize(fontSmall).TextColor(t.TextMuted)
			}
		})
	}
	if len(p.learningData.Checks) == 0 {
		ui.Text(c, "No recent checks. Auto mode records checks when the watcher observes failed direct traffic.").FontSize(fontSmall).TextColor(t.TextMuted)
	}
}
func (p *Panel) beginSourceEdit(kind string, index int) {
	p.managementNoticeBaseline = p.snapshot.Notice
	p.confirmConfig = ""
	p.managementEditor = kind
	p.managementIndex = index
	p.editName = ""
	p.editValue = ""
	p.editChoice = ""
	p.editServer, p.editPort = "", ""
	p.editEndpoint, p.replaceNodeLink = false, false
	p.revealSource = false
	p.localNotice = ""
	switch kind {
	case "node":
		if index >= 0 {
			p.editValue = p.managementDraft.Nodes[index].Raw
			if endpoint, err := desktop.NodeEndpoint(p.editValue); err == nil {
				p.editEndpoint = true
				p.editName, p.editServer, p.editPort = endpoint.Remark, endpoint.Server, endpoint.Port
			}
		}
	case "subscription":
		p.editChoice = "auto"
		if index >= 0 {
			v := p.managementDraft.Subscriptions[index]
			p.editName = v.Name
			p.editValue = v.URL
			p.editChoice = v.Format
			if p.editChoice == "" {
				p.editChoice = "auto"
			}
		}
	case "policy":
		p.editChoice = "proxy"
		if index >= 0 {
			v := p.managementDraft.Policies[index]
			p.editValue = v.Match
			p.editChoice = v.Action
		}
	}
}
func (p *Panel) matches(values ...string) bool {
	return strings.Contains(strings.ToLower(strings.Join(values, "\n")), strings.ToLower(strings.TrimSpace(p.managementQuery)))
}
func (p *Panel) sourceList(c *ui.Context) {
	busy := p.snapshot.Busy != ""
	ui.Row(c).Gap(8).Children(func() {
		panelButton(c, "Add node", components.ButtonPrimary, busy).OnClick(func() { p.beginSourceEdit("node", -1) })
		panelButton(c, "Add subscription", components.ButtonSecondary, busy).OnClick(func() { p.beginSourceEdit("subscription", -1) })
	})
	ui.Text(c, "Manual nodes").Bold()
	count := 0
	for i, v := range p.managementDraft.Nodes {
		if !p.matches(v.Tag, v.Type) {
			continue
		}
		count++
		ui.Row(c.Key(fmt.Sprintf("saved-node-%d", i))).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, v.Tag).SingleLine().Label(v.Tag)
				ui.Text(c, v.Type).FontSize(fontSmall).TextColor(c.Theme().TextMuted)
			})
			panelButton(c, "Edit", components.ButtonSecondary, busy).Label("Edit node " + v.Tag).OnClick(func() { p.beginSourceEdit("node", i) })
		})
	}
	if count == 0 {
		ui.Text(c, "No matching manual nodes").FontSize(fontSmall).TextColor(c.Theme().TextMuted)
	}
	panelHairline(c)
	ui.Text(c, "Subscriptions").Bold()
	count = 0
	for i, v := range p.managementDraft.Subscriptions {
		if !p.matches(v.Name) {
			continue
		}
		count++
		ui.Row(c.Key(fmt.Sprintf("saved-sub-%d", i))).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, v.Name).Grow(1).MinWidth(0).SingleLine().Label(v.Name)
			panelButton(c, "Edit", components.ButtonSecondary, busy).Label("Edit subscription " + v.Name).OnClick(func() { p.beginSourceEdit("subscription", i) })
		})
	}
	if count == 0 {
		ui.Text(c, "No matching subscriptions").FontSize(fontSmall).TextColor(c.Theme().TextMuted)
	}
}
func (p *Panel) policyList(c *ui.Context) {
	busy := p.snapshot.Busy != ""
	panelButton(c, "Add rule", components.ButtonPrimary, busy).OnClick(func() { p.beginSourceEdit("policy", -1) })
	count := 0
	for i, v := range p.managementDraft.Policies {
		if !p.matches(v.Match, v.Action) {
			continue
		}
		count++
		ui.Row(c.Key(fmt.Sprintf("policy-%d", i))).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, v.Match).SingleLine().Label(v.Match)
				ui.Text(c, v.Action).FontSize(fontSmall).TextColor(c.Theme().TextMuted)
			})
			panelButton(c, "Edit", components.ButtonSecondary, busy).Label("Edit rule " + v.Match).OnClick(func() { p.beginSourceEdit("policy", i) })
		})
	}
	if count == 0 {
		ui.Text(c, "No matching rules").FontSize(fontSmall).TextColor(c.Theme().TextMuted)
	}
}
func (p *Panel) sourceInput(c *ui.Context, label string, value *string, secret bool) {
	ui.Text(c, label).Label(label + " field").FontSize(fontSmall).TextColor(c.Theme().TextMuted)
	input := ui.TextInputBase(c.Key(label), value).Label(label).Placeholder(label).Height(36).Padding(6, 8).Radius(radius).Background(c.Theme().Surface).TextColor(c.Theme().Text).Border(hairline, controlBorder(c, false)).FocusRing(false).Disabled(p.snapshot.Busy != "")
	if secret && !p.revealSource {
		input.Password()
	}
	if input.Focused() {
		input.BorderColor(c.Theme().Focus)
	}
	panelFocus(c, input)
}
func (p *Panel) sourceEditor(c *ui.Context) {
	busy := p.snapshot.Busy != ""
	kind, index := p.managementEditor, p.managementIndex
	switch kind {
	case "node":
		if p.editEndpoint {
			panelSwitch(c.Key("replace-node-link"), &p.replaceNodeLink, "Replace full share link", busy)
		}
		if p.editEndpoint && !p.replaceNodeLink {
			ui.Text(c, "Credentials and advanced options are preserved.").FontSize(fontSmall).TextColor(c.Theme().TextMuted)
			p.sourceInput(c, "Node remark", &p.editName, false)
			p.sourceInput(c, "Node server", &p.editServer, false)
			endpoint, _ := desktop.NodeEndpoint(p.managementDraft.Nodes[index].Raw)
			if endpoint.Port != "" {
				p.sourceInput(c, "Node port", &p.editPort, false)
			} else {
				ui.Text(c, "Hopping ports are preserved. Replace the share link to change them.").FontSize(fontSmall).TextColor(c.Theme().TextMuted)
			}
		} else {
			ui.Text(c, "Paste a node share link. Protocol credentials and advanced options stay in the link.").FontSize(fontSmall).TextColor(c.Theme().TextMuted)
			p.sourceInput(c, "Node share link", &p.editValue, true)
			panelSwitch(c.Key("reveal-link"), &p.revealSource, "Reveal link", busy)
		}
	case "subscription":
		p.sourceInput(c, "Subscription name", &p.editName, false)
		p.sourceInput(c, "Subscription URL", &p.editValue, true)
		panelSwitch(c.Key("reveal-url"), &p.revealSource, "Reveal URL", busy)
		panelSelect(c.Key("sub-format"), &p.editChoice, []string{"auto", "singbox", "clash", "base64"}).Label("Subscription format").Disabled(busy)
	case "policy":
		p.sourceInput(c, "Policy match", &p.editValue, false)
		ui.Text(c, "Hostname, *.suffix, keyword:value, cidr:value or HTTP(S) URL.").FontSize(fontSmall).TextColor(c.Theme().TextMuted)
		panelSelect(c.Key("policy-action"), &p.editChoice, []string{"proxy", "direct", "reject"}).Label("Policy action").Disabled(busy)
	}
	label := map[string]string{"node": "Save node", "subscription": "Save subscription", "policy": "Save rule"}[kind]
	if p.localNotice != "" {
		ui.Text(c, p.localNotice).FontSize(fontSmall).TextColor(c.Theme().Danger).Label(p.localNotice)
	}
	ui.Row(c).Gap(8).Children(func() {
		panelButton(c, label, components.ButtonPrimary, busy || strings.TrimSpace(p.editValue) == "").OnClick(func() { p.saveSource(false) })
		panelButton(c, "Cancel edit", components.ButtonSecondary, busy).OnClick(func() { p.managementEditor = "" })
	})
	if index >= 0 {
		panelButton(c, "Remove", components.ButtonGhost, busy).OnClick(func() { p.saveSource(true) })
	}
	p.managementNotice(c)
}
func (p *Panel) saveSource(remove bool) {
	m := p.managementDraft
	switch p.managementEditor {
	case "node":
		original := desktop.ManualNode{}
		if p.managementIndex >= 0 {
			original = m.Nodes[p.managementIndex]
		}
		value := p.editValue
		if remove {
			value = ""
		} else if p.editEndpoint && !p.replaceNodeLink {
			var err error
			value, err = desktop.UpdateNodeEndpoint(original.Raw, p.editName, p.editServer, p.editPort)
			if err != nil {
				p.localNotice = strings.ToUpper(err.Error()[:1]) + err.Error()[1:]
				return
			}
		}
		p.submitManagement(func(ctx context.Context) error { return p.backend.SaveNode(ctx, original, value) })
	case "subscription":
		values := append([]config.SubSource(nil), m.Subscriptions...)
		v := config.SubSource{Name: p.editName, URL: p.editValue, Format: p.editChoice}
		if i := p.managementIndex; i >= 0 {
			if remove {
				values = append(values[:i], values[i+1:]...)
			} else {
				values[i] = v
			}
		} else {
			values = append(values, v)
		}
		p.submitManagement(func(ctx context.Context) error { return p.backend.SaveSubscriptions(ctx, m, values) })
	case "policy":
		values := append([]config.PolicyRule(nil), m.Policies...)
		v := config.PolicyRule{Match: p.editValue, Action: p.editChoice}
		if i := p.managementIndex; i >= 0 {
			if remove {
				values = append(values[:i], values[i+1:]...)
			} else {
				values[i] = v
			}
		} else {
			values = append(values, v)
		}
		p.submitManagement(func(ctx context.Context) error { return p.backend.SavePolicies(ctx, m, values) })
	}
}
