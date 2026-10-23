package svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/recovery"
)

type RepairOptions struct {
	CheckOnly  bool `json:"check_only,omitempty"`
	Restart    bool `json:"restart,omitempty"`
	Fallback   bool `json:"fallback,omitempty"`
	Regenerate bool `json:"regenerate,omitempty"`
}

func (o RepairOptions) Validate() error {
	if o.CheckOnly && (o.Restart || o.Fallback || o.Regenerate) {
		return errors.New("--check cannot be combined with configuration changes or --restart")
	}
	if o.Fallback && o.Regenerate {
		return errors.New("choose --fallback or --regenerate")
	}
	return nil
}

type RepairResult struct {
	State             string `json:"state"`
	Action            string `json:"action"`
	Verified          bool   `json:"verified"`
	PID               int    `json:"pid,omitempty"`
	Detail            string `json:"detail"`
	Problem           string `json:"problem,omitempty"`
	Stage             string `json:"stage,omitempty"`
	UpgradeRequired   bool   `json:"upgrade_required,omitempty"`
	FallbackAvailable bool   `json:"fallback_available,omitempty"`
}

func (s *Server) repair(ctx context.Context, options RepairOptions) (result RepairResult) {
	result = RepairResult{State: "failed", Action: "none", Stage: "check"}
	defer func() {
		_, err := os.Lstat(filepath.Join(s.workDir, "recovery", "last-good.json"))
		result.FallbackAvailable = err == nil
		if result.Verified {
			result.Problem = ""
			result.Stage = "complete"
		} else if result.Problem == "" {
			switch result.State {
			case "maintenance":
				result.Problem = "maintenance"
			case "stopped":
				result.Problem = "core_stopped"
			case "degraded":
				result.Problem = "health_unavailable"
			default:
				result.Problem = "core_start_failed"
			}
		}
	}()
	if err := options.Validate(); err != nil {
		result.Problem = "invalid_options"
		result.Detail = err.Error()
		return
	}
	if !s.repairMu.TryLock() {
		result.Problem = "busy"
		result.Detail = "another repair is in progress"
		return
	}
	defer s.repairMu.Unlock()
	if recovery.Held(s.workDir) {
		result.State = "maintenance"
		result.Detail = "maintenance is held; use ctl resume deliberately before repair"
		return
	}
	s.mu.Lock()
	child, gen, running := s.child, s.restarts, s.runningLocked()
	oldDone := s.childDone
	if !options.CheckOnly {
		s.repairing = true
	}
	s.mu.Unlock()
	if running {
		result.PID = child.Process.Pid
	}
	if !options.CheckOnly {
		defer s.finishRepair()
	}
	var prepared *preparedConfiguration
	if options.Fallback || options.Regenerate {
		result.Stage = "prepare"
		if running && !options.Restart {
			result.State = "degraded"
			result.PID = child.Process.Pid
			result.Problem = "reconnect_required"
			result.Detail = "configuration recovery requires explicit --restart while the core is running"
			return
		}
		s.lifecycle.Lock()
		prepareCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		s.mu.Lock()
		changed := s.child != child || s.restarts != gen
		if !changed {
			s.cancelStart = cancel
		}
		s.mu.Unlock()
		var err error
		if changed {
			err = repairIssue("canceled", errors.New("core lifecycle changed before configuration preparation"))
		} else {
			prepared, err = s.prepareConfiguration(prepareCtx, options)
		}
		if err == nil {
			err = prepareCtx.Err()
		}
		cancel()
		s.lifecycle.Unlock()
		if prepared != nil {
			defer prepared.Close()
		}
		if err != nil {
			result.Problem = problemFor(err)
			result.Detail = err.Error()
			return
		}
		if err = prepared.unchanged(s.workDir); err != nil {
			result.Problem = problemFor(err)
			result.Detail = err.Error()
			return
		}
	}
	if running {
		result.PID = child.Process.Pid
		err := s.verifyRepair(ctx)
		if !s.current(child, gen) {
			result.Problem = "canceled"
			result.Detail = "core changed during repair diagnosis"
			return
		}
		if err == nil && !options.Restart {
			result.State = "healthy"
			result.Verified = true
			result.Detail = "core API and configured DNS protection verified; existing process retained"
			return
		}
		if options.CheckOnly || !options.Restart {
			result.State = "degraded"
			result.Problem = problemFor(err)
			result.Detail = "live core retained: " + err.Error() + "; use repair --restart for an explicit reconnect"
			return
		}
	} else if options.CheckOnly {
		result.State = "stopped"
		result.Detail = "core is stopped; run repair to recover it"
		return
	}
	if options.Restart && running {
		result.Stage = "stop"
		oldChild := child
		if oldDone == nil {
			result.Detail = "cannot confirm the old core's exit"
			return
		}
		reply, stoppedGeneration := s.stopChecked(child, gen, true)
		if !strings.HasPrefix(reply, "disconnected") && !strings.HasPrefix(reply, "not running") {
			result.Detail = reply
			if strings.Contains(reply, "canceled") {
				result.Problem = "canceled"
			}
			return
		}
		child, gen = nil, stoppedGeneration
		defer func() {
			select {
			case <-oldDone:
				return
			default:
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.child == nil && s.restarts == gen {
				s.child = oldChild
				s.childDone = oldDone
				s.stopRequested = true
			}
		}()
		grace := time.NewTimer(5 * time.Second)
		select {
		case <-oldDone:
			grace.Stop()
		case <-ctx.Done():
			grace.Stop()
			result.Detail = ctx.Err().Error()
			return
		case <-grace.C:
			if err := oldChild.Process.Kill(); err != nil {
				result.Detail = "cannot terminate the owned unresponsive core: " + err.Error()
				return
			}
			wait := time.NewTimer(2 * time.Second)
			select {
			case <-oldDone:
				wait.Stop()
			case <-ctx.Done():
				wait.Stop()
				result.Detail = ctx.Err().Error()
				return
			case <-wait.C:
				result.Detail = "old core has not released its resources"
				return
			}
		}
	}
	// Serialize budget reset and generation invalidation. A concurrent manual
	// stop or maintenance hold wins over this repair's earlier observation.
	s.lifecycle.Lock()
	s.mu.Lock()
	if s.restarts != gen || s.child != child || s.runningLocked() {
		s.mu.Unlock()
		s.lifecycle.Unlock()
		result.Problem = "canceled"
		result.Detail = "core lifecycle changed; retry diagnosis"
		return
	}
	if recovery.Held(s.workDir) {
		s.mu.Unlock()
		s.lifecycle.Unlock()
		result.State = "maintenance"
		result.Detail = "maintenance is held"
		return
	}
	pinned := s.circuit.Pinned
	staged := s.activeConfigHash != "" && s.activeConfigHash != s.runtimeHash()
	s.restarts++
	s.repairing = true
	s.stopRequested = false
	s.mu.Unlock()
	result.Stage = "install"
	state := recovery.Circuit{Pinned: pinned}
	if prepared != nil {
		state = recovery.Circuit{Pinned: options.Fallback, RollbackUsed: options.Fallback, Blocked: true}
	}
	err := s.saveCircuitLocked(state)
	if err == nil && prepared != nil {
		err = prepared.Apply(ctx, s.workDir)
		if err == nil {
			s.mu.Lock()
			s.activeConfigHash = s.runtimeHash()
			s.mu.Unlock()
			state.Blocked = false
			err = s.saveCircuitLocked(state)
			result.Action = "regenerated"
			if options.Fallback {
				result.Action = "rolled_back"
			}
		}
	}
	s.mu.Lock()
	startChild, startGen := s.child, s.restarts
	s.mu.Unlock()
	s.lifecycle.Unlock()
	if err != nil {
		result.Problem = problemFor(err)
		result.Detail = "cannot install repair configuration or journal: " + err.Error()
		return
	}
	if prepared == nil && staged && !options.Restart {
		reply := s.rollback()
		if !strings.HasPrefix(reply, "restored") {
			result.Detail = "prepared inputs differ from the last applied runtime; " + reply
			s.blockRepair(result.Detail)
			return
		}
		result.Action = "rolled_back"
	}
	for attempt := 0; attempt < 2; attempt++ {
		result.Stage = "start"
		result.Problem = ""
		reply := s.startChecked(startChild, startGen, true, false)
		failure := reply
		if connectedReply(reply) {
			result.Stage = "verify"
			err = s.verifyRepair(ctx)
			if err == nil {
				s.mu.Lock()
				if s.child != nil && s.runningLocked() {
					result.PID = s.child.Process.Pid
					result.Verified = true
				}
				s.mu.Unlock()
				if result.Verified {
					if result.Action == "none" {
						result.Action = "started"
						if options.Restart && running {
							result.Action = "restarted"
						}
					}
					result.State = "repaired"
					result.Detail = "core API and configured DNS protection verified"
					return
				}
				failure = "core exited after verification"
			} else {
				result.Problem = problemFor(err)
				failure = "core started but verification failed: " + err.Error()
			}
		}
		s.mu.Lock()
		alive, used, stopped := s.runningLocked(), s.circuit.RollbackUsed, s.stopRequested
		if alive {
			result.PID = s.child.Process.Pid
		}
		s.mu.Unlock()
		if stopped || strings.Contains(reply, "canceled") {
			result.Problem = "canceled"
			result.Detail = "repair superseded by a lifecycle change"
			return
		}
		if alive {
			result.State = "degraded"
			result.Detail = failure
			return
		}
		if attempt == 1 || used || ctx.Err() != nil || recovery.Held(s.workDir) || strings.Contains(reply, "Shadowrocket") {
			result.Detail = failure
			s.blockRepair(failure)
			return
		}
		rollback := s.rollback()
		if !strings.HasPrefix(rollback, "restored") {
			result.Detail = failure + "; " + rollback
			s.blockRepair(result.Detail)
			return
		}
		result.Action = "rolled_back"
		s.mu.Lock()
		startChild, startGen = s.child, s.restarts
		s.mu.Unlock()
	}
	return
}
func (s *Server) finishRepair() {
	s.mu.Lock()
	s.repairing = false
	child, gen := s.child, s.restarts
	retry := child != nil && !s.runningLocked() && !s.stopRequested && !s.circuit.Blocked
	s.mu.Unlock()
	if retry && !recovery.Held(s.workDir) {
		go s.retryCore(child, gen)
	}
}
func connectedReply(reply string) bool {
	return strings.HasPrefix(reply, "connected") || strings.HasPrefix(reply, "already running")
}
func (s *Server) blockRepair(reason string) {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	s.mu.Lock()
	state := s.circuit
	s.mu.Unlock()
	state.Blocked = true
	state.Reason = "manual repair failed: " + reason
	if err := s.saveCircuitLocked(state); err != nil {
		s.appendLog("repair journal failed: " + err.Error())
	}
}
func (s *Server) verifyRepair(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	s.mu.Lock()
	child, gen, started := s.child, s.restarts, s.started
	dnsStatus := s.dnsStatus
	guarded := dnsStatus == "protected"
	alive := s.runningLocked()
	s.mu.Unlock()
	if !alive {
		return errors.New("core is not running")
	}
	if dnsStatus != "off" && !guarded {
		return repairIssue("dns_unavailable", errors.New("DNS protection is "+dnsStatus))
	}
	cfg, err := config.Load(filepath.Join(s.workDir, "sakamoto.yaml"))
	if err != nil {
		return repairIssue("config_invalid", err)
	}
	if err = checkConfigurationPair(filepath.Join(s.workDir, "sakamoto.yaml"), s.cfgPath); err != nil {
		return repairIssue("config_mismatch", err)
	}
	if cfg.DNSGuard.Enabled && !guarded {
		return repairIssue("dns_unavailable", errors.New("configured DNS protection is not active"))
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		var epoch time.Time
		probe, cancel := context.WithTimeout(ctx, time.Second)
		epoch, err = s.epoch(probe)
		cancel()
		if err == nil && !epoch.IsZero() {
			if epoch.Before(started.Add(-time.Second)) {
				return repairIssue("api_unavailable", errors.New("API belongs to an older core"))
			}
			break
		}
		if err == nil {
			err = errors.New("core API identity unavailable")
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-deadline.C:
			timer.Stop()
			return repairIssue("api_unavailable", fmt.Errorf("core API unavailable: %w", err))
		case <-timer.C:
		}
	}
	if guarded {
		if err = s.dnsProbeContext(ctx, "127.0.0.1:53"); err != nil {
			return repairIssue("dns_unavailable", err)
		}
	}
	if !s.current(child, gen) {
		return repairIssue("canceled", errors.New("core changed during verification"))
	}
	s.mu.Lock()
	alive = s.runningLocked()
	s.mu.Unlock()
	if !alive {
		return errors.New("core exited during verification")
	}
	return nil
}
func repairReply(result RepairResult) string { raw, _ := json.Marshal(result); return string(raw) }
