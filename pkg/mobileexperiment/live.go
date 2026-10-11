package mobileexperiment

import (
	"encoding/json"
	"fmt"

	"github.com/pi-dal/sakamoto/internal/experiment"
)

// AutomaticRuleSetTag is reserved for the mobile provider's live learning slot.
const AutomaticRuleSetTag = "sakamoto-auto-proxy"

// PrepareLiveRulesJSON installs one inline slot at the existing learned-rule
// position. Its contents can later change without rebuilding the core. Reject
// a colliding user tag instead of taking ownership of someone else's rules.
func PrepareLiveRulesJSON(content, previousJSON, domainsJSON string) (string, error) {
	base, err := OwnedRulesJSON(content, previousJSON, "[]")
	if err != nil {
		return "", err
	}
	var domains []string
	if err = json.Unmarshal([]byte(domainsJSON), &domains); err != nil {
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
	proxy, err := ProxyOutbound(base)
	if err != nil {
		return "", err
	}
	var root map[string]any
	if err = json.Unmarshal([]byte(base), &root); err != nil {
		return "", err
	}
	route := root["route"].(map[string]any)
	rules := route["rules"].([]any)
	sets, _ := route["rule_set"].([]any)
	for _, item := range sets {
		set, ok := item.(map[string]any)
		if !ok {
			return "", fmt.Errorf("invalid rule-set")
		}
		tags := []any{set["tag"]}
		if list, ok := set["tag"].([]any); ok {
			tags = list
		}
		for _, tag := range tags {
			if tag == AutomaticRuleSetTag {
				return "", fmt.Errorf("reserved automatic rule-set tag is already in use")
			}
		}
	}
	for i, item := range rules {
		rule, ok := item.(map[string]any)
		if !ok {
			continue
		}
		refs, _ := rule["rule_set"].([]any)
		if len(refs) == 1 && refs[0] == "rs-proxy" {
			live := map[string]any{"rule_set": []string{AutomaticRuleSetTag}, "action": "route", "outbound": proxy}
			rules = append(rules, nil)
			copy(rules[i+1:], rules[i:])
			rules[i] = live
			route["rules"] = rules
			// An inline set must contain a rule at startup. a^ cannot match any
			// string, so an empty learned list never changes routing.
			headless := map[string]any{"domain_regex": []string{"a^"}}
			if len(domains) > 0 {
				headless = map[string]any{"domain": domains}
			}
			route["rule_set"] = append(sets, map[string]any{"type": "inline", "tag": AutomaticRuleSetTag, "rules": []any{headless}})
			data, encodeErr := json.Marshal(root)
			return string(data), encodeErr
		}
	}
	return "", fmt.Errorf("missing explicit proxy rule")
}
