package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type control struct {
	text, action string
	index        int
}

// contentWriter derives pointer positions from emitted lines, including blank
// lines and wrapped action rows. Body coordinates become terminal coordinates
// once View adds its two-cell frame padding.
type contentWriter struct {
	m                    *model
	b                    *strings.Builder
	startY, initialLines int
}

func newContentWriter(m *model, b *strings.Builder, startY int) *contentWriter {
	return &contentWriter{m: m, b: b, startY: startY, initialLines: strings.Count(b.String(), "\n")}
}
func (w *contentWriter) y() int           { return w.startY + strings.Count(w.b.String(), "\n") - w.initialLines }
func (w *contentWriter) line(text string) { w.b.WriteString(text + "\n") }
func (w *contentWriter) hovered(x, width, y int) bool {
	return w.m.hoverY == y && w.m.hoverX >= x+2 && w.m.hoverX < x+2+width
}
func (w *contentWriter) button(c control, x int) string {
	width := lipgloss.Width(c.text)
	y := w.y()
	w.m.addHit(x, x+width, y, c.action, c.index)
	if w.hovered(x, width, y) {
		return tab.Render(c.text)
	}
	return muted.Render(c.text)
}

// buttons wraps complete buttons, never registering a target for clipped text.
func (w *contentWriter) buttons(controls ...control) {
	x := 1
	limit := w.m.width - 4
	w.b.WriteByte(' ')
	for i, c := range controls {
		width := lipgloss.Width(c.text)
		if i > 0 && x+1+width > limit {
			w.b.WriteString("\n ")
			x = 1
		} else if i > 0 {
			w.b.WriteByte(' ')
			x++
		}
		w.b.WriteString(w.button(c, x))
		x += width
	}
	w.b.WriteByte('\n')
}
func (w *contentWriter) row(text, action string, index int, selected bool) {
	width := w.m.width - 4
	if selected || w.hovered(0, width, w.y()) {
		text = tab.Render(padLine(text, width))
	}
	w.m.addHit(0, width, w.y(), action, index)
	w.line(text)
}

type paneWriter struct {
	*contentWriter
	left, width int
}

func newPaneWriter(m *model, b *strings.Builder, startY, width int) *paneWriter {
	width = min(width, m.width-8)
	p := &paneWriter{contentWriter: newContentWriter(m, b, startY), left: max(0, (m.width-4-width)/2), width: width}
	p.contentWriter.line(strings.Repeat(" ", p.left) + "╭" + strings.Repeat("─", width-2) + "╮")
	return p
}
func (p *paneWriter) line(text string) {
	p.contentWriter.line(strings.Repeat(" ", p.left) + "│ " + padLine(text, p.width-4) + " │")
}
func (p *paneWriter) field(text, action string, index int, selected bool) {
	x, width, y := p.left+2, p.width-4, p.y()
	if selected || p.hovered(x, width, y) {
		text = tab.Render(padLine(ansi.Truncate(text, width, "…"), width))
	}
	p.m.addHit(x, x+width, y, action, index)
	p.line(text)
}
func (p *paneWriter) buttons(controls ...control) {
	x := p.left + 2
	var texts []string
	for _, c := range controls {
		texts = append(texts, p.button(c, x))
		x += lipgloss.Width(c.text) + 4
	}
	p.line(strings.Join(texts, "    "))
}
func (p *paneWriter) end() {
	p.contentWriter.line(strings.Repeat(" ", p.left) + "╰" + strings.Repeat("─", p.width-2) + "╯")
}
