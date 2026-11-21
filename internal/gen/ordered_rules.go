package gen

import "github.com/pi-dal/sakamoto/pkg/mobileconf"

// The compact SRS tail already implements geo-proxy, direct, proxy order.
// Emit just the prefix whose first-match semantics that tail cannot express.
func orderedPrefix(p *parsedConf) []mobileconf.OrderedRule {
	var rules []mobileconf.OrderedRule
	for _, target := range []string{"direct", "proxy"} {
		for _, r := range p.overrides {
			if r.Target == target {
				rules = append(rules, r)
			}
		}
	}
	for _, r := range p.ordered {
		if r.Target != "reject" { // The global rejection set runs before split routing.
			rules = append(rules, r)
		}
	}
	lastRank := 2
	for i := len(rules) - 1; i >= 0; i-- {
		r := rules[i]
		rank := 2
		if r.Target == "direct" {
			rank = 1
		} else if r.Kind == "rule_set" {
			rank = 0
		}
		if rank > lastRank {
			return rules[:i+1]
		}
		lastRank = rank
	}
	return nil
}

func orderedRoutes(prefix []mobileconf.OrderedRule, exit string) []map[string]any {
	var routes []map[string]any
	for len(prefix) > 0 {
		first := prefix[0]
		var values []string
		for len(prefix) > 0 && prefix[0].Target == first.Target && prefix[0].Kind == first.Kind {
			values = append(values, prefix[0].Value)
			prefix = prefix[1:]
		}
		outbound := exit
		if first.Target == "direct" {
			outbound = "direct"
		}
		routes = append(routes, map[string]any{"clash_mode": "Rule", first.Kind: values, "action": "route", "outbound": outbound})
	}
	return routes
}
