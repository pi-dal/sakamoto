// Package sysproxy transactionally manages macOS HTTP/HTTPS proxy settings.
package sysproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/recovery"
)

type Proxy struct {
	Enabled       bool   `json:"enabled"`
	Server        string `json:"server"`
	Port          int    `json:"port"`
	Authenticated bool   `json:"authenticated"`
}
type previous struct {
	Service      string `json:"service"`
	HTTP         Proxy  `json:"http"`
	HTTPS        Proxy  `json:"https"`
	Managed      *Proxy `json:"managed,omitempty"`
	PriorManaged *Proxy `json:"prior_managed,omitempty"`
}
type Runner func(args ...string) (string, error)

func Networksetup(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/sbin/networksetup", args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("networksetup failed: %w", err)
	}
	return string(out), nil
}
func validService(service string) bool {
	return strings.TrimSpace(service) != "" && len(service) < 256 && !strings.HasPrefix(service, "-") && !strings.ContainsAny(service, "\x00\r\n")
}
func validProxy(p Proxy) bool {
	if p.Authenticated || strings.ContainsAny(p.Server, "\x00\r\n") || strings.HasPrefix(p.Server, "-") {
		return false
	}
	return p.Port >= 0 && p.Port <= 65535 && (!p.Enabled || (p.Port > 0 && p.Server != ""))
}
func readProxy(run Runner, kind, service string) (Proxy, error) {
	if !validService(service) {
		return Proxy{}, errors.New("invalid proxy network service")
	}
	out, err := run("-get"+kind+"proxy", service)
	if err != nil {
		return Proxy{}, err
	}
	p := Proxy{}
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "Enabled":
			if value != "Yes" && value != "No" {
				return p, errors.New("invalid proxy enabled state")
			}
			p.Enabled = value == "Yes"
			seen["enabled"] = true
		case "Server":
			p.Server = value
			seen["server"] = true
		case "Port":
			p.Port, err = strconv.Atoi(value)
			if err != nil {
				return p, err
			}
			seen["port"] = true
		case "Authenticated Proxy Enabled":
			p.Authenticated = value == "1" || value == "Yes"
			seen["auth"] = true
		}
	}
	if len(seen) != 4 || p.Port < 0 || p.Port > 65535 {
		return p, errors.New("incomplete proxy read-back")
	}
	return p, nil
}
func setProxy(run Runner, kind, service string, p Proxy) error {
	if !validService(service) || !validProxy(p) {
		return errors.New("invalid saved proxy settings")
	}
	if p.Server != "" && p.Port > 0 {
		if _, err := run("-set"+kind+"proxy", service, p.Server, strconv.Itoa(p.Port)); err != nil {
			return err
		}
	}
	state := "off"
	if p.Enabled {
		state = "on"
	}
	if _, err := run("-set"+kind+"proxystate", service, state); err != nil {
		return err
	}
	after, err := readProxy(run, kind, service)
	if err != nil {
		return err
	}
	if !equivalent(after, p) {
		return errors.New("proxy read-back did not match requested state")
	}
	return nil
}
func equivalent(a, b Proxy) bool {
	return a.Enabled == b.Enabled && (!b.Enabled || (a.Server == b.Server && a.Port == b.Port && a.Authenticated == b.Authenticated))
}
func load(path string) (previous, bool, error) {
	raw, err := recovery.ReadPrivate(path, 8192)
	if errors.Is(err, os.ErrNotExist) {
		return previous{}, false, nil
	}
	if err != nil {
		return previous{}, false, err
	}
	var p previous
	if err = json.Unmarshal(raw, &p); err != nil {
		return p, false, err
	}
	if !validService(p.Service) || !validProxy(p.HTTP) || !validProxy(p.HTTPS) {
		return p, false, errors.New("invalid proxy restore snapshot")
	}
	for _, target := range []*Proxy{p.Managed, p.PriorManaged} {
		if target != nil && (!validProxy(*target) || target.Server != "127.0.0.1" || !target.Enabled) {
			return p, false, errors.New("invalid managed proxy receipt")
		}
	}
	return p, true, nil
}
func CheckMissingState(path, service string, run Runner) error {
	if _, err := os.Lstat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, kind := range []string{"web", "secureweb"} {
		current, err := readProxy(run, kind, service)
		if err != nil {
			return err
		}
		if current.Enabled && current.Server == "127.0.0.1" {
			return errors.New("loopback proxy has no restore snapshot; review original proxy settings administratively")
		}
	}
	return nil
}

