package tui

import (
	"strings"
	"testing"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestAboutFitsTerminalSizesAndOpensCopyright(t *testing.T) {
	for _, size := range []struct{ width, height int }{{64, 16}, {80, 24}, {120, 36}} {
		m := testModel(t)
		m.width, m.height, m.page = size.width, size.height, aboutPage
		view := m.View()
		if !strings.Contains(view, "Ryuichi Sakamoto") || !strings.Contains(view, "▓") {
			t.Fatalf("About portrait or name missing at %dx%d", size.width, size.height)
		}
		for _, r := range view {
			if unicode.Is(unicode.Han, r) {
				t.Fatal("About must remain English-only")
			}
		}
		for _, line := range strings.Split(view, "\n") {
			if lipgloss.Width(line) > size.width {
				t.Fatalf("About overflow at %dx%d: width %d", size.width, size.height, lipgloss.Width(line))
			}
		}
		if size.width == 80 {
			m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		} else {
			var target hit
			for _, h := range m.hits {
				if h.action == "about-copyright" {
					target = h
					break
				}
			}
			if target.action == "" {
				t.Fatalf("copyright link not reachable at %dx%d", size.width, size.height)
			}
			m.click(target.x0, target.y)
		}
		if !m.aboutCopyright {
			t.Fatal("About did not open Copyright")
		}
		credits := m.View()
		if !strings.Contains(credits, "Copyright & attribution") {
			t.Fatal("copyright heading missing")
		}
		for _, r := range credits {
			if unicode.Is(unicode.Han, r) {
				t.Fatal("Copyright must remain English-only")
			}
		}
		m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if m.aboutCopyright {
			t.Fatal("Esc did not return to About")
		}
	}
}
func TestPortraitAssetsAndASCIIAlternative(t *testing.T) {
	for _, size := range []portraitSize{{"wide", 38, 21}, {"compact", 28, 16}, {"mini", 18, 10}, {"tiny", 14, 7}} {
		rows := portraitRows(size)
		if len(rows) != size.height {
			t.Fatalf("%s rows: %d", size.name, len(rows))
		}
		for _, row := range rows {
			if lipgloss.Width(row) != size.width {
				t.Fatalf("%s portrait width: %d", size.name, lipgloss.Width(row))
			}
		}
	}
	t.Setenv("SAKAMOTO_ASCII_ART", "1")
	art := strings.Join(portraitRows(portraitSize{"tiny", 14, 7}), "")
	if strings.ContainsAny(art, "█▓▒░·") || !strings.Contains(art, "@") {
		t.Fatal("ASCII portrait fallback is not usable")
	}
}
func TestCopyrightTextAndScroll(t *testing.T) {
	m := testModel(t)
	m.page, m.aboutCopyright, m.width, m.height = aboutPage, true, 80, 24
	combined := ""
	for i := 0; i < 22; i++ {
		combined += m.View()
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	for _, text := range []string{"Guanye Li", "Joi Ito", "Solid State Survivor", "CC BY 2.0", "commons.wikimedia.org", "creativecommons.org", "not affiliated"} {
		if !strings.Contains(combined, text) {
			t.Fatalf("copyright information missing: %s", text)
		}
	}
}
