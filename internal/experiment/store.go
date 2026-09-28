package experiment

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const FileName = "auto-proxy.json"

var hostname = regexp.MustCompile(`(?i)^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type State struct {
	Domains []string `json:"domains"`
}

func Domain(name string) string {
	name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	if len(name) > 253 || !strings.Contains(name, ".") || !hostname.MatchString(name) || net.ParseIP(name) != nil || strings.HasSuffix(name, ".local") || strings.HasSuffix(name, ".ts.net") {
		return ""
	}
	return name
}
func Load(dir string) (State, error) {
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if os.IsNotExist(err) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(b, &state); err != nil {
		return State{}, err
	}
	if len(state.Domains) > 1000 {
		return State{}, errors.New("auto proxy learned too many domains")
	}
	for _, d := range state.Domains {
		if Domain(d) != d {
			return State{}, fmt.Errorf("invalid auto-proxy domain: %q", d)
		}
	}
	return state, nil
}
func Save(dir string, state State) error {
	if len(state.Domains) > 1000 {
		return errors.New("auto proxy learned too many domains")
	}
	set := map[string]bool{}
	for _, d := range state.Domains {
		if Domain(d) != d {
			return fmt.Errorf("invalid auto-proxy domain: %q", d)
		}
		set[d] = true
	}
	state.Domains = state.Domains[:0]
	for d := range set {
		state.Domains = append(state.Domains, d)
	}
	sort.Strings(state.Domains)
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, FileName), b)
}
func writeAtomic(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".auto-proxy-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// ProxyOutbound extracts the configured chain exit instead of guessing a tag.
func ProxyOutbound(raw []byte) (string, error) {
	var cfg struct {
		Route struct {
			Rules []struct {
				RuleSet  []string `json:"rule_set"`
				Outbound string   `json:"outbound"`
			} `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "", err
	}
	for _, r := range cfg.Route.Rules {
		if len(r.RuleSet) == 1 && r.RuleSet[0] == "rs-proxy" && r.Outbound != "" {
			return r.Outbound, nil
		}
	}
	return "", errors.New("no explicit proxy outbound")
}

// AddRule inserts learned names after explicit DIRECT rules but before explicit
// PROXY rules. It never overrides a source REJECT or DIRECT rule.
func AddRule(raw []byte, domains []string, outbound string) ([]byte, error) {
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	route, ok := data["route"].(map[string]any)
	if !ok {
		return nil, errors.New("missing route")
	}
	rules, ok := route["rules"].([]any)
	if !ok {
		return nil, errors.New("missing route.rules")
	}
	proxyAt := -1
	oldAt := -1
	for i, r := range rules {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		sets, ok := m["rule_set"].([]any)
		if ok && len(sets) == 1 && sets[0] == "rs-proxy" {
			proxyAt = i
		}
		if m["_auto_proxy"] != nil {
			return nil, errors.New("invalid auto rule marker")
		}
		if m["domain"] != nil && m["outbound"] == outbound && m["action"] == "route" {
			oldAt = i
		}
	}
	if proxyAt < 0 {
		return nil, errors.New("missing explicit proxy rule")
	}
	if rules[proxyAt].(map[string]any)["outbound"] != outbound {
		return nil, errors.New("proxy outbound changed; refusing auto update")
	}
	if oldAt >= 0 {
		rules = append(rules[:oldAt], rules[oldAt+1:]...)
		if oldAt < proxyAt {
			proxyAt--
		}
	}
	if len(domains) > 0 {
		values := make([]string, len(domains))
		copy(values, domains)
		rule := map[string]any{"domain": values, "action": "route", "outbound": outbound}
		rules = append(rules, nil)
		copy(rules[proxyAt+1:], rules[proxyAt:])
		rules[proxyAt] = rule
	}
	route["rules"] = rules
	return json.MarshalIndent(data, "", "  ")
}

// Stage prepares and validates an updated generated config without activating it.
func Stage(dir, domain, outbound string) (func() error, error) {
	if Domain(domain) != domain {
		return nil, errors.New("invalid learned domain")
	}
	state, err := Load(dir)
	if err != nil {
		return nil, err
	}
	for _, v := range state.Domains {
		if v == domain {
			return nil, nil
		}
	}
	if len(state.Domains) >= 1000 {
		return nil, errors.New("auto proxy limit reached")
	}
	state.Domains = append(state.Domains, domain)
	sort.Strings(state.Domains)
	path := filepath.Join(dir, "config.json")
	old, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	newConfig, err := AddRule(old, state.Domains, outbound)
	if err != nil {
		return nil, err
	}
	candidate, err := os.CreateTemp(dir, ".auto-check-*.json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(candidate.Name()) }()
	if _, err = candidate.Write(newConfig); err != nil {
		_ = candidate.Close()
		return nil, err
	}
	if err = candidate.Close(); err != nil {
		return nil, err
	}
	if output, err := exec.Command("sing-box", "check", "-D", dir, "-c", candidate.Name()).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("auto rule check: %v: %s", err, strings.TrimSpace(string(output)))
	}
	prevState, stateErr := os.ReadFile(filepath.Join(dir, FileName))
	if stateErr != nil && !os.IsNotExist(stateErr) {
		return nil, stateErr
	}
	if err = Save(dir, state); err != nil {
		return nil, err
	}
	if err = writeAtomic(path, newConfig); err != nil {
		restoreState(dir, prevState)
		return nil, err
	}
	return func() error {
		if e := writeAtomic(path, old); e != nil {
			return e
		}
		restoreState(dir, prevState)
		return nil
	}, nil
}
func restoreState(dir string, prev []byte) {
	if prev == nil {
		_ = os.Remove(filepath.Join(dir, FileName))
		return
	}
	_ = writeAtomic(filepath.Join(dir, FileName), prev)
}
