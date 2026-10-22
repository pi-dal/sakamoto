package svc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/recovery"
	"github.com/pi-dal/sakamoto/internal/restorepoint"
)

func (s *Server) SetVersion(version string) { s.mu.Lock(); s.version = version; s.mu.Unlock() }
func (s *Server) info() string {
	s.mu.Lock()
	value := map[string]any{"version": s.version, "protocol": 2, "capabilities": []string{"native-dns-v1", "offline-recovery-v1", "lifecycle-cancel-v1", "runtime-bundle-v1", "quick-repair-v1"}, "circuit": s.circuit, "state_error": s.stateErr != nil}
	if s.child != nil {
		value["core"] = map[string]any{"pid": s.child.Process.Pid, "executable": s.child.Path, "started": s.started, "alive": s.runningLocked(), "applied_input_sha256": s.activeConfigHash}
	}
	stableWindow := s.stablePeriod.Seconds()
	s.mu.Unlock()
	_, receiptErr := os.Lstat(filepath.Join(s.workDir, "recovery", "last-good.json"))
	value["known_working"] = map[string]any{"receipt_present": receiptErr == nil, "stable_window_seconds": stableWindow}
	if build, ok := debug.ReadBuildInfo(); ok {
		identity := map[string]string{"go": build.GoVersion}
		for _, item := range build.Settings {
			if item.Key == "vcs.revision" || item.Key == "vcs.modified" {
				identity[item.Key] = item.Value
			}
		}
		value["build"] = identity
	}
	raw, _ := json.Marshal(value)
	return string(raw)
}
func (s *Server) coreEpoch(ctx context.Context) (time.Time, error) {
	client, err := s.apiClient(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer client.Close()
	return client.StartedAt(ctx)
}
func (s *Server) captureRuntime(cmd *exec.Cmd) (*restorepoint.Candidate, error) {
	return restorepoint.Capture(s.workDir, cmd.Path)
}
func (s *Server) current(cmd *exec.Cmd, gen int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.child == cmd && s.restarts == gen
}
func (s *Server) saveCircuitLocked(state recovery.Circuit) error {
	err := recovery.SaveCircuit(s.workDir, state)
	s.mu.Lock()
	s.circuit = state
	s.stateErr = err
	s.mu.Unlock()
	return err
}

// Only actual child exits trigger this policy. A failed website or broken API
// stream never causes a healthy data plane to be restarted here.
func (s *Server) retryCore(cmd *exec.Cmd, gen int) {
	s.mu.Lock()
	if s.retryActive && s.retryOwner == cmd && s.retryOwnerGen == gen {
		s.mu.Unlock()
		return
	}
	s.retryActive = true
	s.retryOwner = cmd
	s.retryOwnerGen = gen
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.retryOwner == cmd && s.retryOwnerGen == gen {
			s.retryActive = false
		}
		s.mu.Unlock()
	}()
	for {
		s.lifecycle.Lock()
		if !s.current(cmd, gen) || recovery.Held(s.workDir) {
			s.lifecycle.Unlock()
			return
		}
		s.mu.Lock()
		state := s.circuit
		repairing := s.repairing
		stateErr := s.stateErr
		s.retryGen = gen
		s.mu.Unlock()
		if repairing || state.Blocked || stateErr != nil {
			s.lifecycle.Unlock()
			return
		}
		if state.RollbackUsed || state.Attempts >= recovery.MaxRetries {
			state.Blocked = true
			state.Reason = "crash recovery budget exhausted"
			if err := s.saveCircuitLocked(state); err != nil {
				s.lifecycle.Unlock()
				return
			}
			canRollback := !state.RollbackUsed
			if canRollback {
				if reply := s.rollbackLocked(); strings.HasPrefix(reply, "restored") {
					s.lifecycle.Unlock()
					s.appendLog("crash budget exhausted; starting known-working runtime once")
					s.startGeneration(cmd, gen)
					return
				}
			}
			s.lifecycle.Unlock()
			s.appendLog("recovery blocked; inspect locally, then explicitly resume or roll back")
			return
		}
		state.Attempts++
		state.Reason = "unexpected core exit"
		if err := s.saveCircuitLocked(state); err != nil {
			s.lifecycle.Unlock()
			return
		}
		s.lifecycle.Unlock()
		delay := s.retryDelay * time.Duration(1<<(state.Attempts-1))
		s.appendLog(fmt.Sprintf("auto-restart attempt %d/%d in %s", state.Attempts, recovery.MaxRetries, delay))
		// Small checks keep manual stop responsive without a goroutine per retry.
		deadline := time.Now().Add(delay)
		for time.Now().Before(deadline) {
			timer := time.NewTimer(min(time.Until(deadline), 100*time.Millisecond))
			<-timer.C
			if !s.current(cmd, gen) || recovery.Held(s.workDir) {
				return
			}
		}
		reply := s.startGeneration(cmd, gen)
		if !s.current(cmd, gen) || strings.HasPrefix(reply, "connected") || strings.Contains(reply, "canceled") || strings.Contains(reply, "maintenance") {
			return
		}
		s.appendLog("restart failed: " + reply)
	}
}
func (s *Server) runtimeHash() string {
	hash := sha256.New()
	for _, name := range []string{"config.json", "sakamoto.yaml"} {
		raw, err := os.ReadFile(filepath.Join(s.workDir, name))
		if err != nil {
			return "unreadable"
		}
		hash.Write(raw)
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
func (s *Server) resume() string { return s.resumeWithEngine(false) }
func (s *Server) resumeWithEngine(installed bool) string {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	s.mu.Lock()
	running := s.runningLocked()
	s.mu.Unlock()
	if running && installed {
		return "error: disconnect before choosing the installed core"
	}
	if err := recovery.Resume(s.workDir); err != nil {
		return "error: resume failed: " + err.Error()
	}
	state, err := recovery.LoadCircuit(s.workDir)
	if err == nil && installed {
		state.Pinned = false
		err = recovery.SaveCircuit(s.workDir, state)
	}
	s.mu.Lock()
	s.circuit = state
	s.stateErr = err
	s.stopRequested = false
	s.mu.Unlock()
	if err != nil {
		return "error: resume receipt failed: " + err.Error()
	}
	if running {
		return "resumed; existing core retained"
	}
	return "resumed; disconnected; connect explicitly"
}
func (s *Server) rollback() string {
	s.mu.Lock()
	running := s.runningLocked()
	s.mu.Unlock()
	if running {
		return "error: disconnect before restoring a known-working runtime"
	}
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	s.mu.Lock()
	running = s.runningLocked()
	s.mu.Unlock()
	if running {
		return "error: core started; disconnect before rollback"
	}
	return s.rollbackLocked()
}
func (s *Server) rollbackLocked() string {
	s.mu.Lock()
	state := s.circuit
	s.mu.Unlock()
	if state.RollbackUsed {
		return "error: rollback already used; inspect and explicitly resume before another attempt"
	}
	point, err := restorepoint.Load(s.workDir)
	if err != nil {
		return "error: known-working runtime unavailable: " + err.Error()
	}
	if _, err := os.Lstat(s.dnsStatePath()); err == nil {
		cfg, err := config.Load(filepath.Join(point.Dir, "sakamoto.yaml"))
		if err != nil || !cfg.DNSGuard.Enabled {
			return "error: rollback would leave protected DNS; use explicit offline rescue to restore ordinary networking first"
		}
	}
	if _, err := restorepoint.BackupCandidate(s.workDir); err != nil {
		return "error: cannot preserve rejected candidate: " + err.Error()
	}
	state.RollbackUsed = true
	state.Pinned = true
	state.Blocked = true
	state.Attempts = 0
	if err := s.saveCircuitLocked(state); err != nil {
		return "error: rollback journal: " + err.Error()
	}
	if err := point.Apply(s.workDir); err != nil {
		return "error: rollback incomplete; maintenance required: " + err.Error()
	}
	s.mu.Lock()
	s.activeConfigHash = s.runtimeHash()
	s.mu.Unlock()
	state.Blocked = false
	state.Reason = "known-working runtime restored"
	if err := s.saveCircuitLocked(state); err != nil {
		return "error: rollback receipt: " + err.Error()
	}
	return "restored known-working configuration, referenced assets and pinned core; disconnected"
}

func (s *Server) selectedBinary() (string, error) {
	s.mu.Lock()
	pinned := s.circuit.Pinned
	s.mu.Unlock()
	if !pinned {
		return singBoxExecutable(), nil
	}
	point, err := restorepoint.Load(s.workDir)
	if err != nil {
		return "", err
	}
	// Editing a candidate is explicit intent to apply new content. Do not run a
	// changed configuration against an older engine merely because it was pinned.
	current, err := os.ReadFile(s.cfgPath)
	if err != nil {
		return "", err
	}
	known, err := os.ReadFile(filepath.Join(point.Dir, "config.json"))
	if err != nil {
		return "", err
	}
	if string(current) != string(known) {
		return "", fmt.Errorf("known-working core is pinned; ctl resume-installed is required before applying a new generation")
	}
	return filepath.Join(point.Dir, "engine"), nil
}
func (s *Server) observeRuntime(cmd *exec.Cmd, gen int, done <-chan struct{}, candidate *restorepoint.Candidate) {
	defer candidate.Close()
	observeCtx, cancelObserve := context.WithCancel(context.Background())
	defer cancelObserve()
	go func() {
		select {
		case <-done:
			cancelObserve()
		case <-observeCtx.Done():
		}
	}()
	var epoch time.Time
	var err error
	deadline := time.Now().Add(30 * time.Second)
	for {
		probeCtx, cancel := context.WithTimeout(observeCtx, 3*time.Second)
		epoch, err = s.epoch(probeCtx)
		cancel()
		if err == nil && !epoch.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			s.appendLog("known-working snapshot pending: native API identity unavailable")
			return
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-observeCtx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if epoch.Before(started.Add(-time.Second)) {
		s.appendLog("known-working snapshot rejected: API belongs to an older core")
		return
	}
	if !candidate.Unchanged() {
		s.appendLog("known-working snapshot skipped: staged files differ from applied inputs")
		return
	}
	s.mu.Lock()
	stablePeriod := s.stablePeriod
	s.mu.Unlock()
	timer := time.NewTimer(stablePeriod)
	defer timer.Stop()
	select {
	case <-done:
		return
	case <-timer.C:
	}
	if !s.current(cmd, gen) || recovery.Held(s.workDir) {
		return
	}
	ctx, cancel := context.WithTimeout(observeCtx, 6*time.Second)
	again, err := s.epoch(ctx)
	if err == nil && again.Equal(epoch) {
		s.mu.Lock()
		guarded := s.dnsStatus == "protected"
		s.mu.Unlock()
		if guarded {
			err = s.dnsProbeContext(ctx, "127.0.0.1:53")
		}
	} else if err == nil {
		err = fmt.Errorf("core API epoch changed")
	}
	cancel()
	if err != nil || !s.current(cmd, gen) || recovery.Held(s.workDir) {
		return
	}
	// Do not hold lifecycle during disk copying; stop remains available. The
	// captured inputs and held executable descriptor are immutable evidence.
	point, promoteErr := candidate.Promote(cmd.Process.Pid, epoch)
	if promoteErr != nil {
		s.appendLog("known-working snapshot not promoted: " + promoteErr.Error())
		return
	}
	if err := candidate.Prune(point); err != nil {
		s.appendLog("old snapshot cleanup deferred: " + err.Error())
	}
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if !s.current(cmd, gen) || recovery.Held(s.workDir) {
		return
	}
	s.mu.Lock()
	pinned := s.circuit.Pinned
	alive := s.runningLocked()
	s.mu.Unlock()
	if !alive {
		return
	}
	if err := s.saveCircuitLocked(recovery.Circuit{Pinned: pinned}); err != nil {
		s.appendLog("stable runtime receipt failed: " + err.Error())
		return
	}
	s.appendLog("known-working runtime verified; crash budget reset")
}
