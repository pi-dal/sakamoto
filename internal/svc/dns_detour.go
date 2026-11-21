package svc

// Check every selectable member. A user can switch nodes after startup.
type dnsOutbound struct {
	Type, Tag, Server, Detour string
	Outbounds                 []string
}

func protectedDetour(nodes []dnsOutbound, entry string) bool {
	graph := make(map[string]dnsOutbound, len(nodes))
	for _, node := range nodes {
		if _, duplicate := graph[node.Tag]; duplicate {
			return false
		}
		graph[node.Tag] = node
	}
	// An in-progress entry reads false, rejecting cycles. Finished entries are memoized.
	verified := map[string]bool{}
	var check func(string) bool
	check = func(tag string) bool {
		if safe, visited := verified[tag]; visited {
			return safe
		}
		verified[tag] = false
		n, ok := graph[tag]
		if !ok {
			return false
		}
		switch n.Type {
		case "selector", "urltest":
			if len(n.Outbounds) == 0 {
				return false
			}
			for _, member := range n.Outbounds {
				if !check(member) {
					return false
				}
			}
		case "trojan", "shadowsocks", "socks", "http", "vmess", "vless", "hysteria", "hysteria2", "tuic", "anytls", "ssh", "shadowtls", "naive":
			if n.Server == "" {
				return false
			}
		default:
			return false
		}
		if n.Detour != "" && !check(n.Detour) {
			return false
		}
		verified[tag] = true
		return true
	}
	return check(entry)
}
