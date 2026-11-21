package svc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/recovery"
	"github.com/pi-dal/sakamoto/internal/sysdns"
	"github.com/pi-dal/sakamoto/pkg/regionaldns"
)

func (s *Server) dnsStatePath() string { return filepath.Join(s.workDir, sysdns.StateName) }

func (s *Server) dnsPlan() (*config.Config, bool, error) { return dnsPlanFor(s.workDir, s.cfgPath) }
func dnsPlanFor(dir, path string) (*config.Config, bool, error) {
	cfg, err := config.Load(filepath.Join(dir, "sakamoto.yaml"))
	if err != nil {
		return nil, false, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	if err := regionaldns.Verify(b, cfg.RegionalDNS, cfg.DNSGuard.LocalDomains); err != nil {
		return nil, false, err
	}
	if !cfg.DNSGuard.Enabled {
		return cfg, false, nil
	}
	var core struct {
		Inbounds []struct {
			Type, Tag, Listen string
			Port              int `json:"listen_port"`
		} `json:"inbounds"`
		Outbounds []dnsOutbound
		DNS       struct {
			Final   string `json:"final"`
			Servers []struct {
				Tag, Type, Detour, Server string
				Resolver                  json.RawMessage `json:"domain_resolver"`
			} `json:"servers"`
			Rules []map[string]any `json:"rules"`
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
			resolver = protectedDetour(core.Outbounds, d.Detour)
		}
	}
	private := map[string]bool{}
	hosts := map[string]bool{}
	magic := false
	for _, s := range core.DNS.Servers {
		ip, err := netip.ParseAddr(s.Server)
		private[s.Tag] = err == nil && ip.Unmap().IsPrivate() && s.Type == "udp" && s.Detour == ""
		hosts[s.Tag] = s.Type == "hosts"
		if s.Tag == "ts-dns" {
			magic = s.Type == "udp" && s.Server == "100.100.100.100" && s.Detour == ""
		}
	}
	for _, s := range core.DNS.Servers {
		name := config.BootstrapName(s.Server)
		magicName := magic && (name == "ts.net" || strings.HasSuffix(name, ".ts.net"))
		if name == "" || (!magicName && !cfg.IsLocalDNSName(name)) {
			continue
		}
		var resolver string
		if json.Unmarshal(s.Resolver, &resolver) != nil {
			var options struct{ Server string }
			if json.Unmarshal(s.Resolver, &options) == nil {
				resolver = options.Server
			}
		}
		if (magicName && resolver != "ts-dns") || (!magicName && !private[resolver]) {
			return nil, false, fmt.Errorf("unsafe private DNS bootstrap; regenerate before connecting")
		}
	}
	rules := core.DNS.Rules
	if cfg.RegionalDNS.Domains != "" {
		// Verify authenticated these four exact-name rules and checked overlap.
		if len(rules) < 4 || rules[0]["preferred_by"] != nil {
			return nil, false, fmt.Errorf("unexpected regional DNS precedence")
		}
		rules = rules[4:]
	}
	if len(cfg.DNSGuard.LocalDomains) > 0 && !protectedPrivateRules(rules, cfg.DNSGuard.LocalDomains, private, hosts, magic) {
		return nil, false, fmt.Errorf("private DNS routing differs from protected settings; regenerate before connecting")
	}
	hijack := len(core.Route.Rules) > 0 && core.Route.Rules[0].Action == "hijack-dns" && len(core.Route.Rules[0].Inbound) == 1 && core.Route.Rules[0].Inbound[0] == "protected-dns"
	if !listener || !resolver || !hijack {
		return nil, false, fmt.Errorf("protected DNS needs a regenerated config with native listener and proxy resolver")
	}
	return cfg, true, nil
}

// Check precedence as well as the destination. An earlier public rule can
// consume the query before the private suffix rule is evaluated.
func protectedPrivateRules(rules []map[string]any, suffixes []string, private, hosts map[string]bool, magic bool) bool {
	names := make([]any, len(suffixes))
	for i, s := range suffixes {
		names[i] = s
	}
	for _, r := range rules {
		server, _ := r["server"].(string)
		if len(r) == 3 && r["domain"] != nil && r["action"] == "route" && hosts[server] {
			continue
		}
		if magic && reflect.DeepEqual(r, map[string]any{"domain_suffix": []any{"ts.net"}, "server": "ts-dns"}) {
			continue
		}
		if len(r) != 3 || !reflect.DeepEqual(r["domain_suffix"], names) {
			return false
		}
		return (r["action"] == "route" && private[server]) || (r["action"] == "predefined" && r["rcode"] == "SERVFAIL")
	}
	return false
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
