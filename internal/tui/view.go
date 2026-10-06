package tui

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/pi-dal/sakamoto/internal/core"
	"github.com/pi-dal/sakamoto/internal/gen"
	"github.com/sagernet/sing-box/daemon"
)

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
		return "Not connected"
	}
	if m.conn == nil {
		return "Default: " + tag + " (offline)"
	}
	return tag
}

// offlineGroups reads generated config so groups remain browsable while offline.
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
		return "Not generated"
	}
	var cfg struct {
		Route struct {
			Final string `json:"final"`
		} `json:"route"`
		Outbounds []struct {
			Tag        string `json:"tag"`
			Type       string `json:"type"`
			Detour     string `json:"detour"`
			Server     string `json:"server"`
			ServerPort int    `json:"server_port"`
		} `json:"outbounds"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return "Invalid config"
	}
	for _, o := range cfg.Outbounds {
		if o.Type == "socks" && o.Detour == "MainProxy" && o.Server != "" {
			return fmt.Sprintf("%s:%d", o.Server, o.ServerPort)
		}
	}
	for _, o := range cfg.Outbounds {
		if o.Tag == cfg.Route.Final && o.Server != "" {
			return fmt.Sprintf("%s:%d", o.Server, o.ServerPort)
		}
	}
	return cfg.Route.Final
}

func (m *model) addHit(x0, x1, y int, action string, index int) {
	m.hits = append(m.hits, hit{x0, x1, y, action, index})
}

// servicePhaseState maps the supervisor report to the shared service
// lifecycle. The supervisor protocol only reports connected/disconnected;
// "unavailable" is this TUI's local marker for an unreachable supervisor.
func servicePhaseState(state string) core.ServiceState {
	switch state {
	case "connected":
		return core.ServiceRunning
	case "unavailable":
		return core.ServiceUnavailable
	default:
		return core.ServiceStopped
	}
}

// probePhaseState maps the stored probe display value to the shared probe
// state. Reachable keeps the historical "Available" display prefix.
func probePhaseState(networkState string) core.ProbeState {
	switch {
	case strings.HasPrefix(networkState, "Available"):
		return core.ProbeReachable
	case networkState == string(core.ProbeUnverified):
		return core.ProbeUnverified
	case networkState == string(core.ProbeChecking):
		return core.ProbeChecking
	default:
		return core.ProbeIdle
	}
}

// statusBadge derives the status-bar badge from the shared core phase model.
// The precedence (conflict over probe, unavailable over everything) lives in
// core.PhaseOf; the label strings must stay identical to the historical TUI
// output because mouse hit regions are measured from the rendered width.
func (m *model) statusBadge() (label, styled string) {
	phase := core.PhaseOf(servicePhaseState(m.serviceState), probePhaseState(m.networkState), m.shadowrocket)
	switch phase {
	case core.PhaseReachable:
		label = "● Network reachable"
		styled = good.Render(label)
	case core.PhaseUnverified:
		label = "● TUN running · retrying probe"
		styled = accent.Render(label)
	case core.PhaseConflict:
		label = "⚠ VPN conflict"
		styled = bad.Render(label)
	case core.PhaseUnavailable:
		label = "● Supervisor unavailable"
		styled = bad.Render(label)
	case core.PhaseStarting:
		label = "● Connecting…"
		styled = accent.Render(label)
	case core.PhaseStopping:
		label = "● Disconnecting…"
		styled = accent.Render(label)
	case core.PhaseTUNRunning:
		label = "● TUN running"
		styled = good.Render(label)
	default:
		label = "● Disconnected"
		styled = bad.Render(label)
	}
	return label, styled
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
	mode := "Offline"
	if m.mode != "" {
		mode = m.mode
	}
	selection := "Auto"
	for _, g := range m.groups {
		if g.Tag == "MainProxy" && g.Selected != "" {
			selection = g.Selected
		}
	}
	latency := "Not tested"
	for _, g := range m.groups {
		if g.Tag == selection {
			for _, i := range g.Items {
				if i.Tag == g.Selected && i.UrlTestDelay > 0 {
					latency = fmt.Sprintf("%d ms", i.UrlTestDelay)
				}
			}
		}
	}
	fallback := "Off"
	if m.cfg.FallbackEnabled {
		fallback = strings.Join(m.cfg.Fallbacks["MainProxy"], " → ")
	}
	return []string{
		accent.Render("CURRENT PROXY"), "", "Entry  " + trunc(m.activeNode(), 26), "Selection  " + selection, "Latency  " + latency,
		"", accent.Render("CHAIN EXIT"), "", trunc(m.exitLabel, 30), "",
		accent.Render("NETWORK"), "", "Mode  " + mode, "Fallback  " + fallback, "Tailscale  " + map[bool]string{true: "Optimized", false: "Off"}[m.cfg.TailscaleOptimize],
	}
}
func (m *model) View() string {
	m.prevHits, m.hits = m.hits, nil
	if m.width < 64 || m.height < 16 {
		return fmt.Sprintf("sakamoto · terminal must be at least 64×16 (now %d×%d)\n", m.width, m.height)
	}
	w, inner := m.width, m.width-4
	var b strings.Builder
	b.WriteString("┌" + strings.Repeat("─", w-2) + "┐\n")
	stateLabel, state := m.statusBadge()
	label := "[ Connect ]"
	if m.serviceState == "connected" {
		label = "[ Disconnect ]"
	}
	button := good.Render(label)
	if m.hovered("connect", 0) {
		button = tab.Render(label)
	}
	action := "[ Test all ]"
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
	path := "Entry " + m.activeNode()
	if m.cfg.ChainEnabled {
		path += "  →  SOCKS " + m.exitLabel
	}
	b.WriteString(framedLine(" "+muted.Render(path), inner))
	b.WriteString("└" + strings.Repeat("─", w-2) + "┘\n")
	// Boxed tabs use inverse styling for selection and cell-aligned hit regions.
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
		prefix := "Actions " + name + "  "
		body.WriteString(prefix)
		x = 2 + lipgloss.Width(prefix)
		for _, bt := range []struct{ text, action string }{{"[Use]", "menu-use"}, {"[Test]", "menu-test"}, {"[Details]", "menu-detail"}, {"[Back]", "back"}} {
			text := bt.text + " "
			body.WriteString(muted.Render(text))
			m.addHit(x, x+lipgloss.Width(bt.text), startY, bt.action, 0)
			x += lipgloss.Width(text)
		}
		body.WriteByte('\n')
		startY++
	}
	if m.notice != "" && m.page != aboutPage {
		body.WriteString(" " + muted.Render(m.notice) + "\n")
		startY++
	}
	if m.page != aboutPage && m.serviceState == "unavailable" {
		body.WriteString(" " + bad.Render("Supervisor unavailable; see the one-time setup in README") + "\n")
		startY++
	}
	if m.page != aboutPage && m.shadowrocket {
		message := "Shadowrocket VPN is active. Disconnect it before using sakamoto."
		if m.serviceState == "connected" {
			message = "Two TUNs conflict. Disconnect both VPNs, then reconnect sakamoto."
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
	case aboutPage:
		m.renderAbout(&body, startY)
	}
	// Offset content hit regions by the frame and inner padding.
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
	foot := " Click · Scroll   Tab pages   Esc back   q quit"
	b.WriteString(ansi.Truncate(muted.Render(foot), w, "…"))
	return b.String()
}
func (m *model) renderHome(b *strings.Builder, startY int) {
	mode := m.mode
	if mode == "" {
		mode = "Offline"
	}
	modeButton := "[ Mode: " + mode + " ]"
	style := muted
	if m.hovered("routing-mode", 0) {
		style = focus
	}
	b.WriteString(" " + style.Render(modeButton) + "  m: Rule / Global / Direct\n")
	modeY := startY
	if m.cfg.DNSGuard.Enabled || m.dnsState == "protected" || m.dnsState == "degraded" {
		label := m.dnsState
		if label == "" {
			label = "requires root-daemon upgrade / reconnect"
		}
		b.WriteString(" " + muted.Render("System DNS: "+label) + "\n")
		startY++
	}
	m.addHit(1, 1+lipgloss.Width(modeButton), modeY, "routing-mode", 0)
	startY++
	if len(m.rows) == 0 {
		b.WriteString(" No nodes yet. Check subscriptions or regenerate in Config.\n")
		return
	}
	available := max(3, m.height-startY-4)
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	m.scroll = max(0, min(m.scroll, len(m.rows)-1))
	// Leave one row between groups and include it in scrolling math.
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
		latency := "Untested"
		if r.item.UrlTestDelay > 0 {
			latency = fmt.Sprintf("Reachable %dms", r.item.UrlTestDelay)
		}
		if m.lastTest[r.item.Tag] < 0 {
			latency = "Failed/timed out"
		}
		if m.batch != nil {
			if v, ok := m.batch.results[r.item.Tag]; ok {
				if v < 0 {
					latency = "Failed/timed out"
				} else {
					latency = fmt.Sprintf("Reachable %dms", v)
				}
			} else if _, ok := m.batch.baseline[r.item.Tag]; ok {
				latency = "Testing…"
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
	fmt.Fprintf(b, "\n %s\n", muted.Render(fmt.Sprintf("Node %d/%d · ● selected, not necessarily reachable · scroll for more", m.cursor+1, len(m.rows))))
}

func (m *model) renderNodeDetail(b *strings.Builder, startY int) {
	if m.detailRow < 0 || m.detailRow >= len(m.rows) || m.rows[m.detailRow].item == nil {
		m.detailRow = -1
		return
	}
	r := m.rows[m.detailRow]
	name := r.item.Tag
	fmt.Fprintf(b, " %s\n\n", accent.Render(name))
	fmt.Fprintf(b, " Group       %s\n Protocol    %s\n", r.group.Tag, r.item.Type)
	delay := "Not tested"
	if r.item.UrlTestDelay > 0 {
		delay = fmt.Sprintf("%d ms", r.item.UrlTestDelay)
	}
	fmt.Fprintf(b, " Latency     %s\n", delay)
	// Show the server address only; never display UUIDs or passwords.
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
	fmt.Fprintf(b, " Server      %s\n", server)
	b.WriteString("\n [ Back ]")
	m.addHit(1, lipgloss.Width(" [ Back ]")+1, startY+7, "back", 0)
	b.WriteString("\n")
}

var configSections = []string{"General", "Routing", "Proxy groups", "Nodes & sources", "DNS", "Import limits", "Policy"}

const policySection = 6

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
			return []string{"Source config is not cached; import or refresh it first"}
		}
		section := ""
		for _, line := range strings.Split(string(f), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[") {
				section = line
				continue
			}
			if section == "[General]" && line != "" && !strings.HasPrefix(line, "#") {
				// Truncate long lists here; the full values remain in the source file.
				result = append(result, trunc(line, max(25, m.width-5)))
			}
		}
		return result
	case 1:
		result := []string{"Final route: " + c.Route.Final}
		for _, r := range c.Route.RuleSet {
			result = append(result, "Rule set: "+r.Tag)
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
		result := []string{"Manual nodes file: " + m.cfg.NodesFile}
		for _, s := range m.cfg.Subscriptions {
			result = append(result, "Subscription: "+s.Name)
		}
		return result
	case 4:
		var result []string
		for _, s := range c.DNS.Servers {
			result = append(result, s.Tag+" · "+s.Type+" · "+s.Server)
		}
		return result
	default:
		return []string{"HTTP rewrites, MITM, and Spotify scripts have no sing-box equivalent.", "Generation reports these differences instead of silently discarding them."}
	}
}
func (m *model) renderConfig(b *strings.Builder, startY int) {
	if m.configForm {
		m.renderConfigForm(b, startY)
		return
	}
	if m.importing {
		m.renderImportForm(b, startY)
		return
	}
	// An older plaintext snapshot must not masquerade as current compiled rules.
	if warning := staleShadowrocketSource(m.cfg.ConfPath); warning != "" {
		b.WriteString(" " + bad.Render(warning) + "\n")
		startY++
	}
	if m.policyEditing {
		m.renderPolicyEditor(b, startY)
		return
	}
	if m.configDetail == 3 {
		m.renderSources(b, startY)
		return
	}
	if m.configDetail == policySection {
		m.renderPolicy(b, startY)
		return
	}
	if m.configDetail >= 0 && m.configDetail < len(configSections) {
		w := newContentWriter(m, b, startY)
		w.line(" " + accent.Render(configSections[m.configDetail]))
		w.buttons(control{text: "[ Back to Config ]", action: "back"})
		items := m.sectionDetails(m.configDetail)
		available := max(1, m.height-2-w.y())
		m.detailScroll = min(m.detailScroll, max(0, len(items)-available))
		for j := m.detailScroll; j < len(items) && j < m.detailScroll+available; j++ {
			w.line("   " + muted.Render(ansi.Truncate(items[j], m.width-7, "…")))
		}
		return
	}
	w := newContentWriter(m, b, startY)
	w.line(" Configuration")
	// Primary actions stay visible at the supported minimum terminal size.
	w.buttons(control{text: "[ Import config ]", action: "import"}, control{text: "[ Regenerate ]", action: "generate"}, control{text: "[ Edit settings ]", action: "edit-config"})
	for i, name := range configSections {
		w.row("   "+name+"  ›", "section", i, false)
	}
	w.line(" Subscription sources")
	w.buttons(control{text: "[ Add source ]", action: "add-sub"}, control{text: "[ Edit source ]", action: "edit-sub"}, control{text: "[ Remove source ]", action: "delete-sub"})
	if len(m.cfg.Subscriptions) == 0 {
		w.line("   (No subscriptions; nodes.txt remains available)")
	} else {
		for i, s := range m.cfg.Subscriptions {
			if w.y() >= m.height-2 {
				break
			}
			name := s.Name
			if name == "" {
				name = "Unnamed"
			}
			w.row(fmt.Sprintf("   %s  %s", name, redactURL(s.URL)), "source", i, i == m.sourceCursor)
		}
	}
}
func (m *model) renderImportForm(b *strings.Builder, startY int) {
	title := "Import Shadowrocket .conf"
	hint := "Merge includes and generate a sing-box config"
	switch m.importKind {
	case "node":
		title = "Add node"
		hint = "Paste one share link; credentials stay hidden"
	case "node-edit":
		title = "Edit node"
		hint = "Ctrl+U clears input; Ctrl+R briefly reveals it"
	case "subscription":
		title = "Add subscription"
		hint = "Format: name|HTTPS URL"
	case "subscription-edit":
		title = "Edit subscription"
		hint = "Format: name|HTTPS URL (Ctrl+R reveals it)"
	}
	p := newPaneWriter(m, b, startY, 76)
	display := m.input
	if (m.importKind == "node" || m.importKind == "node-edit") && !m.revealInput {
		display = fmt.Sprintf("●●● (%d chars)", len([]rune(m.input)))
	} else if (m.importKind == "subscription" || m.importKind == "subscription-edit") && !m.revealInput {
		if name, link, ok := strings.Cut(display, "|"); ok {
			display = name + "|" + fmt.Sprintf("●●● (%d chars)", len([]rune(link)))
		}
	} else if u, err := url.Parse(display); err == nil && u.RawQuery != "" {
		display = strings.SplitN(display, "?", 2)[0] + "?…"
	}
	if display == "" {
		display = "Paste an address or share link"
	}
	p.line(accent.Render(title))
	p.field("Input  "+display+"▏", "import-input", 0, true)
	p.line(hint)
	p.buttons(control{text: "[ Confirm ]", action: "import-confirm"}, control{text: "[ Cancel ]", action: "import-cancel"})
	p.end()
}

func (m *model) renderConfigForm(b *strings.Builder, startY int) {
	if m.formCfg == nil {
		return
	}
	p := newPaneWriter(m, b, startY, 82)
	p.line(accent.Render("Edit sakamoto.yaml"))
	p.line("↑↓ choose · Enter selects / edits · Ctrl+S save · Esc cancel")
	available := max(1, m.height-p.y()-5)
	if m.formCursor < m.formScroll {
		m.formScroll = m.formCursor
	}
	if m.formCursor >= m.formScroll+available {
		m.formScroll = m.formCursor - available + 1
	}
	m.formScroll = max(0, min(m.formScroll, max(0, len(m.formRows)-available)))
	end := min(len(m.formRows), m.formScroll+available)
	for i := m.formScroll; i < end; i++ {
		r := m.formRows[i]
		if r.value == nil {
			p.line(accent.Render("  " + r.label))
			continue
		}
		value := r.value()
		if m.formEditing && i == m.formEditIndex {
			value = m.formInput + "▏"
		} else if len(r.choices) > 0 {
			value = "‹ " + value + " ›"
		} else if r.toggle != nil {
			value = "[" + value + "]"
		} else {
			value = "= " + value
		}
		p.field(fmt.Sprintf("%-31s %s", r.label, value), "form-field", i, i == m.formCursor)
	}
	p.line(m.formNotice)
	p.buttons(control{text: "[ Save ]", action: "form-save"}, control{text: "[ Cancel ]", action: "form-cancel"})
	p.end()
}

func (m *model) renderPolicy(b *strings.Builder, startY int) {
	w := newContentWriter(m, b, startY)
	w.line(" " + accent.Render("Routing policy"))
	w.buttons(control{text: "[ Add rule ]", action: "policy-add"}, control{text: "[ Edit rule ]", action: "policy-edit"}, control{text: "[ Remove rule ]", action: "policy-delete"}, control{text: "[ Back ]", action: "back"})
	w.line(" URL matches use the hostname; paths are ignored.")
	w.line(" Rule mode: reject first, then direct, then proxy.")
	w.line(" " + accent.Render("User overrides · editable"))
	if len(m.cfg.PolicyRules) == 0 {
		w.line("   No user overrides.")
	} else {
		available := max(1, m.height-2-w.y())
		if m.policyCursor < m.detailScroll {
			m.detailScroll = m.policyCursor
		}
		if m.policyCursor >= m.detailScroll+available {
			m.detailScroll = m.policyCursor - available + 1
		}
		m.detailScroll = max(0, min(m.detailScroll, max(0, len(m.cfg.PolicyRules)-available)))
		for i := m.detailScroll; i < len(m.cfg.PolicyRules) && w.y() < m.height-2; i++ {
			rule := m.cfg.PolicyRules[i]
			w.row(fmt.Sprintf("   %-7s %s", strings.ToUpper(rule.Action), policyDisplayMatch(rule.Match)), "policy-rule", i, i == m.policyCursor)
		}
	}
	if w.y() < m.height-2 {
		w.line(" " + accent.Render("Imported conf rules · read-only"))
		for _, line := range importedPolicySummary(m.cfg.ConfPath) {
			if w.y() < m.height-2 {
				w.line("   " + line)
			}
		}
	}
}

func importedPolicySummary(confPath string) []string {
	if u, err := url.Parse(confPath); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		confPath = filepath.Join(filepath.Dir(confPath), "imports", "macOS.conf")
	}
	seen := map[string]bool{}
	counts := map[string]int{"proxy": 0, "direct": 0, "reject": 0}
	files := 0
	var readConf func(string) error
	readConf = func(file string) error {
		if seen[file] {
			return nil
		}
		seen[file] = true
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		files++
		section := ""
		for _, raw := range strings.Split(string(data), "\n") {
			line := strings.TrimSpace(strings.SplitN(raw, "//", 2)[0])
			if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
				section = strings.ToLower(line)
				continue
			}
			if section == "[general]" {
				key, value, ok := strings.Cut(line, "=")
				if !ok || !strings.EqualFold(strings.TrimSpace(key), "include") {
					continue
				}
				for _, inc := range splitNonEmpty(strings.TrimSpace(value), ",") {
					if !strings.Contains(inc, "://") {
						_ = readConf(filepath.Join(filepath.Dir(file), inc))
					}
				}
				continue
			}
			if section != "[rule]" {
				continue
			}
			parts := strings.Split(line, ",")
			if len(parts) < 3 {
				continue
			}
			target := strings.ToLower(strings.TrimSpace(parts[2]))
			switch target {
			case "direct", "tailscale":
				counts["direct"]++
			case "reject", "reject-drop", "reject-no-drop":
				counts["reject"]++
			default:
				counts["proxy"]++
			}
		}
		return nil
	}
	if err := readConf(confPath); err != nil {
		return []string{"Source unavailable: " + filepath.Base(confPath)}
	}
	return []string{fmt.Sprintf("Source: %s", filepath.Base(confPath)), fmt.Sprintf("Files: %d · proxy: %d · direct: %d · reject: %d", files, counts["proxy"], counts["direct"], counts["reject"]), "Edit the imported .conf or add an override above."}
}

func policyDisplayMatch(match string) string {
	if u, err := url.Parse(match); err == nil && u.Hostname() != "" {
		return u.Hostname() + " (URL host)"
	}
	return match
}

func (m *model) renderPolicyEditor(b *strings.Builder, startY int) {
	p := newPaneWriter(m, b, startY, 76)
	title := "Add policy rule"
	if m.policyEditIndex >= 0 {
		title = "Edit policy rule"
	}
	p.line(accent.Render(title))
	p.line("Host, *.suffix, keyword:foo, CIDR or URL")
	match := m.policyMatch
	if m.policyField == 0 {
		match += "▏"
	}
	p.field("Match   = "+match, "policy-match", 0, m.policyField == 0)
	p.field("Action  ‹ "+strings.ToLower(m.policyAction)+" ›", "policy-action", 0, m.policyField == 1)
	p.line("Tab/↑↓ moves · Enter selects · Ctrl+S saves")
	if m.policyNotice != "" && p.y() < m.height-5 {
		p.line(m.policyNotice)
	}
	p.buttons(control{text: "[ Save ]", action: "policy-save"}, control{text: "[ Cancel ]", action: "policy-cancel"})
	p.end()
}

func (m *model) renderSources(b *strings.Builder, startY int) {
	w := newContentWriter(m, b, startY)
	w.line(" " + accent.Render("Nodes & sources"))
	w.buttons(control{text: "[ Add node ]", action: "add-node"}, control{text: "[ Edit node ]", action: "edit-node"}, control{text: "[ Remove node ]", action: "delete-node"})
	w.buttons(control{text: "[ Add source ]", action: "add-sub"}, control{text: "[ Edit source ]", action: "edit-sub"}, control{text: "[ Remove source ]", action: "delete-sub"}, control{text: "[ Back ]", action: "back"})
	w.line(" Subscriptions")
	if len(m.cfg.Subscriptions) == 0 {
		w.line("   None (manual nodes remain available)")
	} else {
		for i, s := range m.cfg.Subscriptions {
			if w.y() >= m.height-5 {
				break
			}
			w.row(fmt.Sprintf("   %-17s %s", s.Name, redactURL(s.URL)), "source", i, i == m.sourceCursor)
		}
	}
	w.line(" Manual nodes (select one, then use the buttons above)")
	entries, err := gen.ReadNodes(m.cfg.NodesFile)
	if err != nil {
		w.line("   Read failed: " + err.Error())
		return
	}
	if len(entries) == 0 {
		w.line("   No nodes")
		return
	}
	available := max(1, m.height-2-w.y())
	m.detailScroll = min(max(0, m.detailScroll), max(0, len(entries)-available))
	if m.nodeCursor < m.detailScroll {
		m.detailScroll = m.nodeCursor
	}
	if m.nodeCursor >= m.detailScroll+available {
		m.detailScroll = m.nodeCursor - available + 1
	}
	for i := m.detailScroll; i < len(entries) && w.y() < m.height-2; i++ {
		e := entries[i]
		w.row(fmt.Sprintf("   %-44s %-12s", ansi.Truncate(e.Tag, 43, "…"), e.Type), "node-source", i, i == m.nodeCursor)
	}
}

func redactURL(s string) string {
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host + "/…"
	}
	return "(local source)"
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
			return "Shadowrocket rules changed; export macOS.conf and its included ad rules again"
		}
	}
	return ""
}

func (m *model) renderData(b *strings.Builder, startY int) {
	if m.status != nil {
		fmt.Fprintf(b, " ↑ %s (%s/s)    ↓ %s (%s/s)    Connections %d\n", fmtB(m.status.UplinkTotal), fmtB(m.status.Uplink), fmtB(m.status.DownlinkTotal), fmtB(m.status.Downlink), len(m.conns))
	} else {
		b.WriteString(" Connect to inspect live traffic and connections.\n")
	}
	if m.selectedConn != "" {
		c := m.conns[m.selectedConn]
		if c == nil {
			m.selectedConn = ""
		} else {
			fmt.Fprintf(b, "\n %s\n Target: %s\n Source: %s\n Outbound: %s\n Chain: %s\n", accent.Render("Connection details"), c.Destination, c.Source, c.Outbound, strings.Join(c.ChainList, " → "))
			b.WriteString("\n [ Back ]  [ Close connection ]\n")
			m.addHit(1, 1+lipgloss.Width("[ Back ]"), startY+8, "back", 0)
			x := 1 + lipgloss.Width("[ Back ]  ")
			m.addHit(x, x+lipgloss.Width("[ Close connection ]"), startY+8, "close-conn", 0)
			return
		}
	}
	b.WriteString("\n Recent connections (select for details)\n")
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
	b.WriteString("\n Core logs\n")
	start := max(0, len(m.logs)-min(6, max(2, m.height-startY-maxConn-6)))
	for _, line := range m.logs[start:] {
		fmt.Fprintf(b, "   %s\n", muted.Render(trunc(line, max(20, m.width-5))))
	}
}
func (m *model) renderSettings(b *strings.Builder, startY int) {
	if m.editIndex >= 0 {
		m.renderSettingEditor(b, startY)
		return
	}
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
		value := r.value()
		if r.secret && value != "" {
			value = "•••••• (saved)"
		}
		if len(r.choices) > 0 {
			parts := make([]string, len(r.choices))
			for j, state := range r.choices {
				parts[j] = state
				if state == value {
					parts[j] = "[" + state + "]"
				}
			}
			value = strings.Join(parts, "  ")
		} else if r.toggle != nil {
			indicator = " [toggle]"
		} else if r.edit != nil {
			indicator = " [edit]"
		}
		line := fmt.Sprintf(" %-31s %s%s", r.label, value, indicator)
		if i == m.cfgCursor || m.hovered("setting", i) {
			line = focus.Render(">" + line)
		}
		b.WriteString(line + "\n")
		if r.toggle != nil || r.edit != nil || r.action != nil {
			m.addHit(0, max(m.width, 80), y, "setting", i)
		}
	}
	if m.editIndex >= 0 {
		display := m.input
		if m.cfgRows[m.editIndex].secret {
			display = strings.Repeat("•", len([]rune(display)))
		}
		fmt.Fprintf(b, "\n %s: %s▏\n Enter save · Esc cancel\n", m.editing, display)
	} else {
		b.WriteString("\n Click or Enter to edit · scroll to browse · regenerate and reconnect to apply\n")
	}
}
func (m *model) renderSettingEditor(b *strings.Builder, startY int) {
	r := m.cfgRows[m.editIndex]
	p := newPaneWriter(m, b, startY, 76)
	p.line(accent.Render("Edit " + r.label))
	display := m.input
	if r.secret {
		display = strings.Repeat("•", len([]rune(display)))
	}
	p.field(display+"▏", "setting-input", 0, true)
	p.line("Enter save · Esc cancel · Ctrl+U clear")
	p.buttons(control{text: "[ Save ]", action: "setting-save"}, control{text: "[ Cancel ]", action: "setting-cancel"})
	p.end()
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
