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
		// Keep the cursor in the scrolled viewport rather than snapping back.
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
	if m.page == aboutPage {
		m.aboutScroll = max(0, m.aboutScroll+n)
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
			m.aboutCopyright, m.aboutScroll = false, 0
		case "about-copyright":
			m.aboutCopyright, m.aboutScroll = true, 0
		case "about-back":
			m.aboutCopyright, m.aboutScroll = false, 0
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
			m.notice = "Paste a node share link (input hidden)"
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
			m.notice = "Enter a subscription: name|URL"
		case "delete-sub":
			return m.deleteSelectedSource()
		case "generate":
			return m.generate()
		case "import":
			m.importing = true
			m.importKind = "conf"
			m.input = ""
			m.notice = "Enter a Shadowrocket .conf HTTPS URL or local path"
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
		m.notice = "Input canceled"
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
				m.notice = "Edit failed: " + err.Error()
				return nil
			}
			m.importing = false
			m.importKind = ""
			m.input = ""
			m.notice = "Node updated; regenerate in Config and reconnect to apply"
			return nil
		}
		if m.importKind == "node" {
			if source == "" {
				m.notice = "Node share link cannot be empty"
				return nil
			}
			if err := gen.ChangeNode(m.cfg.NodesFile, 0, "", source); err != nil {
				m.notice = "Could not save node: " + err.Error()
				return nil
			}
			m.importing = false
			m.importKind = ""
			m.input = ""
			m.notice = "Node added; press g to regenerate config"
			return nil
		}
		if m.importKind == "subscription" || m.importKind == "subscription-edit" {
			parts := strings.SplitN(source, "|", 2)
			if len(parts) != 2 {
				m.notice = "Format: name|subscription URL"
				return nil
			}
			if err := gen.ValidSource(strings.TrimSpace(parts[1])); err != nil || !strings.HasPrefix(strings.TrimSpace(parts[1]), "http") {
				m.notice = "Subscription must be an HTTP(S) URL"
				return nil
			}
			if strings.TrimSpace(parts[0]) == "" {
				m.notice = "Subscription name cannot be empty"
				return nil
			}
			old := append([]config.SubSource(nil), m.cfg.Subscriptions...)
			entry := config.SubSource{Name: strings.TrimSpace(parts[0]), URL: strings.TrimSpace(parts[1])}
			if m.importKind == "subscription-edit" {
				if m.sourceCursor < 0 || m.sourceCursor >= len(m.cfg.Subscriptions) {
					m.notice = "Subscription not found"
					return nil
				}
				m.cfg.Subscriptions[m.sourceCursor] = entry
			} else {
				m.cfg.Subscriptions = append(m.cfg.Subscriptions, entry)
			}
			if err := m.cfg.Save(m.cfgPath); err != nil {
				m.cfg.Subscriptions = old
				m.notice = "Could not save subscription: " + err.Error()
				return nil
			}
			m.importing = false
			m.importKind = ""
			m.input = ""
			m.notice = "Subscription added; press g to generate nodes"
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
		m.notice = "Fetching the main config and included files…"
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
		m.notice = "Could not read nodes: " + err.Error()
		return
	}
	if m.nodeCursor >= len(entries) {
		m.notice = "Select a node first"
		return
	}
	m.editNode = entries[m.nodeCursor]
	m.importing = true
	m.importKind = "node-edit"
	m.input = m.editNode.Raw
	m.revealInput = false
	m.notice = "Replace node link (hidden by default; Ctrl+U clears, Ctrl+R reveals)"
}
func (m *model) deleteSelectedNode() tea.Cmd {
	entries, err := gen.ReadNodes(m.cfg.NodesFile)
	if err != nil {
		m.notice = err.Error()
		return nil
	}
	if m.nodeCursor >= len(entries) {
		m.notice = "Select a node first"
		return nil
	}
	e := entries[m.nodeCursor]
	key := fmt.Sprintf("node:%d:%s", e.Line, e.Raw)
	if m.pendingDelete != key {
		m.pendingDelete = key
		m.notice = "Click Remove again to confirm node deletion: " + e.Tag
		return nil
	}
	m.pendingDelete = ""
	if err := gen.ChangeNode(m.cfg.NodesFile, e.Line, e.Raw, ""); err != nil {
		m.notice = err.Error()
	} else {
		m.notice = "Removed node " + e.Tag + "; regenerate and reconnect to apply"
		if m.nodeCursor > 0 {
			m.nodeCursor--
		}
	}
	return nil
}
func (m *model) beginSubscriptionEdit() {
	if m.sourceCursor >= len(m.cfg.Subscriptions) {
		m.notice = "Select a subscription first"
		return
	}
	s := m.cfg.Subscriptions[m.sourceCursor]
	m.importing = true
	m.importKind = "subscription-edit"
	m.input = s.Name + "|" + s.URL
	m.notice = "Edit subscription (name|URL; URLs may contain credentials)"
}
func (m *model) deleteSelectedSource() tea.Cmd {
	if m.sourceCursor >= len(m.cfg.Subscriptions) {
		m.notice = "Select a subscription first"
		return nil
	}
	s := m.cfg.Subscriptions[m.sourceCursor]
	key := "subscription:" + s.Name + ":" + s.URL
	if m.pendingDelete != key {
		m.pendingDelete = key
		m.notice = "Click Remove again to confirm subscription deletion: " + s.Name
		return nil
	}
	m.pendingDelete = ""
	old := append([]config.SubSource(nil), m.cfg.Subscriptions...)
	m.cfg.Subscriptions = append(m.cfg.Subscriptions[:m.sourceCursor], m.cfg.Subscriptions[m.sourceCursor+1:]...)
	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.cfg.Subscriptions = old
		m.notice = "Delete failed: " + err.Error()
	} else {
		m.notice = "Removed subscription " + s.Name + "; regenerate to apply"
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
			m.notice = "Save failed: " + err.Error()
			return nil
		}
		m.notice = "Saved; regenerate in Config and reconnect to apply"
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
		return actionMsg{text: "Connection closed", err: err}
	}
}

