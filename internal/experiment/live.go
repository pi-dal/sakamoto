package experiment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

const LiveRuleSetTag = "sakamoto-auto-proxy"
const LiveRuleFileName = "auto-proxy-rules.json"

// PrepareLiveRules is a generation operation, never an activation operation.
// The native local rule-set watcher later replaces domain matching in place.
func PrepareLiveRules(raw []byte, dir string, committed []string) ([]byte, error) {
	proxy, err := ProxyOutbound(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	route, ok := root["route"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing route")
	}
	rules, ok := route["rules"].([]any)
	if !ok {
		return nil, fmt.Errorf("missing route rules")
	}
	sets, _ := route["rule_set"].([]any)
	for _, item := range sets {
		set, _ := item.(map[string]any)
		if set["tag"] == LiveRuleSetTag {
			return nil, fmt.Errorf("reserved automatic rule-set already exists")
		}
	}
	old := slices.Clone(committed)
	sort.Strings(old)
	next := make([]any, 0, len(rules)+1)
	inserted := false
	for _, item := range rules {
		rule, _ := item.(map[string]any)
		// Remove only the exact legacy rule described by committed state.
		if len(old) > 0 && len(rule) == 3 && rule["action"] == "route" && rule["outbound"] == proxy {
			if names, ok := rule["domain"].([]any); ok {
				values := make([]string, 0, len(names))
				for _, value := range names {
					if value, ok := value.(string); ok {
						values = append(values, value)
					}
				}
				sort.Strings(values)
				if slices.Equal(values, old) {
					continue
				}
			}
		}
		refs, _ := rule["rule_set"].([]any)
		if !inserted && len(refs) == 1 && refs[0] == "rs-proxy" {
			next = append(next, map[string]any{"rule_set": []string{LiveRuleSetTag}, "action": "route", "outbound": proxy})
			inserted = true
		}
		next = append(next, item)
	}
	if !inserted {
		return nil, fmt.Errorf("missing explicit proxy rule")
	}
	path, err := filepath.Abs(filepath.Join(dir, LiveRuleFileName))
	if err != nil {
		return nil, err
	}
	route["rules"] = next
	route["rule_set"] = append(sets, map[string]any{"type": "local", "tag": LiveRuleSetTag, "format": "source", "path": path})
	return json.MarshalIndent(root, "", "  ")
}

func LiveRulesReady(raw []byte, dir string) bool {
	var root struct {
		Route struct {
			RuleSets []struct{ Type, Tag, Format, Path string } `json:"rule_set"`
			Rules    []struct {
				RuleSets         []string `json:"rule_set"`
				Action, Outbound string
			}
		}
	}
	if json.Unmarshal(raw, &root) != nil {
		return false
	}
	expected, err := filepath.Abs(filepath.Join(dir, LiveRuleFileName))
	if err != nil {
		return false
	}
	proxy, err := ProxyOutbound(raw)
	if err != nil {
		return false
	}
	found := false
	for _, set := range root.Route.RuleSets {
		if set.Tag == LiveRuleSetTag {
			if found || set.Type != "local" || set.Format != "source" || filepath.Clean(set.Path) != expected {
				return false
			}
			found = true
		}
	}
	if !found {
		return false
	}
	for _, rule := range root.Route.Rules {
		if slices.Equal(rule.RuleSets, []string{LiveRuleSetTag}) {
			return rule.Action == "route" && rule.Outbound == proxy
		}
	}
	return false
}

func WriteLiveRules(dir string, domains []string) error {
	if len(domains) > 1000 {
		return fmt.Errorf("learned domain limit reached")
	}
	for _, domain := range domains {
		if Domain(domain) != domain {
			return fmt.Errorf("invalid learned domain")
		}
	}
	rules := []any{}
	if len(domains) > 0 {
		rules = append(rules, map[string]any{"domain": domains})
	}
	raw, err := json.Marshal(map[string]any{"version": 4, "rules": rules})
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, LiveRuleFileName), raw)
}

// StageLiveRules changes only the owned file. Committed state is written after
// origin verification; a watcher restart restores the last committed file.
func StageLiveRules(dir, domain string) (State, func() error, error) {
	previous, err := Load(dir)
	if err != nil {
		return State{}, nil, err
	}
	if Domain(domain) != domain || len(previous.Domains) >= 1000 || slices.Contains(previous.Domains, domain) {
		return State{}, nil, fmt.Errorf("invalid or already learned domain")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil || !LiveRulesReady(raw, dir) {
		return State{}, nil, fmt.Errorf("live learning slot is unavailable; apply configuration explicitly")
	}
	next := State{Domains: append(slices.Clone(previous.Domains), domain)}
	sort.Strings(next.Domains)
	if err := WriteLiveRules(dir, next.Domains); err != nil {
		return State{}, nil, err
	}
	return next, func() error { return WriteLiveRules(dir, previous.Domains) }, nil
}
