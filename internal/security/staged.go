package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/sbclient"
	"github.com/pi-dal/sakamoto/internal/svc"
)

const pendingName = "api-rotation.pending.json"

type pendingKey struct {
	Secret string `json:"secret"`
}

func pendingPath(path string) string { return filepath.Join(filepath.Dir(path), pendingName) }

// PendingAPI reports whether a future credential is queued; it never returns
// the credential and never modifies the running connection.
func PendingAPI(path string) (bool, error) {
	info, err := os.Lstat(pendingPath(path))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return false, errors.New("pending API key must be a private regular file")
	}
	return true, nil
}
func readPending(path string) (string, error) {
	exists, err := PendingAPI(path)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}
	raw, err := os.ReadFile(pendingPath(path))
	if err != nil {
		return "", err
	}
	var key pendingKey
	if err := json.Unmarshal(raw, &key); err != nil {
		return "", err
	}
	if err := config.ValidateAPISecret(key.Secret); err != nil {
		return "", err
	}
	return key.Secret, nil
}

// StageAPI prepares a new credential without touching sakamoto.yaml,
// config.json, the active API, its PID, or any system proxy settings.
func StageAPI(path string) error {
	if existing, err := PendingAPI(path); err != nil {
		return err
	} else if existing {
		return errors.New("API rotation already staged; apply or cancel it first")
	}
	secret, err := config.NewAPISecret()
	if err != nil {
		return err
	}
	if _, err := prepare(path, secret); err != nil {
		return err
	}
	raw, err := json.Marshal(pendingKey{Secret: secret})
	if err != nil {
		return err
	}
	tmp, err := stageFile(filepath.Dir(path), ".api-pending-*", raw)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()
	// Link is atomic and fails if another process already staged a key.
	if err := os.Link(tmp, pendingPath(path)); err != nil {
		return fmt.Errorf("stage API key without replacing an existing pending key: %w", err)
	}
	return nil
}
func CancelPendingAPI(path string) error {
	if exists, err := PendingAPI(path); err != nil {
		return err
	} else if !exists {
		return nil
	}
	return os.Remove(pendingPath(path))
}

type preparedAPI struct {
	cfg                                *config.Config
	oldYAML, oldJSON, newYAML, newJSON []byte
}

func prepare(path, secret string) (*preparedAPI, error) {
	if err := config.ValidateAPISecret(secret); err != nil {
		return nil, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	oldYAML, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	oldJSON, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return nil, err
	}
	newJSON, err := replaceSecret(oldJSON, cfg.API.Secret, secret)
	if err != nil {
		return nil, err
	}
	cfg.API.Secret = secret
	newYAML, err := cfg.MarshalYAML()
	if err != nil {
		return nil, err
	}
	candidate, err := stageFile(dir, ".api-check-*.json", newJSON)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(candidate) }()
	if output, checkErr := exec.Command("sing-box", "check", "-D", dir, "-c", candidate).CombinedOutput(); checkErr != nil {
		return nil, fmt.Errorf("new API config rejected: %w: %s", checkErr, strings.TrimSpace(string(output)))
	}
	return &preparedAPI{cfg, oldYAML, oldJSON, newYAML, newJSON}, nil
}
func (p *preparedAPI) install(path string) error {
	if err := atomicFile(path, p.newYAML); err != nil {
		return err
	}
	if err := atomicFile(filepath.Join(filepath.Dir(path), "config.json"), p.newJSON); err != nil {
		_ = atomicFile(path, p.oldYAML)
		return err
	}
	return nil
}
func (p *preparedAPI) restore(path string) error {
	if !previousSafe(p.oldJSON) {
		return errors.New("old API key was unsafe; refusing to restore it")
	}
	if err := atomicFile(path, p.oldYAML); err != nil {
		return err
	}
	return atomicFile(filepath.Join(filepath.Dir(path), "config.json"), p.oldJSON)
}
func verifyAPI(cfg *config.Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		probeCtx, probeCancel := context.WithTimeout(ctx, 2*time.Second)
		client, err := sbclient.Dial(probeCtx, cfg.API.URL, cfg.API.Secret)
		probeCancel()
		if err == nil {
			client.Close()
			return nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("new API did not become healthy: %w", ctx.Err())
}

// ConnectWithPending applies a staged key ONLY when already disconnected, then
// performs the user's normal connect. It never disconnects a live TUN.
func ConnectWithPending(path string) (string, bool, error) {
	currentCfg, err := config.Load(path)
	if err != nil {
		return "", false, err
	}
	if currentCfg.DNSGuard.Enabled {
		if err := svc.RequireDNSLifecycle(); err != nil {
			return "", false, err
		}
	}
	secret, err := readPending(path)
	if err != nil {
		return "", false, err
	}
	if secret == "" {
		reply, err := svc.Send("connect")
		return reply, false, err
	}
	status, err := svc.Send("status")
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(status) != "disconnected" {
		return "", false, errors.New("VPN is still connected; staged API rotation will wait for a normal reconnect")
	}
	p, err := prepare(path, secret)
	if err != nil {
		return "", false, err
	}
	if err := p.install(path); err != nil {
		return "", false, err
	}
	reply, connectErr := svc.Send("connect")
	verified := false
	if connectErr == nil && strings.HasPrefix(reply, "already running") {
		// Another caller started the core concurrently. Do not disconnect it.
		if verifyAPI(p.cfg) == nil {
			connectErr = nil
			reply = "connected (already running)"
			verified = true
		} else {
			if err := p.restore(path); err != nil {
				return reply, false, err
			}
			return reply, false, errors.New("core was started concurrently with the previous credential; staged rotation deferred")
		}
	}
	if connectErr == nil && strings.HasPrefix(reply, "connected") && !verified {
		connectErr = verifyAPI(p.cfg)
	}
	if connectErr != nil || !strings.HasPrefix(reply, "connected") {
		if connectErr == nil {
			connectErr = fmt.Errorf("unexpected supervisor reply %q", strings.TrimSpace(reply))
		}
		if restoreErr := p.restore(path); restoreErr != nil {
			return reply, false, fmt.Errorf("API activation failed (%w); safe staged credential kept: %w", connectErr, restoreErr)
		}
		if restoreErr := svc.Reconnect(); restoreErr != nil {
			return reply, false, fmt.Errorf("API activation failed (%w); previous files restored but reconnect failed: %w", connectErr, restoreErr)
		}
		return reply, false, fmt.Errorf("API activation failed; previous configuration restored: %w", connectErr)
	}
	if err := CancelPendingAPI(path); err != nil {
		return reply, true, fmt.Errorf("new API active but pending-file cleanup failed: %w", err)
	}
	return reply, true, nil
}
