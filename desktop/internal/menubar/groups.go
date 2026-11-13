package menubar

import (
	"context"
	"fmt"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/pi-dal/sakamoto/internal/desktop"
)

func (p *Panel) beginGroupEdit(index int) {
	p.managementEditor = "group"
	p.managementNoticeBaseline = p.snapshot.Notice
	p.managementQuery = ""
	p.confirmConfig = ""
	p.groupDraft = p.automaticGroups.Groups[index]
	p.groupDraft.Nodes = slices.Clone(p.groupDraft.Nodes)
}

func (p *Panel) automaticGroupsView(c *ui.Context) {
	busy := p.snapshot.Busy != ""
	t := c.Theme()
	if p.managementEditor == "" {
		p.managementApply(c)
		ui.Text(c, "Choose which nodes take part in automatic selection.").FontSize(fontSmall).TextColor(t.TextMuted)
		ui.Text(c, "Excluded nodes remain available in ManualPick. New nodes join automatically; saved exclusions survive regeneration.").FontSize(fontSmall).TextColor(t.TextMuted)
		if !p.automaticGroups.Generated {
			ui.Text(c, "Generate configuration to load available nodes.").FontSize(fontSmall).TextColor(t.TextMuted)
		}
		for i, group := range p.automaticGroups.Groups {
			included, excluded := groupCounts(group)
			ui.Row(c.Key(group.Tag)).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					ui.Text(c, group.Tag).Bold()
					ui.Text(c, fmt.Sprintf("%d included, %d excluded", included, excluded)).FontSize(fontSmall).TextColor(t.TextMuted)
				})
				panelButton(c, "Edit", components.ButtonSecondary, busy).Label("Edit group " + group.Tag).OnClick(func() { p.beginGroupEdit(i) })
			})
		}
		p.managementNotice(c)
		return
	}
	ui.Text(c, p.groupDraft.Tag).Bold()
	included, excluded := groupCounts(p.groupDraft)
	ui.Text(c, fmt.Sprintf("%d included, %d excluded", included, excluded)).FontSize(fontSmall).TextColor(t.TextMuted)
	present := 0
	for _, node := range p.groupDraft.Nodes {
		if node.Present {
			present++
		}
	}
	invalid := present > 0 && included == 0
	if invalid {
		ui.Text(c, "Keep at least one available node included.").FontSize(fontSmall).TextColor(t.Danger)
	}
	ui.Row(c).Gap(8).Children(func() {
		panelButton(c, "Save group", components.ButtonPrimary, busy || invalid).OnClick(func() {
			original, tag := p.automaticGroups, p.groupDraft.Tag
			excluded := []string{}
			for _, node := range p.groupDraft.Nodes {
				if node.Excluded {
					excluded = append(excluded, node.Tag)
				}
			}
			p.submitManagement(func(ctx context.Context) error { return p.backend.SaveAutomaticGroup(ctx, original, tag, excluded) })
		})
		panelButton(c, "Cancel edit", components.ButtonSecondary, busy).OnClick(func() { p.managementEditor = "" })
	})
	p.managementNotice(c)
	panelSearchLabel(c.Key("group-node-search"), &p.managementQuery, "Search group nodes")
	count := 0
	for i := range p.groupDraft.Nodes {
		node := &p.groupDraft.Nodes[i]
		if !p.matches(node.Tag) {
			continue
		}
		count++
		ui.Column(c.Key(node.Tag)).Gap(4).Children(func() {
			included := !node.Excluded
			if panelSwitch(c.Key("include-node"), &included, node.Tag, busy).Label("Include " + node.Tag).Changed() {
				node.Excluded = !included
			}
			if !node.Present {
				ui.Text(c, "Currently absent").FontSize(fontSmall).TextColor(t.TextMuted)
			}
		})
	}
	if count == 0 {
		text := "No matching nodes"
		if len(p.groupDraft.Nodes) == 0 {
			text = "No matching nodes yet. Add sources and regenerate configuration."
		}
		ui.Text(c, text).FontSize(fontSmall).TextColor(t.TextMuted)
	}
}

func groupCounts(group desktop.AutomaticGroup) (included, excluded int) {
	for _, node := range group.Nodes {
		if node.Excluded {
			excluded++
		} else if node.Present {
			included++
		}
	}
	return
}
