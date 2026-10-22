package svc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/experiment"
)

func TestRecoveryStaleLearningCannotOverwriteRollback(t *testing.T) {
	testRecoveryStaleLearning(t, nil)
}
func TestRecoveryStaleLearningCannotOverwriteIdenticalRestoredRules(t *testing.T) {
	testRecoveryStaleLearning(t, []string{"candidate.example", "staged.example"})
}
func testRecoveryStaleLearning(t *testing.T, known []string) {
	s, dir := recoveryFixture(t)
	raw, _ := os.ReadFile(s.cfgPath)
	var cfg map[string]any
	recoveryOK(t, json.Unmarshal(raw, &cfg))
	cfg["route"] = map[string]any{"rules": []any{map[string]any{"rule_set": []string{"rs-proxy"}, "outbound": "proxy"}}, "rule_set": []any{map[string]any{"type": "inline", "tag": "rs-proxy", "rules": []any{}}}}
	raw, _ = json.Marshal(cfg)
	raw, err := experiment.PrepareLiveRules(raw, dir, known)
	recoveryOK(t, err)
	if len(known) > 0 {
		recoveryOK(t, experiment.Save(dir, experiment.State{Domains: known}))
	}
	recoveryOK(t, experiment.WriteLiveRules(dir, known))
	recoveryOK(t, os.WriteFile(s.cfgPath, raw, 0600))
	original, err := os.ReadFile(filepath.Join(dir, experiment.LiveRuleFileName))
	recoveryOK(t, err)
	s.stablePeriod = 50 * time.Millisecond
	epoch := time.Now()
	s.epoch = func(context.Context) (time.Time, error) { return epoch, nil }
	if reply := s.start(); !strings.HasPrefix(reply, "connected") {
		t.Fatal(reply)
	}
	waitRecovery(t, func() bool { _, err := os.Stat(filepath.Join(dir, "recovery", "last-good.json")); return err == nil })
	s.mu.Lock()
	s.stablePeriod = time.Hour
	s.mu.Unlock()
	recoveryOK(t, experiment.Save(dir, experiment.State{Domains: []string{"candidate.example"}}))
	recoveryOK(t, experiment.WriteLiveRules(dir, []string{"candidate.example"}))
	next, oldRollback, err := experiment.StageLiveRules(dir, "staged.example")
	recoveryOK(t, err)
	s.stop()
	if reply := s.rollback(); !strings.HasPrefix(reply, "restored") {
		t.Fatal(reply)
	}
	if err := oldRollback(); !errors.Is(err, experiment.ErrLiveRulesChanged) {
		t.Fatal("old stage did not detect restored rules", err)
	}
	if err := experiment.CommitLiveRules(dir, next); !errors.Is(err, experiment.ErrLiveRulesChanged) {
		t.Fatal("old stage committed metadata into restored runtime", err)
	}
	state, err := experiment.Load(dir)
	recoveryOK(t, err)
	current, err := os.ReadFile(filepath.Join(dir, experiment.LiveRuleFileName))
	recoveryOK(t, err)
	if !slices.Equal(state.Domains, known) || string(current) != string(original) {
		t.Fatal("old worker or stale learned metadata changed restored policy", state.Domains, string(current))
	}
	writeRecoveryArtifact(t, map[string]any{"scenario": "stale-learning-after-rollback", "restored_absent_metadata": len(known) == 0, "identical_staged_bytes": len(known) > 0, "stale_worker_cannot_overwrite": true})
}
