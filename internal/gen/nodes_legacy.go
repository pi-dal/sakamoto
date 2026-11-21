package gen

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/pi-dal/sakamoto/pkg/mobileconf"
)

// ---------------- Legacy node export ----------------

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

// SRNode is one loosely decoded entry from Shadowrocket.json.
type SRNode map[string]any

func sstr(n SRNode, k string) string {
	if v, ok := n[k].(string); ok {
		return v
	}
	return ""
}

func srJSONNodes(path string, allowHosts map[string]bool, capture *[]SRNode) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var arr []SRNode
	if err := json.NewDecoder(f).Decode(&arr); err != nil {
		return nil, err
	}
	return parseSRNodes(arr, allowHosts, capture)
}

func parseSRNodes(arr []SRNode, allowHosts map[string]bool, capture *[]SRNode) ([]map[string]any, error) {
	var out []map[string]any
	for _, n := range arr {
		used := SRNode{}
		field := func(key string) string {
			value := sstr(n, key)
			if capture != nil && value != "" {
				used[key] = value
			}
			return value
		}
		host := field("host")
		if (allowHosts != nil && !allowHosts[host]) || field("type") == "Subscribe" {
			continue
		}
		switch strings.ToLower(field("type")) {
		case "hysteria2":
			node := map[string]any{"type": "hysteria2", "tag": field("title"),
				"server": host, "password": field("password"),
				"tls": map[string]any{"enabled": true}}
			if ps, single := splitPortRange(field("port")); len(ps) > 0 {
				node["server_ports"] = ps
			} else {
				node["server_port"] = single
			}
			if peer := field("peer"); peer != "" {
				node["tls"].(map[string]any)["server_name"] = peer
			}
			out = append(out, node)
		case "socks5", "socks":
			node := map[string]any{"type": "socks", "tag": orEmpty(field("title"), "Socks"),
				"server": host, "version": "5"}
			if _, single := splitPortRange(field("port")); single > 0 {
				node["server_port"] = single
			}
			if u := field("user"); u != "" {
				node["username"], node["password"] = u, field("password")
			}
			out = append(out, node)
		case "vmess":
			node := map[string]any{"type": "vmess", "tag": orEmpty(field("title"), host),
				"server": host, "uuid": field("uuid"),
				"security": orEmpty(field("method"), "auto")}
			mode := ""
			if value, present := n["tls"]; present {
				mode = fmt.Sprint(value)
				used["tls"] = mode
			}
			peer := orEmpty(field("sni"), field("peer"))
			if peer != "" && mode == "" {
				return nil, fmt.Errorf("legacy VMess TLS hostname requires an explicit TLS setting")
			}
			if err := mobileconf.ConfigureVMess(node, mode, peer, orEmpty(field("network"), field("obfs")), field("obfsParam"), field("path"), field("headerType")); err != nil {
				return nil, err
			}
			if _, single := splitPortRange(field("port")); single > 0 {
				node["server_port"] = single
			}
			out = append(out, node)
		default:
			continue
		}
		if capture != nil {
			*capture = append(*capture, used)
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
