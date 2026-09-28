package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/sagernet/sing-box/daemon"
)

func testModel(t *testing.T) *model {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfgPath := filepath.Join(dir, "sakamoto.yaml")
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	raw := map[string]any{"route": map[string]any{"final": "Exit"}, "outbounds": []map[string]any{
		{"tag": "MainProxy", "type": "selector", "default": "RealityAuto", "outbounds": []string{"RealityAuto", "ManualPick"}},
		{"tag": "RealityAuto", "type": "urltest", "outbounds": []string{"Realm"}},
		{"tag": "ManualPick", "type": "selector", "outbounds": []string{"Realm"}},
		{"tag": "Realm", "type": "vless"},
		{"tag": "Exit", "type": "socks", "server": "203.0.113.10", "server_port": 45510},
	}}
	data, _ := json.Marshal(raw)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	m := &model{cfg: cfg, cfgPath: cfgPath, conns: map[string]*daemon.Connection{}, width: 90, height: 20, serviceState: "disconnected", exitLabel: exitFromConfig(cfgPath), menuRow: -1, detailRow: -1, configDetail: -1, editIndex: -1, hoverX: -1, hoverY: -1}
	m.buildSettings()
	return m
}

func TestDashboardMouse(t *testing.T) {
	m := testModel(t)
	m.groups = []*daemon.Group{
		{Tag: "MainProxy", Type: "selector", Selectable: true, Selected: "RealityAuto", Items: []*daemon.GroupItem{{Tag: "RealityAuto", Type: "urltest"}}},
		{Tag: "RealityAuto", Type: "urltest", Selected: "VMess-DMIT", Items: []*daemon.GroupItem{{Tag: "VMess-DMIT", Type: "vmess", UrlTestDelay: 110}, {Tag: "Azure-JP", Type: "vless", UrlTestDelay: 180}}},
	}
	m.rebuildRows()
	view := m.View()
	if !strings.Contains(view, "默认：VMess-DMIT（待连接）  →  SOCKS 203.0.113.10:45510") {
		t.Fatalf("chain not visible: %s", view)
	}
	if !strings.Contains(view, "已断开") || !strings.Contains(view, "[ 连接 ]") {
		t.Fatal("status/action missing")
	}
	// 点击标签页，点击范围需与 lipgloss 宽度一致。
	var tabHit hit
	for _, h := range m.hits {
		if h.action == "tab" && h.index == settingsPage {
			tabHit = h
			break
		}
	}
	m.click(tabHit.x0, tabHit.y)
	if m.page != settingsPage {
		t.Fatalf("click settings: %d", m.page)
	}
	m.height = 24
	m.View()
	before := m.cfg.BlockSTUN
	var settingHit hit
	for _, h := range m.hits {
		if h.action == "setting" && m.cfgRows[h.index].label == "阻止 STUN / WebRTC" {
			settingHit = h
			break
		}
	}
	if settingHit.action == "" {
		t.Fatal("STUN switch should be visible")
	}
	m.click(settingHit.x0, settingHit.y)
	if m.cfg.BlockSTUN == before {
		t.Fatal("mouse did not toggle STUN")
	}
	loaded, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.BlockSTUN != m.cfg.BlockSTUN {
		t.Fatal("toggle not persisted")
	}
}

func TestListScrollingKeepsCursorAndClickableRows(t *testing.T) {
	m := testModel(t)
	for i := 0; i < 18; i++ {
		tag := string(rune('A' + i))
		m.groups = append(m.groups, &daemon.Group{Tag: tag, Type: "selector", Selectable: true, Items: []*daemon.GroupItem{{Tag: tag + " node", Type: "vless"}}})
	}
	m.rebuildRows()
	m.View()
	m.scrollBy(10)
	view := m.View()
	if m.scroll == 0 || !strings.Contains(view, "节点") {
		t.Fatal("scroll did not move")
	}
	var nodeHit hit
	for _, h := range m.hits {
		if h.action == "node" {
			nodeHit = h
			break
		}
	}
	if nodeHit.action == "" {
		t.Fatal("no visible node hit target")
	}
	m.click(nodeHit.x0, nodeHit.y)
	if m.cursor != nodeHit.index {
		t.Fatalf("clicked row %d, cursor %d", nodeHit.index, m.cursor)
	}
	for _, h := range m.hits {
		if h.action == "node" && h.y >= m.height {
			t.Fatalf("hit offscreen: %d", h.y)
		}
	}
}

