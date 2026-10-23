package menubar

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/pi-dal/sakamoto/internal/core"
	"github.com/pi-dal/sakamoto/internal/desktop"
)

func cloneConfiguration(value desktop.Configuration) desktop.Configuration {
	value.Fields = append([]desktop.ConfigurationField(nil), value.Fields...)
	for i := range value.Fields {
		value.Fields[i].Choices = append([]string(nil), value.Fields[i].Choices...)
	}
	return value
}

func (p *Panel) openConfiguration() {
	value, err := p.backend.Configuration()
	if err != nil {
		p.localNotice = "Could not load configuration. Open the TUI to repair it."
		return
	}
	p.configDraft = cloneConfiguration(value)
	p.configLoaded = true
	p.configuration = true
	p.configReturnSettings = true
	p.settings = false
	p.connections = false
	p.configSection = ""
	p.localNotice = ""
}

func (p *Panel) saveConfiguration() {
	if !p.configLoaded {
		return
	}
	draft := cloneConfiguration(p.configDraft)
	p.submitAction(func(ctx context.Context) error {
		return p.backend.SaveConfiguration(ctx, draft)
	})
}

func (p *Panel) configurationView(c *ui.Context) {
	t := c.Theme()
	if !p.configLoaded {
		ui.Text(c, "Configuration unavailable").FontSize(fontSmall).TextColor(t.TextMuted)
		return
	}
	ui.Scroll(c).Grow(1).Gap(14).PaddingEnd(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			panelButton(c, "Save configuration", components.ButtonPrimary, p.snapshot.Busy != "").OnClick(p.saveConfiguration)
		})
		if note := p.snapshot.Notice.Text; strings.Contains(strings.ToLower(note), "configuration") || p.snapshot.Busy == "Saving configuration" {
			color := t.TextMuted
			if p.snapshot.Notice.Kind == core.NoticeError {
				color = t.Danger
			}
			ui.Text(c, note).FontSize(fontSmall).TextColor(color).MaxHeight(44)
		}
		p.configurationSections(c)
		panelHairline(c)
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "Nodes and sources").Bold()
			ui.Text(c, fmt.Sprintf("%d subscriptions, nodes file configured", p.configDraft.SubscriptionCount)).FontSize(fontSmall).TextColor(t.TextMuted)
			p.configurationLink(c, "Open TUI for nodes and sources")
		})
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "Routing policy").Bold()
			ui.Text(c, fmt.Sprintf("%d user policy rules", p.configDraft.PolicyCount)).FontSize(fontSmall).TextColor(t.TextMuted)
			p.configurationLink(c, "Open TUI for routing policy")
		})
	})
}

func (p *Panel) configurationLink(c *ui.Context, label string) {
	if p.actions.OpenTUI != nil {
		panelButton(c, label, components.ButtonSecondary, p.actions.Demo).OnClick(func() { p.local(p.actions.OpenTUI) })
		return
	}
	if p.actions.RevealConfig != nil {
		panelButton(c, "Open configuration folder", components.ButtonSecondary, p.actions.Demo).OnClick(func() { p.local(p.actions.RevealConfig) })
	}
}

func (p *Panel) configurationSections(c *ui.Context) {
	current := ""
	for i := range p.configDraft.Fields {
		field := &p.configDraft.Fields[i]
		if field.Section != current {
			current = field.Section
			ui.Column(c).Gap(8).Children(func() {
				ui.Text(c, current).Bold()
			})
		}
		p.configurationField(c, i, field)
	}
}

func (p *Panel) configurationField(c *ui.Context, _ int, field *desktop.ConfigurationField) {
	rowKey := "configuration-" + field.Key
	switch field.Kind {
	case desktop.ConfigBool:
		on := strings.EqualFold(field.Value, "on")
		if panelSwitch(c.Key(rowKey), &on, field.Label, p.snapshot.Busy != "").Changed() {
			if on {
				field.Value = "on"
			} else {
				field.Value = "off"
			}
		}
	case desktop.ConfigChoice:
		selected := field.Value
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, field.Label).Width(132).SingleLine().Label(field.Label)
			if panelSelect(c.Key(rowKey), &selected, field.Choices).Label(field.Label).Grow(1).MinWidth(0).Changed() {
				field.Value = selected
			}
		})
	case desktop.ConfigText:
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, field.Label).Width(132).SingleLine().Label(field.Label)
			t := c.Theme()
			input := ui.TextInputBase(c.Key(rowKey), &field.Value).Grow(1).MinWidth(0).Height(32).Padding(6, 8).Radius(radius).Background(t.Surface).TextColor(t.Text).Label(field.Label).Placeholder(field.Label).FocusRing(false)
			border := controlBorder(c, false)
			if input.Focused() {
				border = t.Focus
			}
			input.Border(hairline, border)
			panelFocus(c, input)
		})
	default:
		return
	}
}
