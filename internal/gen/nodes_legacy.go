package gen

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ---------------- 节点 ----------------

func splitPortRange(s string) (ports []string, single int) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "-") {
		ab := strings.SplitN(s, "-", 2)
		return []string{fmt.Sprintf("%s:%s", ab[0], ab[1])}, 0
	}
	parsed, err := strconv.Atoi(s)
	if err != nil {
		return nil, 0
	}
	return nil, parsed
}

// SRNode 是 Shadowrocket.json 的单条目（字段宽松解析）。
type SRNode map[string]any

func sstr(n SRNode, k string) string {
	if v, ok := n[k].(string); ok {
		return v
	}
	return ""
}

func srJSONNodes(path string, allowHosts map[string]bool) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var arr []SRNode
	if err := json.NewDecoder(f).Decode(&arr); err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, n := range arr {
		host := sstr(n, "host")
		if !allowHosts[host] || sstr(n, "type") == "Subscribe" {
			continue
		}
		switch strings.ToLower(sstr(n, "type")) {
		case "hysteria2":
			node := map[string]any{"type": "hysteria2", "tag": sstr(n, "title"),
				"server": host, "password": sstr(n, "password"),
				"tls": map[string]any{"enabled": true}}
			if ps, single := splitPortRange(sstr(n, "port")); len(ps) > 0 {
				node["server_ports"] = ps
			} else {
				node["server_port"] = single
			}
			if peer := sstr(n, "peer"); peer != "" {
				node["tls"].(map[string]any)["server_name"] = peer
			}
			out = append(out, node)
		case "socks5", "socks":
			node := map[string]any{"type": "socks", "tag": orEmpty(sstr(n, "title"), "Socks"),
				"server": host, "version": "5"}
			if _, single := splitPortRange(sstr(n, "port")); single > 0 {
				node["server_port"] = single
			}
			if u := sstr(n, "user"); u != "" {
				node["username"], node["password"] = u, sstr(n, "password")
			}
			out = append(out, node)
		case "vmess":
			node := map[string]any{"type": "vmess", "tag": orEmpty(sstr(n, "title"), host),
				"server": host, "uuid": sstr(n, "uuid"),
				"security": orEmpty(sstr(n, "method"), "auto")}
			if _, single := splitPortRange(sstr(n, "port")); single > 0 {
				node["server_port"] = single
			}
			out = append(out, node)
		}
	}
	return out, nil
}

func orEmpty(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func isReality(n map[string]any) bool {
	tls, _ := n["tls"].(map[string]any)
	re, _ := tls["reality"].(map[string]any)
	b, _ := re["enabled"].(bool)
	return b
}