func (m *model) toggleConnection() tea.Cmd {
	cmd := "connect"
	if m.serviceState == "connected" {
		cmd = "disconnect"
	}
	if cmd == "connect" && (m.shadowrocket || shadowrocketVPNConnected()) {
		m.shadowrocket = true
		m.notice = "Shadowrocket VPN is still connected; disconnect it before clicking Connect"
		return nil
	}
	m.notice = map[string]string{"connect": "Connecting…", "disconnect": "Disconnecting…"}[cmd]
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
		m.notice = "Connect before selecting a node"
		return nil
	}
	c, g, t := m.conn, r.group.Tag, r.item.Tag
	if r.group.Selectable {
		return func() tea.Msg {
			err := c.SelectOutbound(context.Background(), g, t)
			return actionMsg{text: "Selected " + t, err: err}
		}
	}
	// Select an auto-group leaf via ManualPick before switching MainProxy.
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
		m.notice = "This node is unavailable in ManualPick"
		return nil
	}
	return func() tea.Msg {
		if err := c.SelectOutbound(context.Background(), "ManualPick", t); err != nil {
			return actionMsg{err: err}
		}
		err := c.SelectOutbound(context.Background(), "MainProxy", "ManualPick")
		return actionMsg{text: "Manually selected " + t, err: err}
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
		return actionMsg{text: "Testing latency: " + t, err: err}
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
		m.notice = "Connect before testing latency"
		return nil
	}
	if m.batch != nil {
		m.notice = "Testing: " + m.batch.message()
		return nil
	}
	batch := newTestBatch(m.groups, time.Now())
	if len(batch.tags) == 0 {
		m.notice = "No proxy nodes available for testing"
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
			time.Sleep(40 * time.Millisecond) // Avoid hitting every node and test URL simultaneously.
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
		return actionMsg{text: "Mode: " + next, err: err}
	}
}
func (m *model) toggleSetting(i int) tea.Cmd {
	if i < 0 || i >= len(m.cfgRows) || m.cfgRows[i].toggle == nil {
		return nil
	}
	r := m.cfgRows[i]
	if r.label == "Sync node sources to iCloud" && !m.cfg.ICloud.Enabled && m.pendingDelete != "icloud:confirm" {
		m.pendingDelete = "icloud:confirm"
		m.notice = "Click again to confirm uploading selected sources (possibly including node passwords) to iCloud Drive"
		return nil
	}
	m.pendingDelete = ""
	r.toggle()
	if err := m.cfg.Save(m.cfgPath); err != nil {
		r.toggle()
		m.notice = "Save failed: " + err.Error()
		return nil
	}
	m.notice = "Saved · regenerate to apply on the next connection"
	return nil
}
func (m *model) generate() tea.Cmd {
	m.notice = "Refreshing subscriptions and generating config…"
	path := m.cfgPath
	return func() tea.Msg {
		if err := regenerate(path); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{text: "Config validated and generated; disconnect and reconnect to apply"}
	}
}
func (m *model) editConfig() tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, m.cfgPath)
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return actionMsg{text: "Config editor closed; press g to generate", err: err} })
}
