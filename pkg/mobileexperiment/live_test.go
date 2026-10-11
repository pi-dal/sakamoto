package mobileexperiment

import (
	"encoding/json"
	"testing"
)

func TestLiveRulesPreserveExplicitPriorityAndColdStartDomains(t *testing.T) {
	config := `{"route":{"rules":[{"action":"reject","domain":["blocked.example"]},{"action":"route","outbound":"direct","domain":["explicit.example"]},{"action":"route","outbound":"Exit","rule_set":["rs-proxy"]}],"rule_set":[{"type":"inline","tag":"rs-proxy","rules":[{"domain":["proxy.example"]}]}]}}`
	for _, domains := range []string{`[]`, `["learned.example"]`} {
		prepared, err := PrepareLiveRulesJSON(config, `[]`, domains)
		if err != nil {
			t.Fatal(err)
		}
		var parsed struct {
			Route struct {
				Rules []struct {
					Action   string   `json:"action"`
					Outbound string   `json:"outbound"`
					RuleSet  []string `json:"rule_set"`
				}
				Sets []struct {
					Tag   string                `json:"tag"`
					Rules []map[string][]string `json:"rules"`
				} `json:"rule_set"`
			}
		}
		if err = json.Unmarshal([]byte(prepared), &parsed); err != nil {
			t.Fatal(err)
		}
		rules := parsed.Route.Rules
		if len(rules) != 4 || rules[0].Action != "reject" || rules[1].Outbound != "direct" || rules[2].RuleSet[0] != AutomaticRuleSetTag || rules[3].RuleSet[0] != "rs-proxy" {
			t.Fatal("explicit routing priority changed", prepared)
		}
		set := parsed.Route.Sets[1]
		if set.Tag != AutomaticRuleSetTag || len(set.Rules) != 1 {
			t.Fatal("missing live slot")
		}
		if domains == `[]` && set.Rules[0]["domain_regex"][0] != "a^" {
			t.Fatal("empty slot can match normal traffic")
		}
		if domains != `[]` && set.Rules[0]["domain"][0] != "learned.example" {
			t.Fatal("cold-start domain lost")
		}
		if _, err = PrepareLiveRulesJSON(prepared, `[]`, domains); err == nil {
			t.Fatal("reserved tag collision was accepted")
		}
	}
	for _, invalid := range []string{`["127.0.0.1"]`, `["peer.ts.net"]`, `["https://bad.example"]`} {
		if _, err := PrepareLiveRulesJSON(config, `[]`, invalid); err == nil {
			t.Fatal("invalid automatic domain accepted", invalid)
		}
	}
}
