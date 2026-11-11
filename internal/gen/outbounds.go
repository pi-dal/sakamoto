package gen

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/pi-dal/sakamoto/internal/config"
)

// AutomaticGroupTag uses the generator's node category for membership editors.
func AutomaticGroupTag(node map[string]any) string {
	typeName, _ := node["type"].(string)
	switch typeName {
	case "", "selector", "urltest", "direct", "block", "dns", "socks":
		return ""
	}
	tag, _ := node["tag"].(string)
	if isReality(node) || strings.Contains(strings.ToLower(tag), "reality") {
		return "RealityAuto"
	}
	return "OthersAuto"
}

func automaticMembers(group string, tags, excluded []string) ([]string, error) {
	omitted := map[string]bool{}
	for _, tag := range excluded {
		omitted[tag] = true
	}
	members := make([]string, 0, len(tags))
	for _, tag := range tags {
		if !omitted[tag] {
			members = append(members, tag)
		}
	}
	if len(tags) > 0 && len(members) == 0 {
		return nil, fmt.Errorf("%s would have no available members; include at least one node in Proxy groups", group)
	}
	return members, nil
}

// buildOutbounds merges manual nodes before subscriptions, classifies automatic groups,
// and keeps the configured chain exit separate from selectable entry nodes.
func buildOutbounds(o Options, cfg *config.Config, hc *http.Client) ([]map[string]any, []map[string]any, string, []string, error) {
	if err := cfg.ValidateAutomaticGroups(); err != nil {
		return nil, nil, "", nil, err
	}
	// 3) Nodes.
	allow := map[string]bool{}
	for _, h := range strings.Split(o.AllowHosts, ",") {
		if h = strings.TrimSpace(h); h != "" {
			allow[h] = true
		}
	}
	// Prefer allowlisted srjson and nodes.txt entries, then subscriptions; deduplicate tags.
	var nodes []map[string]any
	if len(allow) > 0 {
		imported, err := srJSONNodes(o.SRJSONPath, allow)
		if err != nil {
			return nil, nil, "", nil, fmt.Errorf("read Shadowrocket export: %w", err)
		}
		nodes = append(nodes, imported...)
	}
	nodes = append(nodes, parseShareLinks(o.NodesFile)...)
	for _, s := range cfg.Subscriptions {
		imported, err := fetchSub(s, hc)
		if err != nil {
			return nil, nil, "", nil, err
		}
		nodes = append(nodes, imported...)
	}
	seen := map[string]bool{}
	dedup := nodes[:0]
	for _, n := range nodes {
		t, _ := n["tag"].(string)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		dedup = append(dedup, n)
	}
	nodes = dedup
	var tags []string
	for _, n := range nodes {
		tags = append(tags, n["tag"].(string))
	}
	reportf("  nodes: %v\n", tags)

	// Apply the global uTLS fingerprint override to every eligible node.
	if cfg.UTLSFingerprint != "" {
		for _, n := range nodes {
			if tls, ok := n["tls"].(map[string]any); ok && tls["enabled"] == true {
				tls["utls"] = map[string]any{"enabled": true, "fingerprint": cfg.UTLSFingerprint}
			}
		}
	}

	// An explicit exit tag separates the role of a node from its protocol.
	// Without one, retain the first SOCKS exit used by existing profiles.
	explicitExit := strings.TrimSpace(cfg.ChainExit)
	if explicitExit == "" {
		explicitExit = strings.TrimSpace(cfg.SocksExit)
	}
	var chainExit string
	if cfg.ChainEnabled {
		chainExit = explicitExit
		if chainExit == "" {
			for _, n := range nodes {
				if n["type"] == "socks" {
					chainExit, _ = n["tag"].(string)
					break
				}
			}
		} else {
			var found map[string]any
			for _, n := range nodes {
				if n["tag"] == chainExit {
					found = n
					break
				}
			}
			if found == nil || !chainableOutbound(found) {
				return nil, nil, "", nil, fmt.Errorf("choose an existing proxy node for chain_exit")
			}
		}
	}
	var dataNodes []map[string]any
	for _, n := range nodes {
		if n["tag"] == chainExit || (cfg.ChainEnabled && explicitExit == "" && n["type"] == "socks") {
			continue
		}
		dataNodes = append(dataNodes, n)
	}
	if cfg.ChainEnabled && explicitExit != "" && len(dataNodes) == 0 {
		return nil, nil, "", nil, fmt.Errorf("chain_exit requires at least one separate upstream node")
	}

	var realityTags, otherTags []string
	for _, n := range dataNodes {
		if AutomaticGroupTag(n) == "RealityAuto" {
			realityTags = append(realityTags, n["tag"].(string)) // Reality protocol or a tag containing "reality".
		} else {
			otherTags = append(otherTags, n["tag"].(string))
		}
	}
	var err error
	realityTags, err = automaticMembers("RealityAuto", realityTags, cfg.AutomaticGroupExclusions["RealityAuto"])
	if err != nil {
		return nil, nil, "", nil, err
	}
	otherTags, err = automaticMembers("OthersAuto", otherTags, cfg.AutomaticGroupExclusions["OthersAuto"])
	if err != nil {
		return nil, nil, "", nil, err
	}
	urltest := func(tag string, members []string) map[string]any {
		return map[string]any{"type": "urltest", "tag": tag, "outbounds": members,
			"url": cfg.URLTest.URL, "interval": cfg.URLTest.Interval, "tolerance": cfg.URLTest.Tolerance}
	}
	var groups []map[string]any
	var mainMembers []string
	if len(realityTags) > 0 {
		groups = append(groups, urltest("RealityAuto", realityTags))
		mainMembers = append(mainMembers, "RealityAuto")
	}
	if len(otherTags) > 0 {
		groups = append(groups, urltest("OthersAuto", otherTags))
		mainMembers = append(mainMembers, "OthersAuto")
	}
	manual := make([]string, 0, len(dataNodes))
	for _, n := range dataNodes {
		manual = append(manual, n["tag"].(string))
	}
	if len(manual) == 0 {
		manual = []string{"direct"} // Placeholder until nodes are imported and config regenerated.
	}
	groups = append(groups, map[string]any{"type": "selector", "tag": "ManualPick", "outbounds": manual})
	mainMembers = append(mainMembers, "ManualPick")
	main := map[string]any{"type": "selector", "tag": "MainProxy", "outbounds": mainMembers,
		"default": mainMembers[0], "interrupt_exist_connections": true}
	// Chain the selected exit through MainProxy. The exit may be SOCKS,
	// VLESS, VMess, Trojan, or any other sing-box leaf outbound.
	for _, n := range nodes {
		delete(n, "_chain")
		if n["tag"] == chainExit {
			n["detour"] = "MainProxy"
		}
	}
	outbounds := append([]map[string]any{main}, groups...)
	outbounds = append(outbounds, nodes...)
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "direct"})
	exitTag := "MainProxy"
	if cfg.ChainEnabled && chainExit != "" {
		exitTag = chainExit
	}

	if err := validateOutboundGraph(outbounds); err != nil {
		return nil, nil, "", nil, err
	}
	return outbounds, nodes, exitTag, mainMembers, nil
}

