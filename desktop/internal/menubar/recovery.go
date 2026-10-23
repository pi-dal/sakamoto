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
	ui.Scroll(c).Grow(1).Gap(16).Children(func() {
		ui.Column(c).Gap(7).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(7).Children(func() {
				icon, color := lucide.Stethoscope(), t.TextMuted
				if !s.Health.CheckedAt.IsZero() {
					icon, color = lucide.CircleAlert(), t.Warning
					if s.Health.Verified {
						icon, color = lucide.CircleCheck(), t.Success
					}
				}
				components.Icon(c, icon).Size(17, 17).TextColor(color)
				ui.Text(c, "Core health").Bold().Grow(1)
				if !s.Health.CheckedAt.IsZero() {
					ui.Text(c, s.Health.CheckedAt.Format("15:04:05")).FontSize(11).TextColor(t.TextMuted).Label("Last checked at " + s.Health.CheckedAt.Format("15:04:05"))
				}
			})
			summary := s.Health.Summary
			if summary == "" {
				summary = "Check the core API and configured DNS without restarting the tunnel."
				if s.ExternalCore {
					summary = "Check the native API of your existing core service."
				}
			}
			ui.Text(c, summary).FontSize(12).TextColor(t.TextMuted)
			ui.Row(c).Gap(8).Children(func() {
				label := "Check health"
				if s.Busy == "Checking health" {
					label = "Checking…"
				}
				components.Button(c, label, components.ButtonPrimary).Height(32).Disabled(busy || p.confirmRestart).OnClick(func() { p.submitAction(p.backend.CheckHealth) })
				if !s.ExternalCore {
					label := "Repair core"
					if s.Busy == "Repairing core" {
						label = "Repairing…"
					}
					components.Button(c, label, components.ButtonSecondary).Height(32).Disabled(busy || p.confirmRestart).OnClick(func() { p.submitAction(func(ctx context.Context) error { return p.backend.Repair(ctx, false) }) })
				}
			})
			if !s.ExternalCore {
				ui.Text(c, "Repair starts a stopped core and keeps a healthy one. It can restore the last working configuration.").FontSize(11).TextColor(t.TextMuted)
			}
		})
		if s.ExternalCore {
			ui.Divider(c)
			ui.Column(c).Gap(7).Children(func() {
				ui.Text(c, "Managed externally").Bold()
				ui.Text(c, "Use your core service manager for repair, restart and DNS recovery.").FontSize(12).TextColor(t.TextMuted)
				components.Button(c, "Open configuration folder", components.ButtonSecondary).Disabled(p.actions.Demo).OnClick(func() { p.local(p.actions.RevealConfig) })
			})
			return
		}
		ui.Divider(c)
		ui.Column(c).Gap(7).Children(func() {
			ui.Text(c, "Reconnect core").Bold()
			if p.confirmRestart {
				ui.Text(c, "Active connections will be interrupted.").FontSize(12).Bold().TextColor(t.Text)
				ui.Row(c).Gap(8).Children(func() {
					components.Button(c, "Cancel", components.ButtonSecondary).Label("Cancel reconnect").Height(32).OnClick(func() { p.confirmRestart = false })
					components.Button(c, "Reconnect now", components.ButtonPrimary).Height(32).Disabled(busy).OnClick(func() {
						p.confirmRestart = false
						p.submitAction(func(ctx context.Context) error { return p.backend.Repair(ctx, true) })
					})
				})
			} else {
				ui.Text(c, "Use when the core is running but health checks fail.").FontSize(12).TextColor(t.TextMuted)
				label := "Reconnect core…"
				if s.Busy == "Reconnecting core" {
					label = "Reconnecting…"
				}
				components.Button(c, label, components.ButtonSecondary).Height(32).Disabled(busy).OnClick(func() { p.confirmRestart = true })
			}
		})
		ui.Divider(c)
		ui.Column(c).Gap(7).Children(func() {
			ui.Text(c, "Automatic route recovery").Bold()
			summary := s.Recovery.Summary
			if summary == "" {
				summary = "Request fresh tests from the background watcher. Manual routes stay selected."
			}
			ui.Text(c, summary).FontSize(12).TextColor(t.TextMuted)
			components.Button(c, "Test automatic route", components.ButtonSecondary).Height(32).Disabled(busy || p.confirmRestart).OnClick(func() { p.submitAction(p.backend.RecoverRoute) })
		})
		if s.Health.State == "unavailable" || s.Health.State == "failed" || s.Health.State == "maintenance" {
			ui.Divider(c)
			ui.Column(c).Gap(7).Children(func() {
				ui.Text(c, "Background service").Bold()
				if s.Health.State == "maintenance" {
					ui.Text(c, "Use sakamoto ctl resume with the same configuration to resume deliberately.").FontSize(12).TextColor(t.TextMuted)
					components.Button(c, "Open configuration folder", components.ButtonSecondary).Disabled(busy || p.actions.Demo || p.actions.RevealConfig == nil).OnClick(func() { p.local(p.actions.RevealConfig) })
				} else {
					ui.Text(c, "Opens the bundled repair command in Terminal. Administrator authorization may be required.").FontSize(12).TextColor(t.TextMuted)
					components.Button(c, "Open service recovery", components.ButtonSecondary).Disabled(busy || p.actions.Demo || p.actions.OpenRecovery == nil).OnClick(func() { p.local(p.actions.OpenRecovery) })
				}
			})
		}
	})
}
