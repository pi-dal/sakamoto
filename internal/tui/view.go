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
			Type       string `json:"type"`
			Detour     string `json:"detour"`
			Server     string `json:"server"`
			ServerPort int    `json:"server_port"`
		} `json:"outbounds"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return "配置错误"
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
	if m.serviceState == "connected" && m.networkState == "待确认" {
		stateLabel = "● TUN 已启动 · 检测待重试"
		state = accent.Render(stateLabel)
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
