package mobilecore

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pi-dal/sakamoto/pkg/mobileconf"
)

// EnsureProxyServerRoutesJSON also protects imported configurations and node
// edits performed after generation. Existing user exclusions are retained.
func EnsureProxyServerRoutesJSON(content string) (string, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal([]byte(content), &root) != nil || root == nil {
		return "", fmt.Errorf("invalid configuration JSON")
	}
	var outbounds []map[string]any
	if raw, ok := root["outbounds"]; ok && json.Unmarshal(raw, &outbounds) != nil {
		return "", fmt.Errorf("invalid outbound configuration")
	}
	routes := mobileconf.ProxyServerRoutes(outbounds)
	if len(routes) == 0 {
		return content, nil
	}
	var inbounds []map[string]json.RawMessage
	if raw, ok := root["inbounds"]; !ok {
		return content, nil
	} else if json.Unmarshal(raw, &inbounds) != nil {
		return "", fmt.Errorf("invalid inbound configuration")
	}
	changed := false
	for _, inbound := range inbounds {
		var kind string
		_ = json.Unmarshal(inbound["type"], &kind)
		if kind != "tun" {
			continue
		}
		var excluded []string
		if raw, ok := inbound["route_exclude_address"]; ok {
			if json.Unmarshal(raw, &excluded) != nil {
				var single string
				if json.Unmarshal(raw, &single) != nil || strings.TrimSpace(single) == "" {
					return "", fmt.Errorf("invalid TUN route exclusions")
				}
				excluded = []string{single}
			}
		}
		seen := map[string]bool{}
		for _, route := range excluded {
			seen[route] = true
		}
		added := false
		for _, route := range routes {
			if !seen[route] {
				seen[route] = true
				excluded = append(excluded, route)
				added = true
			}
		}
		if added {
			inbound["route_exclude_address"], _ = json.Marshal(excluded)
			changed = true
		}
	}
	if !changed {
		return content, nil
	}
	root["inbounds"], _ = json.Marshal(inbounds)
	raw, err := json.Marshal(root)
	return string(raw), err
}