func TestVPNConflictAndFailedNodeLabels(t *testing.T) {
	m := testModel(t)
	m.groups = offlineGroups(m.cfgPath)
	m.rebuildRows()
	m.Update(serviceMsg{state: "connected", shadowrocket: true})
	if !strings.Contains(m.View(), "VPN 冲突") {
		t.Fatal("TUN conflict not visible")
	}
	m.Update(serviceMsg{state: "disconnected", shadowrocket: true})
	if cmd := m.toggleConnection(); cmd != nil || !strings.Contains(m.notice, "先在 Shadowrocket 里断开") {
		t.Fatal("second TUN was not blocked", m.notice)
	}
	m.Update(serviceMsg{state: "connected", shadowrocket: false}) // do not execute network diagnostic command
	m.lastTest = map[string]int32{"Realm": -1}
	if !strings.Contains(m.View(), "失败/超时") {
		t.Fatal("tested-but-failed node shown as untested")
	}
}

func TestFramedLayoutAtTerminalSizes(t *testing.T) {
	for _, size := range [][2]int{{110, 30}, {72, 20}, {64, 16}} {
		m := testModel(t)
		m.width = size[0]
		m.height = size[1]
		m.groups = offlineGroups(m.cfgPath)
		m.rebuildRows()
		for page := homePage; page <= settingsPage; page++ {
			m.page = page
			v := m.View()
			lines := strings.Split(v, "\n")
			if len(lines) != m.height {
				t.Fatalf("%dx%d page %d: got %d lines", m.width, m.height, page, len(lines))
			}
			for i, line := range lines {
				if lipgloss.Width(line) > m.width {
					t.Fatalf("%dx%d page %d row %d overflow: %d", m.width, m.height, page, i, lipgloss.Width(line))
				}
			}
			if !strings.Contains(v, "[ Home ]") || !strings.Contains(v, "┌─ ") {
				t.Fatal("framed tabs missing")
			}
		}
	}
}

func TestMouseHoverContextAndDetail(t *testing.T) {
	m := testModel(t)
	m.height = 24
	m.groups = offlineGroups(m.cfgPath)
	m.rebuildRows()
	m.View()
	var target hit
	for _, h := range m.hits {
		if h.action == "node" && m.rows[h.index].item.Tag == "Realm" {
			target = h
			break
		}
	}
	if target.action == "" {
		t.Fatal("Realm node not rendered")
	}
	m.Update(tea.MouseMsg{Action: tea.MouseActionMotion, X: target.x0 + 2, Y: target.y})
	m.View()
	if !m.hovered("node", target.index) {
		t.Fatal("hover not highlighted")
	}
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonRight, X: target.x0 + 2, Y: target.y})
	if m.menuRow != target.index {
		t.Fatalf("right-click menu: %d", m.menuRow)
	}
	m.View()
	var detail hit
	for _, h := range m.hits {
		if h.action == "menu-detail" {
			detail = h
			break
		}
	}
	if detail.action == "" {
		t.Fatal("context menu not clickable")
	}
	m.click(detail.x0, detail.y)
	if m.detailRow != target.index {
		t.Fatal("detail not opened")
	}
	view := m.View()
	if !strings.Contains(view, "Realm") || !strings.Contains(view, "服务器") {
		t.Fatal("node details missing")
	}
	var back hit
	for _, h := range m.hits {
		if h.action == "back" {
			back = h
			break
		}
	}
	m.click(back.x0, back.y)
	if m.detailRow != -1 {
		t.Fatal("back not working")
	}
}

