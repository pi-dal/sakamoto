package mobileexperiment

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"github.com/pi-dal/sakamoto/internal/experiment"
)

const AutomaticRuleSetTag = "sakamoto-auto-proxy"

func PrepareLiveRulesJSON(content, previousJSON, domainsJSON string) (string, error) {
	return updateMobileSlot(content, previousJSON, domainsJSON, prepareSlot)
}

// ResumePausedRulesJSON restores the saved names at the dormant route's current
// position. An empty list forgets the last paused record and removes its slot.
func ResumePausedRulesJSON(content, domainsJSON string) (string, error) {
	return updateMobileSlot(content, "[]", domainsJSON, resumeSlot)
}

type slotOperation int

const (
	editSlot slotOperation = iota
	prepareSlot
	resumeSlot
)

// Apply, learning updates and legacy migration must all respect saved route order.
func updateMobileSlot(content, previousJSON, domainsJSON string, operation slotOperation) (string, error) {
	var previous, domains []string
	if err := json.Unmarshal([]byte(previousJSON), &previous); err != nil {
		return "", err
	}
	if err := json.Unmarshal([]byte(domainsJSON), &domains); err != nil {
		return "", err
	}
	if len(domains) > 1000 {
		return "", fmt.Errorf("learned domain limit reached")
	}
	for _, name := range domains {
		if experiment.Domain(name) != name {
			return "", fmt.Errorf("invalid learned domain")
		}
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return "", err
	}
	route, _ := root["route"].(map[string]any)
	rules, ok := route["rules"].([]any)
	if !ok {
		return "", fmt.Errorf("missing route rules")
	}
	sets, _ := route["rule_set"].([]any)
	keep := operation == prepareSlot || len(domains) > 0
	head := map[string]any{"domain_regex": []string{"a^"}}
	if len(domains) > 0 {
		head = map[string]any{"domain": domains}
	}
	for i, item := range sets {
		set, _ := item.(map[string]any)
		if set["tag"] != AutomaticRuleSetTag {
			continue
		}
		heads, _ := set["rules"].([]any)
		if set["type"] != "inline" || len(set) != 3 || len(heads) != 1 {
			return "", fmt.Errorf("reserved automatic rule-set conflict")
		}
		old, _ := heads[0].(map[string]any)
		matches := len(previous) > 0 && matchesDomains(old["domain"], previous)
		if operation == resumeSlot {
			matches = matchesDomains(old["domain_regex"], []string{"a^"})
		}
		if len(old) != 1 || !matches {
			return "", fmt.Errorf("automatic rule-set changed; reimport profile")
		}
		if keep {
			set["rules"] = []any{head}
		} else {
			route["rule_set"] = slices.Delete(sets, i, i+1)
			route["rules"] = slices.DeleteFunc(rules, func(item any) bool { return refersTo(item, AutomaticRuleSetTag) })
		}
		raw, err := json.Marshal(root)
		return string(raw), err
	}
	if !keep && len(previous) == 0 {
		return content, nil
	}
	if operation == resumeSlot {
		return "", fmt.Errorf("saved learning rule position is missing. Remove the old learned records before enabling learning again")
	}
	proxy, err := ProxyOutbound(content)
	if err != nil {
		return "", err
	}
	anchor, legacy := -1, -1
	for i, item := range rules {
		if anchor < 0 && refersTo(item, "rs-proxy") {
			anchor = i
		}
		rule, _ := item.(map[string]any)
		if len(previous) > 0 && len(rule) == 3 && rule["action"] == "route" && rule["outbound"] == proxy && matchesDomains(rule["domain"], previous) {
			if legacy >= 0 {
				return "", fmt.Errorf("ambiguous legacy learning rules; reimport the profile")
			}
			legacy = i
		}
	}
	if keep {
		ref := map[string]any{"rule_set": []string{AutomaticRuleSetTag}, "action": "route", "outbound": proxy}
		if legacy >= 0 {
			rules[legacy] = ref
		} else {
			if anchor < 0 {
				return "", fmt.Errorf("missing explicit proxy rule")
			}
			rules = slices.Insert(rules, anchor, any(ref))
		}
		route["rule_set"] = append(sets, map[string]any{"type": "inline", "tag": AutomaticRuleSetTag, "rules": []any{head}})
	} else if legacy >= 0 {
		rules = slices.Delete(rules, legacy, legacy+1)
	}
	route["rules"] = rules
	raw, err := json.Marshal(root)
	return string(raw), err
}

func refersTo(item any, tag string) bool {
	rule, _ := item.(map[string]any)
	refs, _ := rule["rule_set"].([]any)
	return len(refs) == 1 && refs[0] == tag
}

func matchesDomains(raw any, expected []string) bool {
	values, ok := raw.([]any)
	if !ok || len(values) != len(expected) {
		return false
	}
	names := make([]string, len(values))
	for i, value := range values {
		names[i], ok = value.(string)
		if !ok {
			return false
		}
	}
	want := slices.Clone(expected)
	sort.Strings(want)
	sort.Strings(names)
	return slices.Equal(names, want)
}
