package watch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/experiment"
	"github.com/pi-dal/sakamoto/internal/recovery"
)

// prepareLearning never activates generated configuration. Local rule files
// already loaded by the core can safely recover their committed contents.
func (w *Watcher) prepareLearning() error {
	if recovery.Held(filepath.Dir(w.cfgPath)) {
		return nil
	}
	cfg, err := config.Load(w.cfgPath)
	if err != nil || !experimentEnabled(cfg) {
		return err
	}
	dir := filepath.Dir(w.cfgPath)
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !experiment.ProbeReady(raw) || !experiment.LiveRulesReady(raw, dir) {
		w.emit("info", "automatic learning pending: regenerate and apply configuration explicitly; the watcher will not restart the TUN")
		return nil
	}
	state, err := experiment.Load(dir)
	if err != nil {
		return err
	}
	return experiment.WriteLiveRules(dir, state.Domains)
}

func (w *Watcher) learnCandidate(ctx context.Context, candidate experiment.Candidate) (result experiment.LearningResult) {
	result = experiment.LearningResult{Candidate: candidate, Time: time.Now(), State: "skipped"}
	dir := filepath.Dir(w.cfgPath)
	defer func() {
		if err := experiment.Record(dir, result); err != nil {
			w.emit("error", "auto: could not save learning explanation: %v", err)
		}
		w.emit("info", "auto %s: %s; %s", candidate.Domain, result.State, result.Detail)
	}()
	cfg, err := config.Load(w.cfgPath)
	if err != nil || !candidateEnabled(cfg, candidate) || ctx.Err() != nil || recovery.Held(dir) {
		result.Detail = "learning is disabled, unavailable or canceled"
		return
	}
	if candidate.Port != "80" && candidate.Port != "443" {
		result.Detail = "destination is not an HTTP(S) origin on port 80/443; no safe origin check is available"
		return
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		result.Detail = "generated configuration is unavailable"
		return
	}
	if !experiment.ProbeReady(raw) || !experiment.LiveRulesReady(raw, dir) {
		result.State = "deferred"
		result.Detail = "live verification rules are missing; regenerate and apply explicitly; automatic learning never restarts the TUN"
		return
	}
	epoch, err := w.learningCoreEpoch(ctx)
	info, statErr := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil || statErr != nil || info.ModTime().After(epoch.Add(time.Millisecond)) {
		result.State = "deferred"
		result.Detail = "generated configuration has not been applied to this core; apply explicitly to enable live learning"
		return
	}
	state, err := experiment.Load(dir)
	if err != nil {
		result.Detail = err.Error()
		return
	}
	for _, domain := range state.Domains {
		if domain == candidate.Domain {
			result.Detail = "domain already has a learned route"
			return
		}
	}
	probeCtx, stopProbes := context.WithTimeout(ctx, 25*time.Second)
	defer stopProbes()
	w.emit("info", "auto %s: checking direct and proxy origin reachability", candidate.Domain)
	// Both requests use the real native routing exits and carry no app cookies.
	direct := make(chan experiment.ProbeResult, 1)
	go func() { direct <- experiment.Probe(probeCtx, raw, candidate, false) }()
	result.Proxy = experiment.Probe(probeCtx, raw, candidate, true)
	result.Direct = <-direct
	if probeCtx.Err() != nil {
		result.Detail = "verification canceled"
		return
	}
	if result.Direct.Reachable {
		result.State = "direct_recovered"
		result.Detail = "direct origin check succeeded; retaining direct routing"
		return
	}
	if !result.Proxy.Reachable {
		result.State = "not_improved"
		result.Detail = "proxy origin check did not succeed; no route was saved"
		return
	}
	// The original observed failure plus bounded active direct checks meet the
	// configured evidence threshold without requiring repeated user retries.
	result.Failures++
	for result.Failures < latestThreshold(cfg) {
		select {
		case <-probeCtx.Done():
			result.Detail = "verification canceled"
			return
		case <-time.After(time.Second):
		}
		result.Direct = experiment.Probe(probeCtx, raw, candidate, false)
		if result.Direct.Reachable {
			result.State = "direct_recovered"
			result.Detail = "direct origin recovered during confirmation; retaining direct routing"
			return
		}
		if probeCtx.Err() != nil {
			result.Detail = "verification canceled"
			return
		}
		result.Failures++
	}
	// Reload settings immediately before any writes or reconnects.
	latest, err := config.Load(w.cfgPath)
	if err != nil || !candidateEnabled(latest, candidate) || recovery.Held(dir) {
		result.Detail = "learning was disabled during verification"
		return
	}
	current, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil || string(current) != string(raw) {
		result.Detail = "configuration changed during verification; retry on fresh evidence"
		return
	}
	next, rollback, err := experiment.StageLiveRules(dir, candidate.Domain)
	if err != nil {
		result.State = "failed"
		result.Detail = err.Error()
		return
	}
	verified := false
	verifyCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	for verifyCtx.Err() == nil {
		result.Proxy = experiment.ProbeRoute(verifyCtx, raw, candidate)
		if result.Proxy.Reachable {
			verified = true
			break
		}
		select {
		case <-verifyCtx.Done():
		case <-time.After(200 * time.Millisecond):
		}
	}
	current, readErr := os.ReadFile(filepath.Join(dir, "config.json"))
	afterEpoch, epochErr := w.learningCoreEpoch(ctx)
	latest, loadErr := config.Load(w.cfgPath)
	if verified && !recovery.Held(dir) && ctx.Err() == nil && readErr == nil && string(current) == string(raw) && epochErr == nil && afterEpoch.Equal(epoch) && loadErr == nil && candidateEnabled(latest, candidate) {
		if err = experiment.CommitLiveRules(dir, next); err == nil {
			result.State = "verified"
			result.Detail = "direct checks failed; target verified after live rule update; core and existing connections retained"
			return
		}
	}
	if err = rollback(); err != nil {
		if errors.Is(err, experiment.ErrLiveRulesChanged) {
			result.State = "superseded"
			result.Detail = "runtime rules changed; retained the newer rules and committed state"
			return
		}
		result.State = "failed"
		result.Detail = "could not restore the previous live domain file: " + err.Error()
		return
	}
	result.State = "rolled_back"
	result.Detail = "installed route failed verification or the core changed; previous live rules restored without reconnecting"
	return
}

