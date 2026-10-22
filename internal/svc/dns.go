package svc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/recovery"
	"github.com/pi-dal/sakamoto/internal/sysdns"
)

func (s *Server) dnsStatePath() string { return filepath.Join(s.workDir, sysdns.StateName) }

func (s *Server) dnsPlan() (*config.Config, bool, error) {
	cfg, err := config.Load(filepath.Join(s.workDir, "sakamoto.yaml"))
	if err != nil {
		return nil, false, err
	}
	if !cfg.DNSGuard.Enabled {
		return cfg, false, nil
	}
	b, err := os.ReadFile(s.cfgPath)
	if err != nil {
		return nil, false, err
	}
	var core struct {
		Inbounds []struct {
			Type, Tag, Listen string
			Port              int `json:"listen_port"`
		} `json:"inbounds"`
		DNS struct {
			Final   string                               `json:"final"`
			Servers []struct{ Tag, Type, Detour string } `json:"servers"`
		} `json:"dns"`
		Route struct {
			Rules []struct {
				Inbound []string
				Action  string
			}
		} `json:"route"`
	}
	if err := json.Unmarshal(b, &core); err != nil {
		return nil, false, err
	}
	listener := false
	for _, i := range core.Inbounds {
		if i.Type == "direct" && i.Tag == "protected-dns" && i.Listen == "127.0.0.1" && i.Port == 53 {
			listener = true
		}
	}
	resolver := false
	for _, d := range core.DNS.Servers {
		if d.Tag == core.DNS.Final && (d.Type == "https" || d.Type == "tls") && d.Detour != "" && d.Detour != "direct" {
			resolver = true
		}
	}
	hijack := len(core.Route.Rules) > 0 && core.Route.Rules[0].Action == "hijack-dns" && len(core.Route.Rules[0].Inbound) == 1 && core.Route.Rules[0].Inbound[0] == "protected-dns"
	if !listener || !resolver || !hijack {
		return nil, false, fmt.Errorf("protected DNS needs a regenerated config with native listener and proxy resolver")
	}
	return cfg, true, nil
}

// protectDNS is invoked after child Start while the lifecycle lock is held.
// The wait loop closes childDone without that lock, so failed startup is visible.
func (s *Server) protectDNS(ctx context.Context, cfg *config.Config, done <-chan struct{}) error {
	ctx, cancel := context.WithTimeout(ctx, s.dnsWait)
	defer cancel()
	var last error
	for ctx.Err() == nil {
		select {
		case <-done:
			return fmt.Errorf("core exited before DNS was ready")
		default:
		}
		last = s.dnsProbeContext(ctx, "127.0.0.1:53")
		if last == nil {
			select {
			case <-done:
				return fmt.Errorf("core exited during DNS readiness check")
			default:
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if recovery.Held(s.workDir) {
				return fmt.Errorf("maintenance hold canceled DNS takeover")
			}
			runner := func(args ...string) (string, error) { return s.dnsActivateRun(ctx, args...) }
			if err := sysdns.Activate(s.dnsStatePath(), cfg.DNSGuard.Service, runner); err != nil {
				return err
			}
			select {
			case <-done:
				return fmt.Errorf("core exited during DNS takeover")
			default:
				return nil
			}
		}
		timer := time.NewTimer(300 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-done:
			timer.Stop()
			return fmt.Errorf("core exited during DNS readiness")
		case <-timer.C:
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("native DNS did not become healthy: %w", last)
}
func (s *Server) restoreDNS() error { return sysdns.Restore(s.dnsStatePath(), s.dnsRun) }
func (s *Server) restoreDNSContext(ctx context.Context) error {
	return sysdns.Restore(s.dnsStatePath(), func(args ...string) (string, error) { return s.dnsActivateRun(ctx, args...) })
}
func (s *Server) restoreDNSCommand() string {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	s.mu.Lock()
	running := s.runningLocked()
	s.mu.Unlock()
	if running {
		return "restore failed: disconnect the core first"
	}
	if err := s.restoreDNS(); err != nil {
		return "restore failed: " + err.Error()
	}
	return "DNS restored"
}

func (s *Server) runningLocked() bool {
	if s.child == nil {
		return false
	}
	select {
	case <-s.childDone:
		return false
	default:
		return true
	}
}
