package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/gen"
	"github.com/pi-dal/sakamoto/internal/security"
	"github.com/pi-dal/sakamoto/internal/svc"
)

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
	path := m.cfgPath
	return func() tea.Msg {
		if cmd == "connect" {
			out, rotated, err := security.ConnectWithPending(path)
			return actionMsg{text: strings.TrimSpace(out), err: err, apiRotated: rotated}
		}
		out, err := svc.Send(cmd)
		return actionMsg{text: strings.TrimSpace(out), err: err}
	}
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
		if err := regenerate(path); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: "配置已验证并生成；断开再连接以应用"}
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
