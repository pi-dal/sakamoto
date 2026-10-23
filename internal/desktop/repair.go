package desktop

import (
	"context"
	"errors"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/core"
	"github.com/pi-dal/sakamoto/internal/sbclient"
	"github.com/pi-dal/sakamoto/internal/watchcontrol"
)

// HealthReport is a public projection. Supervisor detail, credentials, private
// paths and raw errors never enter the view model.
type HealthReport struct {
	State             string
	Action            string
	Verified          bool
	Summary           string
	CheckedAt         time.Time
	Problem           string
	UpgradeRequired   bool
	FallbackAvailable bool
}

type RecoveryReport struct {
	State       string
	Summary     string
	RequestedAt time.Time
}

func (c *Controller) CheckHealth(ctx context.Context) error {
	return c.perform(ctx, "Checking health", func(ctx context.Context) error {
		var report HealthReport
		var err error
		if c.opts.ExternalCore {
			report = HealthReport{State: "unavailable", Summary: "Core API unavailable. Check the service and API settings."}
			var cfg *config.Config
			cfg, err = config.Load(c.path)
			if err == nil {
				var client *sbclient.Client
				checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				client, err = sbclient.Dial(checkCtx, cfg.API.URL, cfg.API.Secret)
				if err == nil {
					_, err = client.ClashModeStatus(checkCtx)
					client.Close()
				}
			}
			if err == nil {
				report = HealthReport{State: "healthy", Verified: true, Summary: "Core API verified."}
			}
		} else {
			report, err = runtimeRepair(ctx, c.path, true, false)
		}
		c.publishHealth(report)
		return err
	})
}

// Repair uses the existing supervisor policy. restart is reserved for a
// deliberate reconnect; ordinary repair retains an already healthy process.
func (c *Controller) Repair(ctx context.Context, restart bool) error {
	if c.opts.ExternalCore {
		return errors.New("the external service owns repair and restart")
	}
	name := "Repairing core"
	if restart {
		name = "Reconnecting core"
	}
	return c.performRepair(ctx, name, func(ctx context.Context) (HealthReport, error) { return runtimeRepair(ctx, c.path, false, restart) })
}

func (c *Controller) RepairConfiguration(ctx context.Context, fallback bool) error {
	if c.opts.ExternalCore {
		return errors.New("the external service owns configuration recovery")
	}
	name := "Regenerating configuration"
	if fallback {
		name = "Restoring configuration"
	}
	return c.performRepair(ctx, name, func(ctx context.Context) (HealthReport, error) {
		return runtimeConfigurationRepair(ctx, c.path, fallback)
	})
}
func (c *Controller) performRepair(ctx context.Context, name string, repair func(context.Context) (HealthReport, error)) error {
	return c.perform(ctx, name, func(ctx context.Context) error {
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if c.opts.ConflictCheck(checkCtx) {
			c.update(func(s *Snapshot) { s.Notice = core.Errorf("Disconnect Shadowrocket before repairing the core.") })
			return errors.New("conflicting VPN is active")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		report, err := repair(ctx)
		c.publishHealth(report)
		return err
	})
}

func (c *Controller) publishHealth(report HealthReport) {
	report.CheckedAt = time.Now()
	c.update(func(s *Snapshot) {
		s.Health = report
		if report.Verified {
			s.Notice = core.Successf("%s", report.Summary)
		} else {
			s.Notice = core.Errorf("%s", report.Summary)
		}
	})
}

func (c *Controller) RecoverRoute(ctx context.Context) error {
	if c.opts.ExternalCore {
		return errors.New("the external service owns route recovery")
	}
	return c.perform(ctx, "Requesting route tests", func(ctx context.Context) error {
		status, err := watchcontrol.Request(ctx, c.path)
		summary := "Route tests unavailable. Check the background service."
		switch status {
		case "queued":
			summary = "Route tests queued."
		case "busy":
			summary = "Route tests running."
		case "cooldown":
			summary = "Route recovery is cooling down."
		case "manual":
			summary = "Manual route unchanged."
		case "disabled":
			summary = "Automatic fallback is off or has no alternative route."
		case "maintenance":
			summary = "Route recovery paused for maintenance."
		}
		if err != nil {
			status = "unavailable"
		}
		c.update(func(s *Snapshot) {
			s.Recovery = RecoveryReport{State: status, Summary: summary, RequestedAt: time.Now()}
			if err != nil {
				s.Notice = core.Errorf("%s", summary)
			} else {
				s.Notice = core.Progressf("%s", summary)
			}
		})
		return err
	})
}
