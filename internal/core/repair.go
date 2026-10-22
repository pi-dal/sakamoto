package core

// RepairDecision is shared by native mobile clients. Verified means the actual
// core answered a fresh liveness query; end-to-end network checks stay separate.
type RepairDecision struct {
	State    string `json:"state"`
	Action   string `json:"action"`
	Verified bool   `json:"verified"`
	Detail   string `json:"detail"`
}

func PlanRepair(service ServiceState, coreAlive, checkOnly, restart, profileReady bool) RepairDecision {
	result := RepairDecision{State: "failed", Action: "none"}
	if checkOnly && restart {
		result.Detail = "Check and reconnect cannot be combined"
		return result
	}
	if service == ServiceStarting || service == ServiceStopping {
		result.State = "starting"
		result.Action = "wait"
		result.Detail = "Wait for the VPN transition, or disconnect to cancel it"
		return result
	}
	if service == ServiceRunning {
		if coreAlive && !restart {
			result.State = "healthy"
			result.Verified = true
			result.Detail = "Core verified; existing tunnel retained"
			return result
		}
		if !restart {
			result.State = "degraded"
			result.Detail = "Core verification unavailable; reconnect explicitly to replace the tunnel"
			return result
		}
		result.Action = "restart"
		result.State = "starting"
		result.Detail = "Waiting for confirmed VPN teardown before reconnecting"
	} else {
		result.State = "stopped"
		result.Detail = "Core stopped; repair can start the saved profile"
		if checkOnly {
			return result
		}
		result.Action = "start"
	}
	if !profileReady {
		result.State = "failed"
		result.Action = "none"
		result.Detail = "No complete saved configuration; select a current profile in Config"
	}
	return result
}
