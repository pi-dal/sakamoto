package security

import (
	"context"
	"encoding/json"
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
	cfgRaw, err := cfg.MarshalYAML()
	if err != nil {
		return "", err
	}
	if err := atomicFile(filepath.Join(dir, "sakamoto.yaml"), cfgRaw); err != nil {
		return "", err
	}
	if b, e := os.ReadFile(filepath.Join(filepath.Dir(path), "auto-proxy.json")); e == nil {
		if err := atomicFile(filepath.Join(dir, "auto-proxy.json"), b); err != nil {
			return "", err
		}
	}
	if err := gen.Run(gen.Options{ConfPath: cfg.ConfPath, NodesFile: cfg.NodesFile, SRJSONPath: cfg.SRJSONPath, AllowHosts: strings.Join(cfg.AllowHosts, ","), Cfg: cfg, OutDir: dir, Quiet: true}); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	client, err := sbclient.Dial(ctx, cfg.API.URL, cfg.API.Secret)
	if err == nil {
		defer client.Close()
		groups, failures := client.SubscribeGroups(ctx)
		select {
		case snapshot := <-groups:
			if snapshot != nil {
				data, err := os.ReadFile(filepath.Join(dir, "config.json"))
				if err != nil {
					return "", err
				}
				var core map[string]any
				if err := json.Unmarshal(data, &core); err != nil {
					return "", err
				}
				for _, value := range core["outbounds"].([]any) {
					node := value.(map[string]any)
					if node["type"] != "selector" {
						continue
					}
					for _, group := range snapshot.Group {
						if group.Tag == node["tag"] {
							for _, member := range node["outbounds"].([]any) {
								if member == group.Selected {
									node["default"] = group.Selected
								}
							}
						}
					}
				}
				encoded, err := json.MarshalIndent(core, "", "  ")
				if err != nil {
					return "", err
				}
				if err := atomicFile(filepath.Join(dir, "config.json"), encoded); err != nil {
					return "", err
				}
			}
		case <-failures:
			return "", fmt.Errorf("could not capture active selector choices")
		case <-ctx.Done():
			return "", fmt.Errorf("selector capture timed out")
		}
	} else {
		// Avoid hiding a live manual selection behind a failed API capture.
		if socket, err := net.DialTimeout("unix", filepath.Join(filepath.Dir(path), "svc.sock"), time.Second); err == nil {
			_ = socket.SetDeadline(time.Now().Add(time.Second))
			_, _ = socket.Write([]byte("status\n"))
			buffer := make([]byte, 256)
			n, _ := socket.Read(buffer)
			_ = socket.Close()
			if strings.HasPrefix(string(buffer[:n]), "connected") {
				return "", fmt.Errorf("native API unavailable; cannot safely preserve selectors")
			}
		}
	}
	if out, err := exec.Command("sing-box", "check", "-D", dir, "-c", filepath.Join(dir, "config.json")).CombinedOutput(); err != nil {
		return "", fmt.Errorf("DNS candidate check: %v: %s", err, strings.TrimSpace(string(out)))
	}
	ok = true
	return dir, nil
}