func validateOutboundGraph(outbounds []map[string]any) error {
	graph := make(map[string][]string, len(outbounds))
	for _, node := range outbounds {
		tag, _ := node["tag"].(string)
		if _, exists := graph[tag]; tag == "" || exists {
			return fmt.Errorf("proxy chain requires unique non-empty outbound tags")
		}
		members, _ := node["outbounds"].([]string)
		deps := append([]string(nil), members...)
		if detour, _ := node["detour"].(string); detour != "" {
			deps = append(deps, detour)
		}
		graph[tag] = deps
	}
	visited, visiting := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(tag string) error {
		if visiting[tag] {
			return fmt.Errorf("proxy chain creates a routing cycle")
		}
		if visited[tag] {
			return nil
		}
		deps, exists := graph[tag]
		if !exists {
			return fmt.Errorf("proxy chain references a missing outbound")
		}
		visiting[tag] = true
		for _, dependency := range deps {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		delete(visiting, tag)
		visited[tag] = true
		return nil
	}
	for tag := range graph {
		if err := visit(tag); err != nil {
			return err
		}
	}
	return nil
}

func chainableOutbound(node map[string]any) bool {
	switch node["type"] {
	case nil, "", "selector", "urltest", "direct", "block", "dns":
		return false
	default:
		return true
	}
}
