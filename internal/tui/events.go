package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/svc"
	"github.com/sagernet/sing-box/daemon"
)

// Update routes Bubble Tea events to one focused state transition per event family.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
	case tickMsg:
		if m.batch != nil {
			m.batch.expire(time.Now())
			m.refreshTestProgress()
		}
		return m, tea.Batch(tick(), queryService)
	case serviceMsg:
		return m, m.onService(v)
	case networkMsg:
		m.onNetwork(v)
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
		m.onConnections((*daemon.ConnectionEvents)(v))
	case logMsg:
		m.onLogs((*daemon.Log)(v))
	case testDispatchMsg:
		if m.batch == v.batch {
			for tag := range v.errors {
				m.batch.results[tag] = -1
			}
			m.refreshTestProgress()
		}
	case importMsg:
		m.onImport(v)
	case actionMsg:
		m.onAction(v)
		return m, queryService
	case tea.MouseMsg:
		return m, m.onMouse(v)
	case tea.KeyMsg:
		return m, m.onKey(v)
	}
	return m, nil
}

func (m *model) onService(v serviceMsg) tea.Cmd {
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
		return func() tea.Msg {
			_, err := svc.Send("disconnect")
			return actionMsg{text: "已停用 sakamoto；请先断开 Shadowrocket VPN 再连接", err: err}
		}
	}
	if m.serviceState == "connected" && !m.shadowrocket && (previous != "connected" || wasSR) && !m.netChecking {
		m.netChecking = true
		m.networkState = "检查中"
		expected := m.exitLabel
		return func() tea.Msg { return diagnoseNetwork(expected) }
	}
	return nil
}
func (m *model) onNetwork(v networkMsg) {
	m.netChecking = false
	if v.err != nil {
		m.networkState = "不可用"
		m.notice = "TUN 已启动但系统流量不通：" + v.err.Error()
	} else {
		m.networkState = "可用 · 出口 " + v.ip
		m.notice = "网络已验证：出口 " + v.ip
	}
}
func (m *model) onConnections(ev *daemon.ConnectionEvents) {
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
}
func (m *model) onLogs(l *daemon.Log) {
	if l.Reset_ {
		m.logs = nil
	}
	for _, x := range l.Messages {
		m.logs = append(m.logs, x.Message)
	}
	if len(m.logs) > 100 {
		m.logs = m.logs[len(m.logs)-100:]
	}
}
func (m *model) onImport(v importMsg) {
	m.importBusy = false
	if v.err != nil {
		m.notice = "导入失败：" + v.err.Error()
		return
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
}
func (m *model) onAction(v actionMsg) {
	if v.apiRotated {
		if latest, err := config.Load(m.cfgPath); err == nil {
			m.cfg.API.Secret = latest.API.Secret
			m.cfg.API.URL = latest.API.URL
		}
	}
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
}
func (m *model) onMouse(v tea.MouseMsg) tea.Cmd {
	if m.importBusy {
		return nil
	}
	if v.Action == tea.MouseActionMotion {
		m.hoverX, m.hoverY = v.X, v.Y
	}
	if v.Action == tea.MouseActionPress {
		switch v.Button {
		case tea.MouseButtonLeft:
			return m.click(v.X, v.Y)
		case tea.MouseButtonRight:
			m.rightClick(v.X, v.Y)
		case tea.MouseButtonWheelDown:
			m.scrollBy(3)
		case tea.MouseButtonWheelUp:
			m.scrollBy(-3)
		}
	}
	return nil
}
func (m *model) onKey(v tea.KeyMsg) tea.Cmd {
	if m.importing {
		return m.handleImportInput(v)
	}
	if m.editIndex >= 0 {
		return m.handleInput(v)
	}
	switch v.String() {
	case "q", "ctrl+c":
		return tea.Quit
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
		switch m.page {
		case settingsPage:
			return m.activateSetting(m.cfgCursor)
		case homePage:
			return m.selectCurrent()
		case configPage:
			m.configDetail = 0
		}
	case "c", " ":
		return m.toggleConnection()
	case "t":
		return m.testCurrent()
	case "u":
		return m.testAll()
	case "m":
		return m.cycleMode()
	case "g":
		return m.generate()
	case "a":
		if m.page == configPage {
			m.importing = true
			m.importKind = "conf"
			m.input = ""
			m.notice = "输入 Shadowrocket .conf URL 或本地路径"
		}
	case "n":
		if m.page == configPage {
			m.importing = true
			m.importKind = "node"
			m.input = ""
			m.notice = "添加节点分享链接"
		}
	case "d":
		if m.page == configPage && m.configDetail == 3 {
			return m.deleteSelectedNode()
		}
	case "e":
		if m.page == configPage {
			return m.editConfig()
		}
	case "i":
		if m.page == homePage {
			m.detailRow = m.cursor
			m.menuRow = -1
		}
	}
	return nil
}
