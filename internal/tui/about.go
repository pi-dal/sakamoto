package tui

import (
	"embed"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// CC BY 2.0 portrait adaptations. Full provenance is in NOTICE.md and
// docs/portrait-license.md; no photo or network fetch is needed at runtime.
//
//go:embed assets/portrait-*.txt
var portraitAssets embed.FS

type portraitSize struct {
	name          string
	width, height int
}

func choosePortrait(width, height int) portraitSize {
	switch {
	case width >= 100 && height >= 21:
		return portraitSize{"wide", 38, 21}
	case width >= 78 && height >= 16:
		return portraitSize{"compact", 28, 16}
	case height >= 10:
		return portraitSize{"mini", 18, 10}
	case height >= 7:
		return portraitSize{"tiny", 14, 7}
	default:
		return portraitSize{}
	}
}
func portraitRows(size portraitSize) []string {
	if size.name == "" {
		return nil
	}
	raw, err := portraitAssets.ReadFile("assets/portrait-" + size.name + ".txt")
	if err != nil {
		return nil
	}
	rows := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if os.Getenv("TERM") == "dumb" || os.Getenv("SAKAMOTO_ASCII_ART") == "1" {
		replacer := strings.NewReplacer("█", "@", "▓", "#", "▒", "*", "░", ":", "·", ".")
		for i := range rows {
			rows[i] = replacer.Replace(rows[i])
		}
	}
	return rows
}
func appendWrapped(lines []string, text string, width int) []string {
	if text == "" {
		return append(lines, "")
	}
	return append(lines, strings.Split(ansi.Wrap(text, max(12, width), ""), "\n")...)
}

func (m *model) renderAbout(b *strings.Builder, startY int) {
	available := max(0, m.height-startY-2)
	inner := m.width - 4
	var rows []string
	var buttonRow, buttonX, buttonWidth int
	var action string
	if m.aboutCopyright {
		back := "[ Back to About ]"
		buttonRow, buttonX, buttonWidth, action = 0, 1, lipgloss.Width(back), "about-back"
		rows = append(rows, " "+muted.Render(back), "", " "+accent.Render("Copyright & attribution"), "")
		for _, paragraph := range []string{
			"Software © 2026 Guanye Li (pi-dal). Code license: GPL-3.0-or-later.",
			"sing-box upstream © nekohasekai and contributors. See NOTICE.md for its GPL-3.0-or-later terms.",
			"The name sakamoto honors musician and composer Ryuichi Sakamoto (1952–2023). This independent project is not affiliated with or endorsed by him, his family, estate, or representatives.",
			"Portrait photo: Joi Ito. Wikimedia Commons crop: Solid State Survivor. Source: RyuichiSakamoto2007.jpg.",
			"Pixel-art adaptation: tighter crop, grayscale downsampling and Unicode blocks by the sakamoto project. Image license: Creative Commons Attribution 2.0 (CC BY 2.0).",
			"Source: https://commons.wikimedia.org/wiki/File:RyuichiSakamoto2007.jpg",
			"License: https://creativecommons.org/licenses/by/2.0/",
		} {
			for _, line := range strings.Split(ansi.Wrap(paragraph, max(16, inner-2), ""), "\n") {
				rows = append(rows, " "+line)
			}
			rows = append(rows, "")
		}
	} else {
		art := choosePortrait(m.width, available)
		image := portraitRows(art)
		textWidth := inner - art.width - 4
		if len(image) == 0 {
			textWidth = inner - 2
		}
		text := []string{accent.Render("Ryuichi Sakamoto"), "1952–2023 · musician and composer", ""}
		if available < 11 {
			text = appendWrapped(text, "Named in tribute to his art and curiosity.", textWidth)
		} else {
			text = appendWrapped(text, "sakamoto is named in tribute to his work across music, technology and experimentation.", textWidth)
			text = append(text, "")
			text = appendWrapped(text, "Independent open-source software. No affiliation or endorsement is implied.", textWidth)
			text = append(text, "")
			text = appendWrapped(text, "Portrait: Joi Ito / Solid State Survivor · CC BY 2.0", textWidth)
		}
		text = append(text, "")
		button := "[ Copyright & attribution → ]"
		buttonRow, buttonX, buttonWidth, action = len(text), art.width+4, lipgloss.Width(button), "about-copyright"
		text = append(text, muted.Render(button))
		rowCount := max(len(image), len(text))
		for i := 0; i < rowCount; i++ {
			left := ""
			if i < len(image) {
				left = image[i]
			}
			right := ""
			if i < len(text) {
				right = text[i]
			}
			if len(image) > 0 {
				rows = append(rows, " "+padLine(left, art.width)+"   "+right)
			} else {
				rows = append(rows, " "+right)
			}
		}
		if len(image) == 0 {
			buttonX = 1
		}
	}
	m.aboutScroll = min(max(0, m.aboutScroll), max(0, len(rows)-available))
	end := min(len(rows), m.aboutScroll+available)
	for i := m.aboutScroll; i < end; i++ {
		b.WriteString(rows[i] + "\n")
	}
	if buttonRow >= m.aboutScroll && buttonRow < end {
		m.addHit(buttonX, buttonX+buttonWidth, startY+buttonRow-m.aboutScroll, action, 0)
	}
}
