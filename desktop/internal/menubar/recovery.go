package menubar

import (
	"context"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

func (p *Panel) recoveryView(c *ui.Context) {
	s, t := p.snapshot, c.Theme()
	busy := s.Busy != ""
	ui.Scroll(c).Grow(1).Gap(20).Children(func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				icon, color := lucide.Stethoscope(), t.TextMuted
				if !s.Health.CheckedAt.IsZero() {
					icon, color = lucide.CircleAlert(), t.Warning
					if s.Health.Verified {
						icon, color = lucide.CircleCheck(), t.Success
					}
				}
				components.Icon(c, icon).Size(16, 16).TextColor(color)
				ui.Text(c, "Core health").Bold().Grow(1)
				if !s.Health.CheckedAt.IsZero() {
					ui.Text(c, s.Health.CheckedAt.Format("15:04:05")).FontSize(fontSmall).TextColor(t.TextMuted).Label("Last checked at " + s.Health.CheckedAt.Format("15:04:05"))
				}
			})
			if s.Health.Summary != "" {
				ui.Text(c, s.Health.Summary).FontSize(fontSmall).TextColor(t.TextMuted)
			}
			ui.Row(c).Gap(8).Children(func() {
				label := "Check health"
				if s.Busy == "Checking health" {
					label = "Checking…"
				}
				panelButton(c, label, components.ButtonPrimary, busy || p.confirmRestart).OnClick(func() { p.submitAction(p.backend.CheckHealth) })
				if !s.ExternalCore {
					label := "Repair core"
					if s.Busy == "Repairing core" {
						label = "Repairing…"
					}
					panelButton(c, label, components.ButtonSecondary, busy || p.confirmRestart).OnClick(func() { p.submitAction(func(ctx context.Context) error { return p.backend.Repair(ctx, false) }) })
				}
			})
			if !s.ExternalCore {
				if p.confirmRestart {
					ui.Text(c, "Active connections will be interrupted.").FontSize(fontSmall)
					ui.Row(c).Gap(8).Children(func() {
						panelButton(c, "Cancel", components.ButtonSecondary, false).Label("Cancel reconnect").OnClick(func() { p.confirmRestart = false })
						panelButton(c, "Reconnect now", components.ButtonPrimary, busy).OnClick(func() {
							p.confirmRestart = false
							p.submitAction(func(ctx context.Context) error { return p.backend.Repair(ctx, true) })
						})
					})
				} else {
					label := "Reconnect core…"
					if s.Busy == "Reconnecting core" {
						label = "Reconnecting…"
					}
					panelButton(c, label, components.ButtonSecondary, busy).OnClick(func() { p.confirmRestart = true })
				}
			}
		})
		panelHairline(c)
		if s.ExternalCore {
			ui.Column(c).Gap(8).Children(func() {
				ui.Text(c, "Managed externally").FontSize(fontSmall).TextColor(t.TextMuted)
				panelButton(c, "Open configuration folder", components.ButtonSecondary, p.actions.Demo || p.actions.RevealConfig == nil).OnClick(func() { p.local(p.actions.RevealConfig) })
			})
			return
		}
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "Automatic route").Bold()
			if s.Recovery.Summary != "" {
				color := t.TextMuted
				if s.Recovery.State == "unavailable" {
					color = t.Danger
				}
				ui.Text(c, s.Recovery.Summary).FontSize(fontSmall).TextColor(color)
			}
			label := "Test route"
			if s.Busy == "Requesting route tests" {
				label = "Requesting tests…"
			}
			panelButton(c, label, components.ButtonSecondary, busy || p.confirmRestart).OnClick(func() { p.submitAction(p.backend.RecoverRoute) })
		})
		if s.Health.State == "unavailable" || s.Health.State == "failed" || s.Health.State == "maintenance" {
			panelHairline(c)
			ui.Column(c).Gap(8).Children(func() {
				ui.Text(c, "Background service").Bold()
				if s.Health.State == "maintenance" {
					ui.Text(c, "Resume with sakamoto ctl resume using this configuration.").FontSize(fontSmall).TextColor(t.TextMuted)
					panelButton(c, "Open configuration folder", components.ButtonSecondary, busy || p.confirmRestart || p.actions.Demo || p.actions.RevealConfig == nil).OnClick(func() { p.local(p.actions.RevealConfig) })
				} else {
					panelButton(c, "Open recovery terminal", components.ButtonSecondary, busy || p.confirmRestart || p.actions.Demo || p.actions.OpenRecovery == nil).OnClick(func() { p.local(p.actions.OpenRecovery) })
				}
			})
		}
	})
}
