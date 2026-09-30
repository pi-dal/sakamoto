// Package sysdns owns the root supervisor's transactional macOS DNS takeover.
// The DNS data plane remains inside sing-box. No external DNS service is started.
package sysdns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

const StateName = "dns-restore.json"

type Runner func(args ...string) (string, error)
type previous struct {
	Service string   `json:"service"`
	Servers []string `json:"servers"`
}

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
func currentDNS(run Runner, service string) ([]string, error) {
	if !validService(service) {
		return nil, errors.New("invalid DNS network service")
	}
	out, err := run("-getdnsservers", service)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(out)
	if strings.HasPrefix(text, "There aren't any DNS Servers set on ") {
		return []string{}, nil
	}
	if text == "" {
		return nil, errors.New("networksetup returned no DNS state")
	}
	var ips []string
	for _, line := range strings.Split(text, "\n") {
		ip, err := netip.ParseAddr(strings.TrimSpace(line))
		if err != nil {
			return nil, errors.New("networksetup DNS response was not an IP list")
		}
		ips = append(ips, ip.String())
	}
	return ips, nil
}
func setDNS(run Runner, service string, servers []string) error {
	args := []string{"-setdnsservers", service}
	if len(servers) == 0 {
		args = append(args, "Empty")
	} else {
		args = append(args, servers...)
	}
	if _, err := run(args...); err != nil {
		return err
	}
	after, err := currentDNS(run, service)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(after, servers) {
		return errors.New("system DNS read-back did not match the requested state")
	}
	return nil
}
func savedDNS(path string) (previous, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return previous{}, false, nil
	}
	if err != nil {
		return previous{}, false, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 4096 {
		return previous{}, false, errors.New("unsafe DNS restore state")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return previous{}, false, err
	}
	var p previous
	if err := json.Unmarshal(b, &p); err != nil {
		return previous{}, false, err
	}
	if !validService(p.Service) {
		return previous{}, false, errors.New("invalid saved DNS service")
	}
	if p.Servers == nil {
		p.Servers = []string{}
	}
	for _, server := range p.Servers {
		if _, err := netip.ParseAddr(server); err != nil {
			return previous{}, false, errors.New("invalid saved DNS server")
		}
	}
	return p, true, nil
}
func writeSaved(path string, p previous) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".dns-restore-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Activate is called only after the core's UDP and TCP DNS probes succeed.
// A persisted snapshot survives child restarts and is never replaced by 127.0.0.1.
func Activate(path, service string, run Runner) error {
	saved, exists, err := savedDNS(path)
	if err != nil {
		return err
	}
	current, err := currentDNS(run, service)
	if err != nil {
		return err
	}
	target := []string{"127.0.0.1"}
	if exists {
		if saved.Service != service {
			return errors.New("DNS restore state belongs to another network service")
		}
		if reflect.DeepEqual(current, target) {
			return nil
		}
		if !reflect.DeepEqual(current, saved.Servers) {
			return errors.New("system DNS was changed externally; refusing to override it")
		}
	} else {
		if reflect.DeepEqual(current, target) {
			return errors.New("loopback DNS is configured without a restore snapshot; resolve manually")
		}
		saved = previous{Service: service, Servers: current}
		if err := writeSaved(path, saved); err != nil {
			return err
		}
	}
	if err := setDNS(run, service, target); err != nil {
		restoreErr := Restore(path, run)
		return errors.Join(fmt.Errorf("DNS takeover failed: %w", err), restoreErr)
	}
	return nil
}

// Restore must succeed before an intentional core stop. On failure keep both
// the snapshot and core alive so the loopback resolver cannot become a black hole.
func Restore(path string, run Runner) error {
	saved, exists, err := savedDNS(path)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	current, err := currentDNS(run, saved.Service)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, []string{"127.0.0.1"}) && !reflect.DeepEqual(current, saved.Servers) {
		for _, ip := range current {
			if ip == "127.0.0.1" {
				return errors.New("mixed loopback DNS changed externally; restore snapshot retained")
			}
		}
		return os.Remove(path) // Preserve an explicit non-loopback user override.
	}
	if !reflect.DeepEqual(current, saved.Servers) {
		if err := setDNS(run, saved.Service, saved.Servers); err != nil {
			return fmt.Errorf("restore system DNS: %w", err)
		}
	}
	return os.Remove(path)
}
