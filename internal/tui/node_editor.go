package tui

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/pi-dal/sakamoto/internal/desktop"
	"github.com/pi-dal/sakamoto/internal/gen"
)

const sourcesSection = 3

type nodeEditor struct {
	original gen.NodeEntry
	values   []string
	field    int
	cursor   int // rune offset within the focused field
	notice   string
}

var nodeFieldLabels = []string{"Remark / name", "IP / hostname", "Port"}

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
	original := entries[m.nodeCursor]
	endpoint, err := desktop.NodeEndpoint(original.Raw)
	if err != nil {
		m.replaceNodeLink(original)
		m.notice = "This link could not be opened as fields. Paste a replacement or press Esc."
		return
	}
	values := []string{endpoint.Remark, endpoint.Server}
	if endpoint.Port != "" {
		values = append(values, endpoint.Port)
	}
	m.nodeEditor = &nodeEditor{original: original, values: values, cursor: len([]rune(values[0]))}
	m.notice = ""
}

func (m *model) replaceNodeLink(original gen.NodeEntry) {
	m.nodeEditor = nil
	m.editNode = original
	m.importing = true
	m.importKind = "node-edit"
	m.input = original.Raw
	m.revealInput = false
}

func (m *model) moveSourceNode(delta int) {
	entries, err := gen.ReadNodes(m.cfg.NodesFile)
	if err != nil {
		m.notice = "Could not read nodes: " + err.Error()
		return
	}
	m.nodeCursor = max(0, min(len(entries)-1, m.nodeCursor+delta))
	m.pendingDelete = ""
}

func (e *nodeEditor) selectField(field int) {
	e.field = field
	e.cursor = len([]rune(e.values[field]))
}

func (m *model) saveNodeFields() {
	e := m.nodeEditor
	port := ""
	if len(e.values) == 3 {
		port = e.values[2]
	}
	updated, err := desktop.UpdateNodeEndpoint(e.original.Raw, e.values[0], e.values[1], port)
	if err == nil && updated != e.original.Raw {
		err = gen.ChangeNode(m.cfg.NodesFile, e.original.Line, e.original.Raw, updated)
	}
	if err != nil {
		e.notice = err.Error()
		return
	}
	m.nodeEditor = nil
	m.notice = "Node saved. Regenerate and reconnect to apply."
}

func (m *model) handleNodeFields(k tea.KeyMsg) {
	e := m.nodeEditor
	value := []rune(e.values[e.field])
	switch k.String() {
	case "esc":
		m.nodeEditor = nil
		return
	case "ctrl+s":
		m.saveNodeFields()
		return
	case "tab", "enter", "down":
		e.selectField((e.field + 1) % len(e.values))
		return
	case "shift+tab", "up":
		e.selectField((e.field + len(e.values) - 1) % len(e.values))
		return
	case "left":
		e.cursor = max(0, e.cursor-1)
	case "right":
		e.cursor = min(len(value), e.cursor+1)
	case "home", "ctrl+a":
		e.cursor = 0
	case "end", "ctrl+e":
		e.cursor = len(value)
	case "ctrl+u":
		value, e.cursor = nil, 0
	case "backspace":
		if e.cursor > 0 {
			e.cursor--
			value = append(value[:e.cursor], value[e.cursor+1:]...)
		}
	case "delete":
		if e.cursor < len(value) {
			value = append(value[:e.cursor], value[e.cursor+1:]...)
		}
	default:
		if k.Type != tea.KeyRunes || len(value)+len(k.Runes) > 8192 {
			return
		}
		for _, r := range k.Runes {
			if unicode.IsControl(r) {
				return
			}
		}
		value = append(append(append([]rune(nil), value[:e.cursor]...), k.Runes...), value[e.cursor:]...)
		e.cursor += len(k.Runes)
	}
	if updated := string(value); updated != e.values[e.field] {
		e.values[e.field] = updated
		e.notice = ""
	}
}

func (m *model) clickNodeFields(x, y int) {
	for _, h := range m.hits {
		if h.y != y || x < h.x0 || x >= h.x1 {
			continue
		}
		switch h.action {
		case "node-field":
			m.nodeEditor.selectField(h.index)
		case "node-save":
			m.saveNodeFields()
		case "node-cancel":
			m.nodeEditor = nil
		case "node-link":
			m.replaceNodeLink(m.nodeEditor.original)
		}
		return
	}
}

func (m *model) renderNodeFields(b *strings.Builder, startY int) {
	e := m.nodeEditor
	p := newPaneWriter(m, b, startY, 76)
	p.line(accent.Render("Edit node"))
	p.line("Tab / ↑↓ fields  ·  Ctrl+S save  ·  Esc cancel")
	for i, value := range e.values {
		// Imported remarks can contain terminal control characters. Keep the
		// original for validation but never render them as terminal input.
		runes := []rune(value)
		for j, r := range runes {
			if unicode.IsControl(r) {
				runes[j] = '�'
			}
		}
		value = string(runes)
		if i == e.field {
			before := string(runes[:e.cursor])
			width := max(1, p.width-23)
			before = ansi.TruncateLeft(before, max(0, ansi.StringWidth(before)-width), "")
			value = before + "▏" + string(runes[e.cursor:])
		}
		p.field(padLine(nodeFieldLabels[i], 18)+value, "node-field", i, i == e.field)
	}
	if e.notice != "" {
		p.line(bad.Render(e.notice))
	} else if len(e.values) == 2 {
		p.line("Port ranges are kept from the share link.")
	} else {
		p.line("Save stages changes. Regenerate to use them.")
	}
	p.buttons(control{text: "[ Save ]", action: "node-save"}, control{text: "[ Cancel ]", action: "node-cancel"}, control{text: "[ Replace link ]", action: "node-link"})
	p.end()
}
