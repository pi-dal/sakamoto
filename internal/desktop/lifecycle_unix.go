//go:build darwin || linux

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/pi-dal/sakamoto/internal/security"
	"github.com/pi-dal/sakamoto/internal/svc"
)

func runtimeRepair(ctx context.Context, path string, check, restart bool) (HealthReport, error) {
	result, err := svc.RepairAt(ctx, path, svc.RepairOptions{CheckOnly: check, Restart: restart})
	return projectRepair(result, err)
}
func runtimeConfigurationRepair(ctx context.Context, path string, fallback bool) (HealthReport, error) {
	result, err := svc.RepairAt(ctx, path, svc.RepairOptions{Restart: true, Fallback: fallback, Regenerate: !fallback})
	return projectRepair(result, err)
}
func projectRepair(result svc.RepairResult, err error) (HealthReport, error) {
	report := HealthReport{State: "failed", Action: "none", Summary: "Core could not start.", UpgradeRequired: result.UpgradeRequired, FallbackAvailable: result.FallbackAvailable}
	// Accept only known public states and actions. Detail is deliberately discarded.
	switch result.State {
	case "healthy", "repaired", "stopped", "degraded", "maintenance", "unavailable", "failed":
		report.State = result.State
	}
	switch result.Action {
	case "none", "started", "restarted", "rolled_back", "regenerated":
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
			if report.Action == "regenerated" {
				report.Summary = "Configuration regenerated. Core API and DNS verified."
			}
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
	if !report.Verified {
		messages := map[string]string{
			"service_upgrade_required": "Update the background service to recover configurations.",
			"generation_failed":        "Configuration could not be generated.",
			"config_invalid":           "Configuration rejected by the core.",
			"fallback_unavailable":     "No usable working configuration is available.",
			"pending_api_rotation":     "Apply or cancel the pending API key change first.",
			"inputs_changed":           "Configuration changed. Try again.",
			"reconnect_required":       "Reconnect to apply configuration recovery.",
			"api_unavailable":          "Core API unavailable.",
			"dns_unavailable":          "DNS protection unavailable.",
			"config_mismatch":          "API settings do not match the core configuration.",
			"timeout":                  "Repair timed out. Check health before retrying.",
			"canceled":                 "Repair canceled.",
			"busy":                     "Another repair is in progress.",
			"permission_denied":        "Access to the background service was denied.",
			"core_start_failed":        "Core could not start.",
			"health_unavailable":       "Core health checks failed.",
			"core_stopped":             "Core stopped.",
			"maintenance":              "Recovery paused for maintenance.",
			"service_unavailable":      "Background service unavailable.",
			"service_response_invalid": "Background service returned an invalid response.",
			"invalid_options":          "This repair action is unavailable.",
		}
		if summary, ok := messages[result.Problem]; ok {
			report.Problem = result.Problem
			report.Summary = summary
		}
	}
	if !report.Verified && err == nil {
		err = errors.New("core health was not verified")
	}
	return report, err
}

func runtimeRecoveryStatus(ctx context.Context, path string) (known, supported, fallback bool) {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	reply, err := svc.SendToContext(ctx, filepath.Join(filepath.Dir(path), svc.SockName), "info")
	if err != nil {
		return false, false, false
	}
	if strings.HasPrefix(reply, "unknown:") {
		return true, false, false
	}
	var info struct {
		Capabilities []string `json:"capabilities"`
		Working      struct {
			Present bool `json:"receipt_present"`
		} `json:"known_working"`
	}
	if json.Unmarshal([]byte(reply), &info) != nil || info.Capabilities == nil {
		return false, false, false
	}
	for _, capability := range info.Capabilities {
		if capability == "configuration-repair-v1" {
			supported = true
		}
	}
	return true, supported, info.Working.Present
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
