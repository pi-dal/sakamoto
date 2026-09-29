package tui

import (
	"encoding/hex"
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
		if !strings.Contains(view, "Ryuichi Sakamoto") || !strings.ContainsAny(view, "▓▀") {
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
func TestAboutKeyboardOpensCredits(t *testing.T) {
	m := testModel(t)
	m.page = aboutPage
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.aboutCopyright {
		t.Fatal("Enter did not open Copyright")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.aboutCopyright {
		t.Fatal("Esc did not return to About")
	}
}

func TestPortraitAssetsAndASCIIAlternative(t *testing.T) {
	sizes := []portraitSize{{"wide", 56, 21}, {"compact", 46, 16}, {"mini", 28, 10}, {"tiny", 18, 7}}
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")
	for _, size := range sizes {
		rows := portraitRows(size)
		if len(rows) != size.height {
			t.Fatalf("%s rows: %d", size.name, len(rows))
		}
		for _, row := range rows {
			if lipgloss.Width(row) != size.width {
				t.Fatalf("%s width: %d", size.name, lipgloss.Width(row))
			}
			if !strings.Contains(row, "\x1b[38;2;") || !strings.Contains(row, "▀") || !strings.HasSuffix(row, "\x1b[0m") {
				t.Fatalf("%s truecolor row invalid", size.name)
			}
		}
	}
	t.Setenv("COLORTERM", "")
	if row := portraitRows(sizes[1])[0]; !strings.Contains(row, "\x1b[38;5;") {
		t.Fatal("ANSI-256 fallback unavailable")
	}
	t.Setenv("NO_COLOR", "1")
	if row := portraitRows(sizes[1])[0]; strings.Contains(row, "\x1b[") {
		t.Fatal("NO_COLOR emitted ANSI escapes")
	}
	t.Setenv("SAKAMOTO_ASCII_ART", "1")
	art := strings.Join(portraitRows(sizes[3]), "")
	if strings.ContainsAny(art, "█▓▒░·") || !strings.Contains(art, "@") {
		t.Fatal("ASCII fallback is not usable")
	}
	// The compact portrait holds two 8-bit image samples per cell rather than
	// reducing the face to the previous handful of block-density levels.
	raw, err := portraitAssets.ReadFile("assets/portrait-compact.gray")
	if err != nil {
		t.Fatal(err)
	}
	levels := map[byte]bool{}
	minLevel, maxLevel := byte(255), byte(0)
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		pixels, err := hex.DecodeString(line)
		if err != nil || len(pixels) != 46*2 {
			t.Fatal("compact pixel matrix malformed", err)
		}
		for _, value := range pixels {
			levels[value] = true
			if value < minLevel {
				minLevel = value
			}
			if value > maxLevel {
				maxLevel = value
			}
		}
	}
	if len(levels) < 100 || minLevel > 40 || maxLevel < 210 {
		t.Fatalf("portrait lost facial contrast/detail: levels=%d range=%d..%d", len(levels), minLevel, maxLevel)
	}
}
func BenchmarkPortraitWideTruecolor(b *testing.B) {
	b.Setenv("TERM", "xterm-256color")
	b.Setenv("COLORTERM", "truecolor")
	for i := 0; i < b.N; i++ {
		if len(portraitRows(portraitSize{"wide", 56, 21})) != 21 {
			b.Fatal("portrait missing")
		}
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
	for _, text := range []string{"pi-dal", "Joi Ito", "Solid State Survivor", "CC BY 2.0", "commons.wikimedia.org", "creativecommons.org", "not affiliated"} {
		if !strings.Contains(combined, text) {
			t.Fatalf("copyright information missing: %s", text)
		}
	}
}
