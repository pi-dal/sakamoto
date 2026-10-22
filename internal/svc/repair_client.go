package svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/recovery"
	"github.com/pi-dal/sakamoto/internal/sysdns"
)

var ErrSupervisorUnavailable = errors.New("supervisor unavailable")

// RepairAt uses the supervisor's serialized repair when available. Older
// supervisors get a bounded connect/disconnect sequence with real API checks.
func RepairAt(ctx context.Context, cfgPath string, options RepairOptions) (RepairResult, error) {
	if options.CheckOnly && options.Restart {
		return RepairResult{}, errors.New("--check and --restart cannot be combined")
	}
	raw, _ := json.Marshal(options)
	reply, err := SendToContext(ctx, filepath.Join(filepath.Dir(cfgPath), SockName), "repair "+string(raw))
	if err != nil {
		return RepairResult{State: "unavailable", Action: "none", Detail: "supervisor unavailable"}, fmt.Errorf("%w: %v", ErrSupervisorUnavailable, err)
	}
	var result RepairResult
	if strings.HasPrefix(reply, "unknown:") {
		return repairLegacy(ctx, cfgPath, options)
	}
	if err = json.Unmarshal([]byte(reply), &result); err != nil {
		return result, errors.New("supervisor returned an invalid repair response")
	}
	if !result.Verified {
		return result, errors.New(result.Detail)
	}
	return result, nil
}
func repairLegacy(ctx context.Context, path string, options RepairOptions) (RepairResult, error) {
	result := RepairResult{State: "failed", Action: "none"}
	dir := filepath.Dir(path)
	send := func(cmd string) (string, error) { return SendToContext(ctx, filepath.Join(dir, SockName), cmd) }
	verify := func(status string) error {
		if err := verifyLegacyRepair(ctx, path, status); err != nil {
			return err
		}
		after, err := send("status")
		if err != nil {
			return err
		}
		beforePID, afterPID := statusPID.FindStringSubmatch(status), statusPID.FindStringSubmatch(after)
		if !strings.HasPrefix(after, "connected") || len(beforePID) != 2 || len(afterPID) != 2 || beforePID[1] != afterPID[1] {
			return errors.New("core changed during verification")
		}
		return nil
	}
	fail := func(detail string) (RepairResult, error) { result.Detail = detail; return result, errors.New(detail) }
	if recovery.Held(dir) {
		result.State = "maintenance"
		return fail("maintenance is held; resume deliberately before repair")
	}
	status, err := send("status")
	if err != nil {
		return fail(err.Error())
	}
	running := strings.HasPrefix(status, "connected")
	if pid := statusPID.FindStringSubmatch(status); len(pid) == 2 {
		result.PID, _ = strconv.Atoi(pid[1])
	}
	if running {
		err = verify(status)
		if err == nil && !options.Restart {
			result.State = "healthy"
			result.Verified = true
			result.Detail = "core API and configured DNS protection verified; existing process retained"
			return result, nil
		}
		if !options.Restart {
			result.State = "degraded"
			return fail("live core retained: " + err.Error() + "; use repair --restart for an explicit reconnect")
		}
	} else if options.CheckOnly {
		result.State = "stopped"
		return fail("core is stopped; run repair to recover it")
	}
	if running {
		reply, err := send("disconnect")
		if err != nil || (!strings.HasPrefix(reply, "disconnected") && !strings.HasPrefix(reply, "not running")) {
			return fail("disconnect failed; original DNS protection retained: " + strings.TrimSpace(reply))
		}
		stopped := false
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
			if result.PID > 0 && syscall.Kill(result.PID, 0) == syscall.ESRCH {
				stopped = true
				break
			}
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return fail(ctx.Err().Error())
			case <-timer.C:
			}
		}
		if !stopped {
			return fail("old core has not exited; refusing overlapping VPN instances")
		}
	}
	if strings.Contains(status, "blocked") {
		reply, err := send("resume")
		if err != nil || !strings.HasPrefix(reply, "resumed") {
			return fail("cannot resume blocked supervisor; upgrade or inspect locally")
		}
	}
	if recovery.Held(dir) {
		result.State = "maintenance"
		return fail("maintenance began during repair")
	}
	reply, err := send("connect")
	if err != nil || !connectedReply(reply) {
		return fail("core start failed: " + strings.TrimSpace(reply) + "; running legacy supervisor may need a controlled upgrade")
	}
	status, err = send("status")
	if err != nil {
		return fail(err.Error())
	}
	if err = verify(status); err != nil {
		result.State = "degraded"
		return fail("core started but verification failed: " + err.Error())
	}
	if pid := statusPID.FindStringSubmatch(status); len(pid) == 2 {
		result.PID, _ = strconv.Atoi(pid[1])
	}
	result.State = "repaired"
	result.Action = "started"
	if running {
		result.Action = "restarted"
	}
	result.Verified = true
	result.Detail = "core API and configured DNS protection verified (legacy supervisor)"
	return result, nil
}
func verifyLegacyRepair(ctx context.Context, path, status string) error {
	if !strings.HasPrefix(status, "connected") {
		return errors.New("core is not running")
	}
	if strings.Contains(status, "dns=degraded") || strings.Contains(status, "dns=failed") || strings.Contains(status, "dns=starting") {
		return errors.New("DNS protection is not ready")
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	deadline := time.Now().Add(3 * time.Second)
	for {
		probe, cancelProbe := context.WithTimeout(ctx, time.Second)
		client, err := apiClientAt(probe, filepath.Join(filepath.Dir(path), "config.json"))
		if err == nil {
			_, err = client.StartedAt(probe)
			client.Close()
		}
		cancelProbe()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return err
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if cfg.DNSGuard.Enabled || strings.Contains(status, "dns=protected") {
		if !strings.Contains(status, "dns=protected") {
			return errors.New("configured DNS protection is not active")
		}
		return sysdns.ProbeContext(ctx, "127.0.0.1:53")
	}
	return nil
}
