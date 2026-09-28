// Package sysproxy mirrors sakamoto's lifecycle to macOS HTTP/HTTPS proxy settings.
// It saves the previous settings before changing them and restores them when the core stops.
package sysproxy

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pi-dal/sakamoto/internal/config"
)

type Proxy struct {
	Enabled       bool   `json:"enabled"`
	Server        string `json:"server"`
	Port          int    `json:"port"`
	Authenticated bool   `json:"authenticated"`
}
type previous struct {
	Service string `json:"service"`
	HTTP    Proxy  `json:"http"`
	HTTPS   Proxy  `json:"https"`
}
type Runner func(args ...string) (string, error)

func Networksetup(args ...string) (string, error) {
	out, err := exec.Command("/usr/sbin/networksetup", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("networksetup %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
func readProxy(run Runner, kind, service string) (Proxy, error) {
	out, err := run("-get"+kind+"proxy", service)
	if err != nil {
		return Proxy{}, err
	}
	p := Proxy{}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "Enabled":
			p.Enabled = strings.EqualFold(v, "Yes")
		case "Server":
			p.Server = v
		case "Port":
			p.Port, _ = strconv.Atoi(v)
		case "Authenticated Proxy Enabled":
			p.Authenticated = v == "1"
		}
	}
	return p, nil
}
func setProxy(run Runner, kind, service string, p Proxy) error {
	if p.Server != "" && p.Port > 0 {
		if _, err := run("-set"+kind+"proxy", service, p.Server, strconv.Itoa(p.Port)); err != nil {
			return err
		}
	}
	state := "off"
	if p.Enabled {
		state = "on"
	}
	_, err := run("-set"+kind+"proxystate", service, state)
	return err
}

// Sync is idempotent. The saved settings file survives watch process restarts.
// Core status is provided by the caller, so the package never starts/stops a VPN.
func Sync(stateFile string, cfg *config.Config, coreConnected bool, run Runner) error {
	service := cfg.SystemProxy.Service
	if service == "" {
		service = "Wi-Fi"
	}
	data, err := os.ReadFile(stateFile)
	saved := previous{}
	hasSaved := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if hasSaved {
		if err := json.Unmarshal(data, &saved); err != nil {
			return fmt.Errorf("无法读取代理恢复文件: %w", err)
		}
	}
	if !coreConnected || !cfg.SystemProxy.Enabled {
		if !hasSaved {
			return nil
		}
		if err := setProxy(run, "web", saved.Service, saved.HTTP); err != nil {
			return err
		}
		if err := setProxy(run, "secureweb", saved.Service, saved.HTTPS); err != nil {
			return err
		}
		return os.Remove(stateFile)
	}
	if !cfg.MixedInbound.Enabled {
		return fmt.Errorf("系统代理需要同时启用 mixed_inbound")
	}
	if cfg.MixedInbound.Port < 1 || cfg.MixedInbound.Port > 65535 {
		return fmt.Errorf("mixed_inbound 端口无效")
	}
	if hasSaved && saved.Service != service {
		return fmt.Errorf("系统代理仍在管理 %s；请先停用并恢复，再切换网络服务", saved.Service)
	}
	httpCurrent, err := readProxy(run, "web", service)
	if err != nil {
		return err
	}
	httpsCurrent, err := readProxy(run, "secureweb", service)
	if err != nil {
		return err
	}
	if !hasSaved {
		if httpCurrent.Authenticated || httpsCurrent.Authenticated {
			return fmt.Errorf("当前网络使用带身份验证的系统代理，不能安全覆盖")
		}
		saved = previous{Service: service, HTTP: httpCurrent, HTTPS: httpsCurrent}
		b, _ := json.Marshal(saved)
		if err := os.MkdirAll(filepath.Dir(stateFile), 0700); err != nil {
			return err
		}
		tmp := stateFile + ".tmp"
		if err := os.WriteFile(tmp, b, 0600); err != nil {
			return err
		}
		if err := os.Rename(tmp, stateFile); err != nil {
			return err
		}
	}
	target := Proxy{Enabled: true, Server: "127.0.0.1", Port: cfg.MixedInbound.Port}
	if httpCurrent == target && httpsCurrent == target {
		return nil
	}
	if err := setProxy(run, "web", service, target); err != nil {
		return err
	}
	if err := setProxy(run, "secureweb", service, target); err != nil {
		_ = setProxy(run, "web", service, saved.HTTP)
		return err
	}
	return nil
}