func (w *Watcher) learningCoreEpoch(ctx context.Context) (time.Time, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	client, err := w.dialFn(checkCtx)
	if err != nil {
		return time.Time{}, err
	}
	defer client.Close()
	return client.StartedAt(checkCtx)
}

func candidateEnabled(cfg *config.Config, candidate experiment.Candidate) bool {
	return cfg.Experiment.Mode == "auto" || (cfg.Experiment.CFRegionBlock && strings.Contains(candidate.Reason, "Cloudflare"))
}

func latestThreshold(cfg *config.Config) int {
	return max(1, min(20, cfg.Experiment.Threshold))
}

func (w *Watcher) considerLearning(ctx context.Context, candidate experiment.Candidate) {
	now := time.Now()
	if w.learningBusy.Load() || (!w.lastLearning.IsZero() && now.Sub(w.lastLearning) < 30*time.Second) {
		return
	}
	if w.learningCooldown == nil {
		w.learningCooldown = map[string]time.Time{}
	}
	for domain, until := range w.learningCooldown {
		if !now.Before(until) {
			delete(w.learningCooldown, domain)
		}
	}
	if now.Before(w.learningCooldown[candidate.Domain]) || len(w.learningCooldown) >= 1000 {
		return
	}
	if !w.proxyHealthy() {
		w.learningCooldown[candidate.Domain] = now.Add(30 * time.Second)
		result := experiment.LearningResult{Candidate: candidate, Time: now, State: "proxy_unhealthy", Detail: "selected proxy has no fresh successful health test; no route was changed"}
		if err := experiment.Record(filepath.Dir(w.cfgPath), result); err != nil {
			w.emit("error", "auto: could not save learning explanation: %v", err)
		}
		w.emit("info", "auto %s: %s; %s", candidate.Domain, result.State, result.Detail)
		return
	}
	w.learningCooldown[candidate.Domain] = now.Add(5 * time.Minute)
	if !w.learningBusy.CompareAndSwap(false, true) {
		return
	}
	w.lastLearning = now
	w.learningWorkers.Add(1)
	go func() {
		defer w.learningWorkers.Done()
		defer w.learningBusy.Store(false)
		w.learnCandidate(ctx, candidate)
	}()
}
