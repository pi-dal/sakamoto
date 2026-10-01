package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/gen"
	"github.com/pi-dal/sakamoto/internal/sbclient"
	"github.com/sagernet/sing-box/daemon"
)

// PrepareDNS builds a private candidate without rewriting active files or
// changing DNS/TUN state. Selector defaults preserve the current manual choice.
func PrepareDNS(path string) (string, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return "", err
	}
	cfg.DNSGuard.Enabled = true
	dir, err := os.MkdirTemp(filepath.Dir(path), ".dns-candidate-")
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(dir)
		}
	}()
	if err := writeCandidateInputs(path, dir, cfg); err != nil {
		return "", err
	}
	if err := gen.Run(gen.Options{ConfPath: cfg.ConfPath, NodesFile: cfg.NodesFile, SRJSONPath: cfg.SRJSONPath, AllowHosts: strings.Join(cfg.AllowHosts, ","), Cfg: cfg, OutDir: dir, Quiet: true}); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := preserveSelectors(ctx, path, dir, cfg); err != nil {
		return "", err
	}
	if out, err := exec.Command("sing-box", "check", "-D", dir, "-c", filepath.Join(dir, "config.json")).CombinedOutput(); err != nil {
		return "", fmt.Errorf("DNS candidate check: %w: %s", err, strings.TrimSpace(string(out)))
	}
	ok = true
	return dir, nil
}

func writeCandidateInputs(path, dir string, cfg *config.Config) error {
	cfgRaw, err := cfg.MarshalYAML()
	if err != nil {
		return err
	}
	if err := atomicFile(filepath.Join(dir, "sakamoto.yaml"), cfgRaw); err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(path), "auto-proxy.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read learned proxy policy: %w", err)
	}
	return atomicFile(filepath.Join(dir, "auto-proxy.json"), b)
}

func preserveSelectors(ctx context.Context, path, dir string, cfg *config.Config) error {
	client, err := sbclient.Dial(ctx, cfg.API.URL, cfg.API.Secret)
	if err != nil {
		connected, statusErr := supervisorConnected(filepath.Join(filepath.Dir(path), "svc.sock"))
		if statusErr != nil {
			return fmt.Errorf("cannot verify selector state after native API failure: %w", statusErr)
		}
		if connected {
			return fmt.Errorf("native API unavailable; cannot safely preserve selectors: %w", err)
		}
		return nil // No running core means there is no live selector to lose.
	}
	defer client.Close()
	groups, failures := client.SubscribeGroups(ctx)
	select {
	case snapshot, open := <-groups:
		if !open || snapshot == nil {
			return errors.New("native API returned no selector snapshot")
		}
		return applySelectorChoices(filepath.Join(dir, "config.json"), snapshot)
	case err := <-failures:
		return fmt.Errorf("could not capture active selector choices: %w", err)
	case <-ctx.Done():
		return fmt.Errorf("selector capture timed out: %w", ctx.Err())
	}
}

func supervisorConnected(path string) (bool, error) {
	socket, err := net.DialTimeout("unix", path, time.Second)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = socket.Close() }()
	if err := socket.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return false, err
	}
	if _, err := socket.Write([]byte("status\n")); err != nil {
		return false, err
	}
	buffer := make([]byte, 256)
	n, err := socket.Read(buffer)
	if err != nil {
		return false, err
	}
	status := strings.TrimSpace(string(buffer[:n]))
	if strings.HasPrefix(status, "connected ") {
		return true, nil
	}
	if status == "disconnected" {
		return false, nil
	}
	return false, fmt.Errorf("unexpected supervisor status %q", status)
}

func applySelectorChoices(path string, snapshot *daemon.Groups) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var core map[string]any
	if err := json.Unmarshal(data, &core); err != nil {
		return err
	}
	outbounds, ok := core["outbounds"].([]any)
	if !ok {
		return errors.New("generated config has no outbound list")
	}
	for _, value := range outbounds {
		node, ok := value.(map[string]any)
		if !ok {
			return errors.New("generated config contains a malformed outbound")
		}
		if node["type"] != "selector" {
			continue
		}
		tag, ok := node["tag"].(string)
		if !ok || tag == "" {
			return errors.New("generated selector has no tag")
		}
		members, ok := node["outbounds"].([]any)
		if !ok {
			return fmt.Errorf("selector %q has no outbound members", tag)
		}
		for _, group := range snapshot.Group {
			if group == nil {
				return errors.New("native API returned a malformed selector group")
			}
			if group.Tag != tag {
				continue
			}
			found := false
			for _, member := range members {
				name, ok := member.(string)
				if !ok {
					return fmt.Errorf("selector %q contains a malformed member", tag)
				}
				if name == group.Selected {
					node["default"] = group.Selected
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("active selection %q is unavailable in generated selector %q", group.Selected, tag)
			}
		}
	}
	encoded, err := json.MarshalIndent(core, "", "  ")
	if err != nil {
		return err
	}
	return atomicFile(path, encoded)
}