func TestMouseConfigAndSettingEditor(t *testing.T) {
	m := testModel(t)
	m.height = 28
	m.page = configPage
	m.View()
	var section hit
	for _, h := range m.hits {
		if h.action == "section" && h.index == 3 {
			section = h
			break
		}
	}
	if section.action == "" {
		t.Fatal("config sections not clickable")
	}
	m.click(section.x0, section.y)
	if m.configDetail != 3 || !strings.Contains(m.View(), "手动节点（点击后用上方按钮编辑/删除）") {
		t.Fatal("section detail missing")
	}
	m.page = settingsPage
	m.View()
	var port hit
	for _, h := range m.hits {
		if h.action == "setting" && m.cfgRows[h.index].label == "监听端口" {
			port = h
			break
		}
	}
	if port.action == "" {
		t.Fatal("port edit not clickable")
	}
	m.click(port.x0, port.y)
	if m.editIndex != port.index {
		t.Fatal("editor not opened")
	}
	m.input = "12081"
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.cfg.MixedInbound.Port != 12081 || m.editIndex != -1 {
		t.Fatal("port not saved")
	}
	loaded, err := config.Load(m.cfgPath)
	if err != nil || loaded.MixedInbound.Port != 12081 {
		t.Fatal("port not persisted", err)
	}
}