func ValidateState(path string) error { _, _, err := load(path); return err }

func save(path string, p previous) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return recovery.Atomic(path, raw, 0600)
}

// Restore changes only the exact managed targets. Explicit external overrides
// are retained. The file survives partial failure and disappears only after
// both protocols have verified read-back or a confirmed external override.
func Restore(path string, legacyTarget *Proxy, run Runner) error {
	saved, exists, err := load(path)
	if err != nil || !exists {
		return err
	}
	for _, entry := range []struct {
		kind   string
		before Proxy
	}{{"web", saved.HTTP}, {"secureweb", saved.HTTPS}} {
		current, err := readProxy(run, entry.kind, saved.Service)
		if err != nil {
			return err
		}
		if equivalent(current, entry.before) {
			continue
		}
		owned := false
		targets := []*Proxy{saved.Managed, saved.PriorManaged}
		if saved.Managed == nil {
			targets = append(targets, legacyTarget)
		}
		for _, target := range targets {
			if target != nil && current == *target {
				owned = true
			}
		}
		if !owned {
			if saved.Managed == nil && legacyTarget == nil && current.Enabled && current.Server == "127.0.0.1" {
				return errors.New("legacy snapshot lacks managed port; inspect local sidecar before restoring")
			}
			continue // Preserve a verified user override.
		}
		if err = setProxy(run, entry.kind, saved.Service, entry.before); err != nil {
			return err
		}
	}
	if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return recovery.SyncDir(filepath.Dir(path))
}

func Sync(stateFile string, cfg *config.Config, coreConnected bool, run Runner) error {
	service := cfg.SystemProxy.Service
	if service == "" {
		service = "Wi-Fi"
	}
	target := Proxy{Enabled: true, Server: "127.0.0.1", Port: cfg.MixedInbound.Port}
	if recovery.Held(filepath.Dir(stateFile)) {
		return nil
	}
	if !coreConnected || !cfg.SystemProxy.Enabled {
		return Restore(stateFile, &target, run)
	}
	if !cfg.MixedInbound.Enabled || !validProxy(target) {
		return errors.New("system proxy requires a valid mixed inbound")
	}
	saved, hasSaved, err := load(stateFile)
	if err != nil {
		return err
	}
	if hasSaved && saved.Service != service {
		return fmt.Errorf("system proxy still manages %s; disable and restore before switching services", saved.Service)
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
			return errors.New("refusing to override an authenticated system proxy")
		}
		saved = previous{Service: service, HTTP: httpCurrent, HTTPS: httpsCurrent}
	} else if saved.Managed != nil {
		for i, current := range []Proxy{httpCurrent, httpsCurrent} {
			before := saved.HTTP
			if i == 1 {
				before = saved.HTTPS
			}
			if current != *saved.Managed && (saved.PriorManaged == nil || current != *saved.PriorManaged) && !equivalent(current, before) {
				return errors.New("system proxy changed externally; refusing to override it")
			}
		}
	}
	if hasSaved && saved.Managed != nil && *saved.Managed == target && httpCurrent == target && httpsCurrent == target {
		return nil
	}
	if saved.Managed != nil && *saved.Managed != target {
		copy := *saved.Managed
		saved.PriorManaged = &copy
	}
	saved.Managed = &target
	if err = save(stateFile, saved); err != nil {
		return err
	}
	if recovery.Held(filepath.Dir(stateFile)) {
		return nil
	}
	if httpCurrent != target {
		if err = setProxy(run, "web", service, target); err != nil {
			return err
		}
	}
	if httpsCurrent != target {
		if err = setProxy(run, "secureweb", service, target); err != nil {
			return err
		}
	}
	return nil
}
