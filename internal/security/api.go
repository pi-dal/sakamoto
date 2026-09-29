// Package security rotates the local sing-box API credential without printing it.
package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/sbclient"
	"github.com/pi-dal/sakamoto/internal/svc"
)

var pidPattern = regexp.MustCompile(`\bpid=(\d+)`)

// RotateAPI replaces both the sidecar and generated secret. When this is the
// active runtime, it reconnects the service; any failed reconnect restores
// both files and attempts to bring the previous configuration back online.
func RotateAPI(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	jsonPath := filepath.Join(dir, "config.json")
	oldYAML, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	oldJSON, err := os.ReadFile(jsonPath)
	if err != nil {
		return err
	}

	newSecret, err := config.NewAPISecret()
	if err != nil {
		return err
	}
	newJSON, err := replaceSecret(oldJSON, cfg.API.Secret, newSecret)
	if err != nil {
		return err
	}
	cfg.API.Secret = newSecret

	// Check before changing either live file. -D preserves relative rule-set paths.
	candidate, err := stageFile(dir, ".api-check-*.json", newJSON)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(candidate) }()
	if out, checkErr := exec.Command("sing-box", "check", "-D", dir, "-c", candidate).CombinedOutput(); checkErr != nil {
		return fmt.Errorf("new API config rejected: %w: %s", checkErr, strings.TrimSpace(string(out)))
	}
	newYAML, err := cfg.MarshalYAML()
	if err != nil {
		return err
	}

	active := filepath.Clean(dir) == filepath.Clean(config.DefaultDir())
	status := "disconnected"
	if active {
		if s, e := svc.Send("status"); e == nil {
			status = strings.TrimSpace(s)
		}
	}
	if err := atomicFile(path, newYAML); err != nil {
		return err
	}
	if err := atomicFile(jsonPath, newJSON); err != nil {
		_ = atomicFile(path, oldYAML)
		return err
	}
	if !strings.HasPrefix(status, "connected") {
		return nil
	}
	rollback := func(cause error) error {
		if !previousSafe(oldJSON) {
			// Never resurrect a known-weak credential or web dashboard on a
			// failed reconnect. The new validated files stay on disk.
			return fmt.Errorf("API rotation failed; safe credential retained (old API was unsafe), reconnect manually: %w", cause)
		}
		_ = atomicFile(path, oldYAML)
		_ = atomicFile(jsonPath, oldJSON)
		current, _ := svc.Send("status")
		_, _ = svc.Send("disconnect")
		if m := pidPattern.FindStringSubmatch(current); len(m) == 2 {
			waitExit(m[1])
		}
		_, _ = svc.Send("connect")
		return fmt.Errorf("API rotation failed, previous config restored: %w", cause)
	}
	if reply, err := svc.Send("disconnect"); err != nil || !strings.HasPrefix(reply, "disconnected") {
		return rollback(fmt.Errorf("disconnect: %q: %v", strings.TrimSpace(reply), err))
	}
	if m := pidPattern.FindStringSubmatch(status); len(m) == 2 {
		waitExit(m[1])
	}
	if reply, err := svc.Send("connect"); err != nil || !strings.HasPrefix(reply, "connected") {
		return rollback(fmt.Errorf("connect: %q: %v", strings.TrimSpace(reply), err))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		probeCtx, probeCancel := context.WithTimeout(ctx, 2*time.Second)
		client, e := sbclient.Dial(probeCtx, cfg.API.URL, newSecret)
		probeCancel()
		if e == nil {
			client.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return rollback(fmt.Errorf("new API not reachable: %w", ctx.Err()))
		case <-time.After(250 * time.Millisecond):
		}
	}
	return rollback(fmt.Errorf("new API not reachable: %w", ctx.Err()))
}

func previousSafe(raw []byte) bool {
	var data struct {
		Services []struct {
			Type      string `json:"type"`
			Listen    string `json:"listen"`
			Secret    string `json:"secret"`
			Dashboard struct {
				Enabled bool `json:"enabled"`
			} `json:"dashboard"`
		} `json:"services"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return false
	}
	for _, s := range data.Services {
		if s.Type == "api" {
			return s.Listen == "127.0.0.1" && !s.Dashboard.Enabled && config.ValidateAPISecret(s.Secret) == nil
		}
	}
	return false
}

func replaceSecret(raw []byte, old, secret string) ([]byte, error) {
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	services, ok := data["services"].([]any)
	if !ok {
		return nil, errors.New("config.json has no services")
	}
	found := false
	for _, item := range services {
		service, ok := item.(map[string]any)
		if !ok || service["type"] != "api" {
			continue
		}
		if service["listen"] != "127.0.0.1" || service["secret"] != old {
			return nil, errors.New("API service and sidecar disagree; refusing rotation")
		}
		service["secret"] = secret
		service["dashboard"] = map[string]any{"enabled": false}
		found = true
	}
	if !found {
		return nil, errors.New("no local API service")
	}
	return json.MarshalIndent(data, "", "  ")
}

func stageFile(dir, pattern string, b []byte) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if _, err = f.Write(b); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
func atomicFile(path string, b []byte) error {
	name, err := stageFile(filepath.Dir(path), ".api-rotate-*", b)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(name) }()
	return os.Rename(name, path)
}
func waitExit(pidString string) {
	var pid int
	if _, err := fmt.Sscanf(pidString, "%d", &pid); err != nil || pid <= 0 {
		return
	}
	for i := 0; i < 100; i++ {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
