package mobilecore

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"

	"go4.org/netipx"
)

var tailnetPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fd7a:115c:a1e0::/48"),
}

// PrepareTailscaleConnectivityJSON repairs the runtime configuration, including
// old/imported profiles. A tailnet route in the core cannot help if the OS has
// already excluded that address from the TUN. Desktop's external Tailscale
// bypass policy must not apply to the embedded mobile endpoint.
func PrepareTailscaleConnectivityJSON(configJSON string) (string, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(configJSON), &root); err != nil || root == nil {
		return "", fmt.Errorf("invalid mobile configuration")
	}
	var endpoints []struct {
		Type string `json:"type"`
		Tag  string `json:"tag"`
	}
	if err := json.Unmarshal(root["endpoints"], &endpoints); err != nil && root["endpoints"] != nil {
		return "", fmt.Errorf("invalid endpoints")
	}
	var tags []string
	for _, endpoint := range endpoints {
		if endpoint.Type == "tailscale" {
			if endpoint.Tag == "" {
				return "", fmt.Errorf("tailscale endpoint requires a tag")
			}
			tags = append(tags, endpoint.Tag)
		}
	}
	if len(tags) == 0 {
		return configJSON, nil
	}

	var inbounds []map[string]json.RawMessage
	if err := json.Unmarshal(root["inbounds"], &inbounds); err != nil && root["inbounds"] != nil {
		return "", fmt.Errorf("invalid inbounds")
	}
	for _, inbound := range inbounds {
		var kind string
		_ = json.Unmarshal(inbound["type"], &kind)
		if kind != "tun" {
			continue
		}
		var exclusions []string
		if raw := inbound["route_exclude_address"]; raw != nil {
			var err error
			exclusions, err = routeStrings(raw)
			if err != nil {
				return "", fmt.Errorf("invalid TUN route exclusions")
			}
			var excluded netipx.IPSetBuilder
			for _, text := range exclusions {
				prefix, err := netip.ParsePrefix(text)
				if err != nil {
					return "", fmt.Errorf("invalid TUN route exclusion")
				}
				excluded.AddPrefix(prefix.Masked())
			}
			for _, tailnet := range tailnetPrefixes {
				excluded.RemovePrefix(tailnet)
			}
			set, err := excluded.IPSet()
			if err != nil {
				return "", fmt.Errorf("invalid TUN route exclusions")
			}
			var repaired []string
			for _, prefix := range set.Prefixes() {
				repaired = append(repaired, prefix.String())
			}
			if len(repaired) == 0 {
				delete(inbound, "route_exclude_address")
			} else {
				inbound["route_exclude_address"] = marshalJSON(repaired)
			}
		}
		var included, sets []string
		if raw := inbound["route_address"]; raw != nil {
			var err error
			included, err = routeStrings(raw)
			if err != nil {
				return "", fmt.Errorf("invalid TUN routes")
			}
		}
		var err error
		sets, err = routeStrings(inbound["route_address_set"])
		if err != nil {
			return "", fmt.Errorf("invalid TUN route sets")
		}
		if len(included) > 0 || len(sets) > 0 {
			for _, tailnet := range tailnetPrefixes {
				covered := false
				for _, text := range included {
					prefix, err := netip.ParsePrefix(text)
					if err != nil {
						return "", fmt.Errorf("invalid TUN route")
					}
					if prefix.Bits() <= tailnet.Bits() && prefix.Contains(tailnet.Addr()) {
						covered = true
						break
					}
				}
				if !covered {
					included = append(included, tailnet.String())
				}
			}
			inbound["route_address"] = marshalJSON(included)
		}
	}
	root["inbounds"] = marshalJSON(inbounds)

	var route map[string]json.RawMessage
	if err := json.Unmarshal(root["route"], &route); err != nil && root["route"] != nil {
		return "", fmt.Errorf("invalid route")
	}
	if route == nil {
		route = make(map[string]json.RawMessage)
	}
	var rules []map[string]json.RawMessage
	if err := json.Unmarshal(route["rules"], &rules); err != nil && route["rules"] != nil {
		return "", fmt.Errorf("invalid route rules")
	}
	// Replace only our simple preferred-route rules; retain rules with extra
	// user conditions. All endpoints are tried dynamically before the fallback.
	rules = slices.DeleteFunc(rules, func(rule map[string]json.RawMessage) bool {
		var preferred []string
		var outbound, action string
		_ = json.Unmarshal(rule["preferred_by"], &preferred)
		_ = json.Unmarshal(rule["outbound"], &outbound)
		_ = json.Unmarshal(rule["action"], &action)
		return len(preferred) == 1 && preferred[0] == outbound && slices.Contains(tags, outbound) &&
			(action == "route" && len(rule) == 3 || action == "" && len(rule) == 2)
	})
	at := 0
	for at < len(rules) {
		var action string
		_ = json.Unmarshal(rules[at]["action"], &action)
		if action != "sniff" && action != "hijack-dns" {
			break
		}
		at++
	}
	var connectivity []map[string]json.RawMessage
	for _, tag := range tags {
		connectivity = append(connectivity, map[string]json.RawMessage{"preferred_by": marshalJSON([]string{tag}), "action": marshalJSON("route"), "outbound": marshalJSON(tag)})
	}
	// With one endpoint, literal tailnet addresses must never fall through to
	// DIRECT/proxy just because the peer map is not ready or lacks that peer.
	if len(tags) == 1 {
		static := map[string]json.RawMessage{"ip_cidr": marshalJSON([]string{"100.64.0.0/10", "fd7a:115c:a1e0::/48"}), "action": marshalJSON("route"), "outbound": marshalJSON(tags[0])}
		rules = slices.DeleteFunc(rules, func(rule map[string]json.RawMessage) bool {
			return string(marshalJSON(rule)) == string(marshalJSON(static))
		})
		connectivity = append(connectivity, static)
	}
	rules = slices.Insert(rules, at, connectivity...)
	route["rules"] = marshalJSON(rules)
	root["route"] = marshalJSON(route)
	return string(marshalJSON(root)), nil
}

func routeStrings(raw json.RawMessage) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err == nil {
		return values, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return []string{value}, nil
}

func marshalJSON(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
