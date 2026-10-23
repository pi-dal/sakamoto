//go:build darwin || linux

package desktop

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/pi-dal/sakamoto/internal/security"
	"github.com/pi-dal/sakamoto/internal/svc"
)

func runtimeRepair(ctx context.Context, path string, check, restart bool) (HealthReport, error) {
	result, err := svc.RepairAt(ctx, path, svc.RepairOptions{CheckOnly: check, Restart: restart})
	report := HealthReport{State: "failed", Action: "none", Summary: "Repair failed. Open recovery terminal for details."}
	// Accept only known public states and actions. Detail is deliberately discarded.
	switch result.State {
	case "healthy", "repaired", "stopped", "degraded", "maintenance", "unavailable", "failed":
		report.State = result.State
	}
	switch result.Action {
	case "none", "started", "restarted", "rolled_back":
		report.Action = result.Action
	}
	report.Verified = err == nil && result.Verified && (report.State == "healthy" || report.State == "repaired")
	switch report.State {
	case "healthy":
		if report.Verified {
			report.Summary = "Core API and DNS verified."
		}
	case "repaired":
		if report.Verified {
			report.Summary = "Core API and DNS verified after repair."
			if report.Action == "rolled_back" {
				report.Summary = "Restored the last working configuration. Core API and DNS verified."
			}
		}
	case "stopped":
		report.Summary = "Core stopped."
	case "degraded":
		report.Summary = "Core health checks failed."
	case "maintenance":
		report.Summary = "Recovery paused for maintenance."
	case "unavailable":
		report.Summary = "Background service unavailable."
	}
	if !report.Verified && err == nil {
		err = errors.New("core health was not verified")
	}
	return report, err
}

func runtimeStatus(ctx context.Context, path string) (string, error) {
	return svc.SendToContext(ctx, filepath.Join(filepath.Dir(path), svc.SockName), "status")
}
func runtimeDisconnect(ctx context.Context, path string) (string, error) {
	return svc.SendToContext(ctx, filepath.Join(filepath.Dir(path), svc.SockName), "disconnect")
}
func runtimeConnect(path string) (string, error) {
	reply, _, err := security.ConnectWithPending(path)
	return reply, err
}
