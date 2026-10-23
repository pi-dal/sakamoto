package menubar

import (
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

const (
	fontTitle = 16
	fontBody  = 13
	fontSmall = 12
	hairline  = 0.5
	radius    = 6
)

// Palette: Radix Colors, commit dbdb85470547c7d34b9001f48fddb08ded335979.
// Steps 1–2: backgrounds; 3–5: control states; 6: separators; 7–8: control
// borders; 9–10: solid accent; 11–12: text. See DESIGN.md and desktop/LICENSE.radix.
// Dark primary hover uses light Indigo 10 to keep white 13px text above 4.5:1.
func radixTheme(dark bool) *ui.Theme {
	if dark {
		return &ui.Theme{
			Dark: true, Background: ui.Hex("#111113"), Surface: ui.Hex("#212225"), SurfaceHover: ui.Hex("#272a2d"), SurfacePressed: ui.Hex("#2e3135"), Border: ui.Hex("#363a3f"),
			Text: ui.Hex("#edeef0"), TextMuted: ui.Hex("#b0b4ba"), Accent: ui.Hex("#3e63dd"), AccentHover: ui.Hex("#3358d4"), AccentPressed: ui.Hex("#3358d4"), AccentText: ui.Hex("#ffffff"),
			Danger: ui.Hex("#ff9592"), Warning: ui.Hex("#ffca16"), Success: ui.Hex("#3dd68c"), Selection: ui.Hex("#253974"), Focus: ui.Hex("#3e63dd"),
			Inverse: ui.Hex("#edeef0"), InverseText: ui.Hex("#111113"), Scrollbar: ui.Hex("#696e77"), ScrollbarWidth: 6, Radius: radius, Spacing: 4, FontSize: fontBody,
		}
	}
	return &ui.Theme{
		Background: ui.Hex("#fcfcfd"), Surface: ui.Hex("#f0f0f3"), SurfaceHover: ui.Hex("#e8e8ec"), SurfacePressed: ui.Hex("#e0e1e6"), Border: ui.Hex("#d9d9e0"),
		Text: ui.Hex("#1c2024"), TextMuted: ui.Hex("#60646c"), Accent: ui.Hex("#3e63dd"), AccentHover: ui.Hex("#3358d4"), AccentPressed: ui.Hex("#3358d4"), AccentText: ui.Hex("#ffffff"),
		Danger: ui.Hex("#ce2c31"), Warning: ui.Hex("#ab6400"), Success: ui.Hex("#218358"), Selection: ui.Hex("#d2deff"), Focus: ui.Hex("#3e63dd"),
		Inverse: ui.Hex("#1c2024"), InverseText: ui.Hex("#fcfcfd"), Scrollbar: ui.Hex("#8b8d98"), ScrollbarWidth: 6, Radius: radius, Spacing: 4, FontSize: fontBody,
	}
}

func controlBorder(c *ui.Context, hover bool) ui.Color {
	if c.Theme().Dark {
		if hover {
			return ui.Hex("#5a6169") // Slate 8
		}
		return ui.Hex("#43484e") // Slate 7
	}
	if hover {
		return ui.Hex("#b9bbc6")
	}
	return ui.Hex("#cdced6")
}

func selectedColors(c *ui.Context) (ui.Color, ui.Color) {
	if c.Theme().Dark {
		return ui.Hex("#182449"), ui.Hex("#9eb1ff") // Indigo 3, 11
	}
	return ui.Hex("#edf2fe"), ui.Hex("#3a5bc7")
}

// MyGo bases retain their keyboard, focus and accessibility behavior. Styling
// lives here because library controls otherwise add mixed colors, 1px borders,
// shadows and animated glass. State changes in this panel are immediate.
func styleControl(c *ui.Context, b ui.Element, variant components.ButtonVariant) ui.Element {
	t := c.Theme()
	bg, fg, border := t.Surface, t.Text, controlBorder(c, b.Hovered())
	switch variant {
	case components.ButtonPrimary:
		bg, fg = t.Accent, t.AccentText
	case components.ButtonGhost:
		bg, border = ui.Transparent, ui.Transparent
	}
	if !b.IsDisabled() {
		if b.Pressed() {
			bg = t.SurfacePressed
			if variant == components.ButtonPrimary {
				bg = t.AccentPressed
			}
		} else if b.Hovered() {
			bg = t.SurfaceHover
			if variant == components.ButtonPrimary {
				bg = t.AccentHover
			}
		}
	}
	if variant == components.ButtonPrimary {
		border = bg
	}
	b.Height(32).Padding(6, 12).Radius(radius).Background(bg).TextColor(fg).Border(hairline, border)
	if b.IsDisabled() {
		b.Opacity(0.45)
	}
	panelFocus(c, b)
	return b
}

func panelFocus(c *ui.Context, control ui.Element) {
	control.FocusRing(false)
	if control.FocusVisible() {
		ink := c.Theme().Focus
		control.DrawOver(func(p *ui.Painter, r ui.Rect) {
			p.Stroke(ui.Rect{X: r.X - 2, Y: r.Y - 2, W: r.W + 4, H: r.H + 4}, ink, radius+2, hairline)
		})
	}
}

func panelButton(c *ui.Context, label string, variant components.ButtonVariant, disabled bool) ui.Element {
	b := styleControl(c, ui.ButtonBase(c).Disabled(disabled), variant)
	if label != "" {
		b.Label(label).Children(func() { ui.Text(c, label).SingleLine() })
	}
	return b
}

func panelIconButton(c *ui.Context, icon *ui.SVG, label string, disabled bool) ui.Element {
	b := panelButton(c, "", components.ButtonGhost, disabled).Size(32, 32).Padding(0).Label(label)
	b.Children(func() { components.Icon(c, icon).Size(16, 16).TextColor(c.Theme().TextMuted) })
	ui.TooltipBase(c, b, func(tip ui.Element) {
		t := c.Theme()
		tip.Padding(6, 8).Radius(radius).Background(t.Inverse).TextColor(t.InverseText).FontSize(fontSmall).
			Children(func() { ui.Text(c, label) })
	})
	return b
}

func panelSelect(c *ui.Context, selected *string, options []string) ui.Element {
	t := c.Theme()
	sel := ui.SelectBase(c, selected)
	b := styleControl(c, sel.Trigger, components.ButtonSecondary).MinWidth(140).Justify(ui.SpaceBetween).Gap(8)
	b.Children(func() {
		ui.Text(c, *selected).Grow(1).MinWidth(0).SingleLine()
		components.Icon(c, lucide.ChevronDown()).Size(14, 14).TextColor(t.TextMuted)
	})
	sel.Popup(func(panel ui.Element) {
		bg := ui.Hex("#f9f9fb") // Slate 2
		if t.Dark {
			bg = ui.Hex("#18191b")
		}
		// Outer 10 minus the 4-point inset (border included) equals inner 6.
		panel.Margin(4, 0, 0, 0).Padding(4-hairline).Radius(radius+4).MaxWidth(PanelWidth-40).Background(bg).Border(hairline, controlBorder(c, true))
		ui.Scroll(c).MaxHeight(280).Children(func() {
			for _, option := range options {
				item := sel.Item(option).Height(32).Padding(6, 8).Gap(8).Radius(radius)
				if item.Highlighted() {
					fill, ink := selectedColors(c)
					item.Background(fill).TextColor(ink)
				}
				item.Children(func() {
					ui.Text(c, option).Grow(1).MinWidth(0).SingleLine()
					if option == *selected {
						components.Icon(c, lucide.Check()).Size(14, 14)
					} else {
						ui.Box(c).Size(14, 14)
					}
				})
			}
		})
	})
	return b
}

func panelSegmented(c *ui.Context, selected *int, labels []string, disabled bool) ui.Element {
	t := c.Theme()
	parts := ui.SegmentedBase(c, selected, len(labels))
	track := parts.Track.Disabled(disabled).AlignItems(ui.Stretch).Height(34).Padding(2-hairline).Gap(2).
		Radius(radius+2).Background(t.Surface).Border(hairline, controlBorder(c, false))
	track.Children(func() {
		for i, label := range labels {
			s := styleControl(c, parts.Segment(i), components.ButtonGhost).Width(0).Grow(1).MinWidth(0).Height(30).Padding(4, 8)
			if i == *selected {
				fill, ink := selectedColors(c)
				s.Background(fill).TextColor(ink)
			}
			s.Label(label).Children(func() { ui.Text(c, label).SingleLine() })
		}
	})
	return track
}

func panelSearch(c *ui.Context, query *string) ui.Element {
	t := c.Theme()
	row := ui.Row(c).AlignItems(ui.Center).Height(32).Padding(0, 8).Gap(8).Radius(radius).Background(t.Surface).Border(hairline, controlBorder(c, false))
	var input ui.Element
	row.Children(func() {
		components.Icon(c, lucide.Search()).Size(14, 14).TextColor(t.TextMuted)
		input = ui.TextInputBase(c, query).Grow(1).MinWidth(0).Label("Search nodes").Placeholder("Search nodes").FocusRing(false)
		input.OnTextKey(func(k ui.TextKey) bool {
			if k.Key == ui.KeyEscape && k.Text() != "" {
				k.SetText("")
				return true
			}
			return false
		})
		if *query != "" {
			panelIconButton(c, lucide.X(), "Clear search", false).Size(24, 24).OnClick(func() { *query = ""; input.Focus() })
		}
	})
	if input.Focused() {
		row.BorderColor(t.Focus)
	}
	return input
}

func panelSwitch(c *ui.Context, on *bool, label string, disabled bool) ui.Element {
	t := c.Theme()
	sw := ui.SwitchBase(c, on).Label(label).Disabled(disabled).AlignItems(ui.Center).Height(32).Gap(12)
	if disabled {
		sw.Opacity(0.45)
	}
	panelFocus(c, sw)
	sw.Children(func() {
		ui.Text(c, label).Grow(1)
		fill := t.SurfaceHover
		if *on {
			fill = t.Accent
		}
		ui.Box(c).Size(34, 20).Radius(10).Background(fill).Border(hairline, controlBorder(c, false)).Draw(func(p *ui.Painter, r ui.Rect) {
			x := r.X + 2
			if *on {
				x = r.X + r.W - 18
			}
			p.Fill(ui.Rect{X: x, Y: r.Y + 2, W: 16, H: 16}, t.AccentText, 8)
		})
	})
	return sw
}

func panelHairline(c *ui.Context) ui.Element {
	return ui.Box(c).Height(hairline).Shrink(0).Background(c.Theme().Border)
}