func TestMouseNodeEditDeleteAndFallbackSetting(t *testing.T) {
	m := testModel(t)
	m.page = configPage
	m.height = 30
	m.configDetail = 3
	m.cfg.NodesFile = filepath.Join(t.TempDir(), "nodes.txt")
	link := "vless://11111111-2222-3333-4444-555555555555@203.0.113.10:443#East"
	if err := os.WriteFile(m.cfg.NodesFile, []byte("# keep\n"+link+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m.View()
	var node, edit, del hit
	for _, h := range m.hits {
		switch h.action {
		case "node-source":
			node = h
		case "edit-node":
			edit = h
		case "delete-node":
			del = h
		}
	}
	if node.action == "" || edit.action == "" || del.action == "" {
		t.Fatalf("node controls missing: %+v", m.hits)
	}
	m.click(node.x0, node.y)
	m.click(edit.x0, edit.y)
	if m.importKind != "node-edit" || !m.importing {
		t.Fatal("node editor not opened")
	}
	if strings.Contains(m.View(), "11111111-2222") {
		t.Fatal("secret shown in editor")
	}
	m.input = "vless://11111111-2222-3333-4444-555555555555@203.0.113.11:443#West"
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	contents, _ := os.ReadFile(m.cfg.NodesFile)
	if !strings.Contains(string(contents), "#West") || !strings.Contains(string(contents), "# keep") {
		t.Fatal("edit did not preserve source comments")
	}
	m.View()
	for _, h := range m.hits {
		if h.action == "delete-node" {
			del = h
		}
	}
	m.click(del.x0, del.y)
	contents, _ = os.ReadFile(m.cfg.NodesFile)
	if !strings.Contains(string(contents), "#West") {
		t.Fatal("delete was not confirmed")
	}
	m.View()
	for _, h := range m.hits {
		if h.action == "delete-node" {
			del = h
		}
	}
	m.click(del.x0, del.y)
	contents, _ = os.ReadFile(m.cfg.NodesFile)
	if strings.Contains(string(contents), "#West") {
		t.Fatal("node not deleted")
	}
	m.page = settingsPage
	for i, r := range m.cfgRows {
		if r.label == "Reality 自动回落" {
			m.cfgCursor = i
			break
		}
	}
	m.View()
	var fallback hit
	for _, h := range m.hits {
		if h.action == "setting" && m.cfgRows[h.index].label == "Reality 自动回落" {
			fallback = h
			break
		}
	}
	if fallback.action == "" {
		t.Fatal("fallback setting not clickable")
	}
	m.click(fallback.x0, fallback.y)
	if m.cfg.FallbackEnabled {
		t.Fatal("fallback toggle not saved")
	}
}

func TestSubscriptionEditDeleteAndRedactedDisplay(t *testing.T) {
	m := testModel(t)
	m.height = 32
	m.page = configPage
	m.configDetail = 3
	m.cfg.Subscriptions = []config.SubSource{{Name: "test", URL: "https://example.com/private/token123?token=hidden"}}
	m.View()
	if strings.Contains(m.View(), "token123") || strings.Contains(m.View(), "hidden") {
		t.Fatal("subscription secret leaked on screen")
	}
	var edit, del hit
	for _, h := range m.hits {
		switch h.action {
		case "edit-sub":
			edit = h
		case "delete-sub":
			del = h
		}
	}
	if edit.action == "" || del.action == "" {
		t.Fatal("subscription controls not visible")
	}
	m.click(edit.x0, edit.y)
	if m.importKind != "subscription-edit" {
		t.Fatal("edit form not opened")
	}
	if strings.Contains(m.View(), "token123") {
		t.Fatal("subscription edit revealed credentials")
	}
	m.input = "renamed|https://example.com/new/token?flag=sr"
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.cfg.Subscriptions[0].Name != "renamed" {
		t.Fatal("subscription edit not applied")
	}
	m.View()
	for _, h := range m.hits {
		if h.action == "delete-sub" {
			del = h
		}
	}
	m.click(del.x0, del.y)
	if len(m.cfg.Subscriptions) != 1 {
		t.Fatal("delete without confirmation")
	}
	m.View()
	for _, h := range m.hits {
		if h.action == "delete-sub" {
			del = h
		}
	}
	m.click(del.x0, del.y)
	if len(m.cfg.Subscriptions) != 0 {
		t.Fatal("subscription not deleted")
	}
}

func TestMouseDataConnectionDetail(t *testing.T) {
	m := testModel(t)
	m.page = dataPage
	m.height = 30
	m.conns["conn-1"] = &daemon.Connection{Id: "conn-1", Domain: "example.org", Destination: "93.184.215.14:443", Outbound: "Exit", CreatedAt: 10}
	m.View()
	var rowHit hit
	for _, h := range m.hits {
		if h.action == "conn" {
			rowHit = h
			break
		}
	}
	if rowHit.action == "" {
		t.Fatal("connection row not clickable")
	}
	m.click(rowHit.x0, rowHit.y)
	if m.selectedConn != "conn-1" {
		t.Fatal("connection detail not selected")
	}
	m.View()
	var closeHit hit
	for _, h := range m.hits {
		if h.action == "close-conn" {
			closeHit = h
			break
		}
	}
	if closeHit.action == "" {
		t.Fatal("close connection action not clickable")
	}
	m.click(closeHit.x0, closeHit.y) // offline client: safe no-op
	var back hit
	for _, h := range m.hits {
		if h.action == "back" {
			back = h
			break
		}
	}
	m.click(back.x0, back.y)
	if m.selectedConn != "" {
		t.Fatal("connection back not working")
	}
}

func TestMouseImportRemoteConfWithInclude(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/macOS.conf":
			w.Write([]byte("[General]\ninclude=ad.conf\n[Rule]\nDOMAIN-SUFFIX,app.example,PROXY\n"))
		case "/ad.conf":
			w.Write([]byte("[Rule]\nDOMAIN-SUFFIX,ads.example,REJECT\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	m := testModel(t)
	m.page = configPage
	m.height = 28
	m.cfg.NodesFile = filepath.Join(t.TempDir(), "none.txt")
	m.cfg.SRJSONPath = filepath.Join(t.TempDir(), "none.json")
	m.cfg.TailscaleOptimize = false
	if err := m.cfg.Save(m.cfgPath); err != nil {
		t.Fatal(err)
	}
	m.View()
	var button hit
	for _, h := range m.hits {
		if h.action == "import" {
			button = h
			break
		}
	}
	if button.action == "" {
		t.Fatal("import button absent")
	}
	m.click(button.x0, button.y)
	if !m.importing {
		t.Fatal("import form did not open")
	}
	m.input = server.URL + "/macOS.conf"
	cmd := m.handleImportInput(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("import did not start")
	}
	m.Update(cmd())
	if m.importing || m.cfg.ConfPath != server.URL+"/macOS.conf" {
		t.Fatal("import result not persisted", m.notice)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(m.cfgPath), "rules", "reject.srs")); err != nil {
		t.Fatal("ad rule not compiled", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(m.cfgPath), "imports", "macOS.conf")); err != nil {
		t.Fatal("remote source not cached", err)
	}
	if _, err := exec.LookPath("sing-box"); err == nil {
		cmd := exec.Command("sing-box", "check", "-c", filepath.Join(filepath.Dir(m.cfgPath), "config.json"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("generated config invalid: %v: %s", err, out)
		}
	}
	loaded, err := config.Load(m.cfgPath)
	if err != nil || loaded.ConfPath != m.cfg.ConfPath {
		t.Fatal("source not saved", err)
	}
	// 失败时不改已保存的来源。
	m.importing = true
	m.input = server.URL + "/missing.conf"
	cmd = m.handleImportInput(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(cmd())
	if m.cfg.ConfPath != loaded.ConfPath || !m.importing {
		t.Fatal("failed import replaced source")
	}
}

func TestFailedImportKeepsLastWorkingConfig(t *testing.T) {
	if _, err := exec.LookPath("sing-box"); err != nil {
		t.Skip("sing-box unavailable")
	}
	m := testModel(t)
	before, err := os.ReadFile(filepath.Join(filepath.Dir(m.cfgPath), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	m.cfg.NodesFile = filepath.Join(filepath.Dir(m.cfgPath), "bad-nodes.txt")
	if err := os.WriteFile(m.cfg.NodesFile, []byte("vless://not-a-uuid@127.0.0.1:443?security=reality&pbk=invalid&sid=zz#broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m.cfg.SRJSONPath = filepath.Join(filepath.Dir(m.cfgPath), "none.json")
	m.cfg.TailscaleOptimize = false
	if err := m.cfg.Save(m.cfgPath); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[Rule]\nDOMAIN-SUFFIX,ads.example,REJECT\n"))
	}))
	defer server.Close()
	if err := importSource(m.cfgPath, server.URL+"/macOS.conf"); err == nil {
		t.Fatal("invalid outbound should fail sing-box check")
	}
	after, err := os.ReadFile(filepath.Join(filepath.Dir(m.cfgPath), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("last working config was overwritten")
	}
	loaded, err := config.Load(m.cfgPath)
	if err != nil || loaded.ConfPath == server.URL+"/macOS.conf" {
		t.Fatal("failed import saved source", err)
	}
}

func TestExitReadsGeneratedConfig(t *testing.T) {
	m := testModel(t)
	if m.exitLabel != "203.0.113.10:45510" {
		t.Fatal(m.exitLabel)
	}
	m.groups = offlineGroups(m.cfgPath)
	m.rebuildRows()
	if len(m.rows) == 0 {
		t.Fatal("offline group list missing")
	}
	v := m.View()
	if !strings.Contains(v, "入口 默认：") || !strings.Contains(v, "Realm") {
		t.Fatal("offline state not visible")
	}
	for _, h := range m.hits {
		if h.action == "node" {
			m.click(h.x0, h.y)
			if m.notice != "先连接，再选择节点" {
				t.Fatalf("offline click: %q", m.notice)
			}
			return
		}
	}
	t.Fatal("offline nodes not clickable")
}
