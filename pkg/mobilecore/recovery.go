package mobilecore

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"

	"github.com/pi-dal/sakamoto/internal/core"
	"github.com/pi-dal/sakamoto/internal/recovery"
	"github.com/pi-dal/sakamoto/internal/restorepoint"
)

// RecoveryProtocol identifies the compiled shared bridge, separately from app
// and native core versions. Both platforms bind this package in the SAME Go
// runtime as libbox; a second standalone recovery AAR/framework is unsupported.
func RecoveryProtocol() int32      { return 1 }
func RecoveryStableSeconds() int64 { return int64(recovery.StableInterval.Seconds()) }
func RepairDecisionJSON(service string, coreAlive, checkOnly, restart, profileReady bool) string {
	raw, _ := json.Marshal(core.PlanRepair(parseServiceState(service), coreAlive, checkOnly, restart, profileReady))
	return string(raw)
}

// RepairRuntimeJSON adds the durable episode to a read-only check. A manual
// repair keeps PlanRepair's explicit start action and resets admission in Begin.
func RepairRuntimeJSON(root, service string, coreAlive, checkOnly, restart, profileReady bool) string {
	decision := core.PlanRepair(parseServiceState(service), coreAlive, checkOnly, restart, profileReady)
	if checkOnly && decision.State == "stopped" {
		state, err := recovery.ReadMobileState(filepath.Join(root, "recovery", "mobile"))
		if err != nil {
			decision.State = "failed"
			decision.Detail = "Recovery journal is unreadable; repair explicitly to preserve and reset it"
		} else if state.Blocked {
			decision.State = "blocked"
			decision.Detail = "Automatic core recovery is blocked; repair explicitly for one new attempt"
		} else if state.Held {
			decision.Detail = "Disconnected manually; automatic recovery is paused"
		} else if state.Phase == "failed" {
			decision.State = "backoff"
			decision.Detail = "Core recovery is in backoff; explicit repair is available"
		}
	}
	raw, _ := json.Marshal(decision)
	return string(raw)
}

// RuntimeGate fences platform callbacks without holding a lock across native
// startup, TUN creation, stop, or platform-main-thread callbacks.
type RuntimeGate struct {
	mu         sync.Mutex
	generation int64
	phase      string
}

func NewRuntimeGate() *RuntimeGate { return &RuntimeGate{phase: "Stopped"} }
func (g *RuntimeGate) Begin() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.phase == "Starting" || g.phase == "Running" || g.phase == "Stopping" {
		return 0
	}
	g.generation++
	g.phase = "Starting"
	return g.generation
}
func (g *RuntimeGate) Stop() { g.mu.Lock(); defer g.mu.Unlock(); g.generation++; g.phase = "Stopping" }
func (g *RuntimeGate) FinishStop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.phase == "Stopping" {
		g.phase = "Stopped"
	}
}
func (g *RuntimeGate) Current(ticket int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return ticket != 0 && ticket == g.generation && (g.phase == "Starting" || g.phase == "Running")
}
func (g *RuntimeGate) Ready(ticket int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if ticket == 0 || ticket != g.generation || g.phase != "Starting" {
		return false
	}
	g.phase = "Running"
	return true
}
func (g *RuntimeGate) State() string { g.mu.Lock(); defer g.mu.Unlock(); return g.phase }

// RecoverySession owns one provider/service's persistent crash episode and
// private retained configuration. Only the tunnel process writes this object.
type RecoverySession struct {
	journal   *recovery.Mobile
	root      string
	mu        sync.Mutex
	lifecycle sync.Mutex
	canceled  bool
	candidate *restorepoint.MobileCandidate
	active    *restorepoint.MobileCandidate
	ticket    string
}

func NewRecoverySession(root string) (*RecoverySession, error) {
	journal, err := recovery.NewMobile(filepath.Join(root, "recovery", "mobile"))
	if err != nil {
		return nil, err
	}
	return &RecoverySession{journal: journal, root: root}, nil
}
func (s *RecoverySession) BeginFallback() (string, error) {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if s.canceled {
		return "", errors.New("recovery session stopped")
	}
	return s.journal.BeginFallback()
}
func (s *RecoverySession) Begin(manual bool) (string, error) {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if s.canceled {
		return "", errors.New("recovery session stopped")
	}
	return s.journal.Begin(manual)
}
func (s *RecoverySession) StatusJSON() (string, error) {
	state, err := s.journal.Status()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(state)
	return string(raw), err
}
func (s *RecoverySession) Prepare(ticket, content, metadata, workingDirectory string) error {
	state, err := s.journal.Status()
	if err != nil {
		return err
	}
	if state.Token != ticket || state.Held {
		return errors.New("recovery capture superseded")
	}
	candidate, err := restorepoint.CaptureMobile(s.root, content, metadata, workingDirectory)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err = s.journal.Status()
	if err != nil || state.Token != ticket || state.Held {
		candidate.Close()
		return errors.New("recovery capture superseded")
	}
	if s.candidate != nil && s.candidate != s.active {
		s.candidate.Close()
	}
	s.candidate = candidate
	s.ticket = ticket
	return nil
}
func (s *RecoverySession) PreparedConfig() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.candidate == nil {
		return "", errors.New("no captured mobile configuration")
	}
	return s.candidate.Content()
}
func (s *RecoverySession) applied() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil && s.active != s.candidate {
		s.active.RetainInputs()
	}
	s.active = s.candidate
}
func (s *RecoverySession) Ready(ticket, instance, version string) error {
	if err := s.journal.Ready(ticket, instance, version); err != nil {
		return err
	}
	s.applied()
	return nil
}
func (s *RecoverySession) Replaced(ticket, instance, version string) error {
	if err := s.journal.Replaced(ticket, instance, version); err != nil {
		return err
	}
	s.applied()
	return nil
}
func (s *RecoverySession) Failed(ticket string) error { return s.journal.Fail(ticket) }
func (s *RecoverySession) Stop() error {
	// Persist cancellation before waiting for disk capture to finish.
	s.lifecycle.Lock()
	s.canceled = true
	err := s.journal.Stop()
	s.lifecycle.Unlock()
	return err
}

// Release deletes unpromoted snapshots only AFTER native shutdown. Preparing a
// replacement must never delete assets still owned by the preceding service.
func (s *RecoverySession) ReleaseSnapshots() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.candidate != nil {
		s.candidate.Close()
	}
	if s.active != nil && s.active != s.candidate {
		s.active.Close()
	}
	s.candidate = nil
	s.active = nil
}
func (s *RecoverySession) Stable(ticket, instance string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ticket != ticket || s.candidate == nil || s.candidate != s.active {
		return errors.New("no captured configuration for the running core")
	}
	if err := s.journal.CheckStable(ticket, instance); err != nil {
		return err
	}
	if err := s.candidate.Promote(); err != nil {
		return err
	}
	return s.journal.Stable(ticket, instance)
}

// A retained mobile profile uses the installed signed core. The platform MUST
// run native CheckConfig again and confirm a new instance before reporting repair.
func (s *RecoverySession) RetainedConfig() (string, error) {
	content, _, err := restorepoint.LoadMobile(s.root)
	return content, err
}
func (s *RecoverySession) RetainedMetadata() (string, error) {
	_, metadata, err := restorepoint.LoadMobile(s.root)
	return metadata, err
}
