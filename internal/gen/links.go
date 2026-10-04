package gen

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/pkg/mobileconf"
)

func parseShareLinks(path string) []map[string]any {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return parseShareLines(lines)
}

// parseShareLines delegates to the shared implementation in pkg/mobileconf,
// which also backs the iOS manual-node validation. Notices (nested sub://
// links) keep flowing to the host report as before.
func parseShareLines(lines []string) []map[string]any {
	nodes, notices := mobileconf.ParseShareLines(lines)
	for _, notice := range notices {
		reportf("%s", notice)
	}
	return nodes
}

// ---------------- Subscription fetching ----------------

// fetchSub detects sing-box JSON, Clash YAML, or base64 share-link feeds.
func fetchSub(src config.SubSource, hc *http.Client) ([]map[string]any, error) {
	resp, err := hc.Get(src.URL)
	if err != nil {
		return nil, fmt.Errorf("subscription %s fetch failed: %s", src.Name, errString(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("subscription %s: HTTP %d", src.Name, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxConfBytes+1))
	if err != nil {
		return nil, fmt.Errorf("subscription %s read failed: %w", src.Name, err)
	}
	if len(body) > maxConfBytes {
		return nil, fmt.Errorf("subscription %s exceeds the size limit", src.Name)
	}
	txt := strings.TrimSpace(string(body))
	format := src.Format
	if format == "" || format == "auto" {
		switch {
		case strings.HasPrefix(txt, "{"):
			format = "singbox"
		case strings.Contains(txt, "proxies:"):
			format = "clash"
		default:
			format = "base64"
		}
	}
	var nodes []map[string]any
	switch format {
	case "singbox":
		var cfg struct {
			Outbounds []map[string]any `json:"outbounds"`
		}
		if err := json.Unmarshal(body, &cfg); err != nil {
			return nil, fmt.Errorf("subscription %s JSON parse failed: %w", src.Name, err)
		}
		for _, o := range cfg.Outbounds {
			switch o["type"] { // Keep only leaf protocol nodes.
			case "selector", "urltest", "direct", "block", "dns", "", nil:
			default:
				nodes = append(nodes, o)
			}
		}
	case "clash":
		var c struct {
			Proxies []map[string]any `yaml:"proxies"`
		}
		if err := yaml.Unmarshal(body, &c); err != nil {
			return nil, fmt.Errorf("subscription %s YAML parse failed: %w", src.Name, err)
		}
		for _, p := range c.Proxies {
			if n := clashToOutbound(p); n != nil {
				nodes = append(nodes, n)
			}
		}
	default: // Base64 share links.
		if dec, err := base64.StdEncoding.DecodeString(txt); err == nil {
			txt = string(dec)
		}
		nodes = parseShareLines(strings.Split(txt, "\n"))
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("subscription %s contains no supported nodes (%s)", src.Name, format)
	}
	reportf("  subscription %s: %d nodes (%s)\n", src.Name, len(nodes), format)
	return nodes, nil
}

// clashToOutbound converts Clash/mihomo proxy entries into sing-box outbounds.
func clashToOutbound(p map[string]any) map[string]any {
	name, _ := p["name"].(string)
	server, _ := p["server"].(string)
	port := intFromAny(p["port"])
	switch strings.ToLower(str(p, "type")) {
	case "vless":
		n := map[string]any{"type": "vless", "tag": name, "server": server,
			"server_port": port, "uuid": str(p, "uuid")}
		if fl := str(p, "flow"); fl != "" {
			n["flow"] = fl
		}
		tls := map[string]any{"enabled": true, "server_name": orEmpty(str(p, "servername"), server)}
		if ro, ok := p["reality-opts"].(map[string]any); ok {
			tls["reality"] = map[string]any{"enabled": true,
				"public_key": str(ro, "public-key"), "short_id": str(ro, "short-id")}
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": orEmpty(str(p, "client-fingerprint"), "chrome")}
		}
		n["tls"] = tls
		return n
	case "vmess":
		return map[string]any{"type": "vmess", "tag": name, "server": server,
			"server_port": port, "uuid": str(p, "uuid"),
			"security": orEmpty(str(p, "cipher"), "auto"), "alter_id": intFromAny(p["alterId"])}
	case "trojan":
		return map[string]any{"type": "trojan", "tag": name, "server": server,
			"server_port": port, "password": str(p, "password"),
			"tls": map[string]any{"enabled": true, "server_name": orEmpty(str(p, "sni"), server)}}
	case "hysteria2":
		return map[string]any{"type": "hysteria2", "tag": name, "server": server,
			"server_port": port, "password": str(p, "password"),
			"tls": map[string]any{"enabled": true, "server_name": orEmpty(str(p, "sni"), server)}}
	case "ss":
		return map[string]any{"type": "shadowsocks", "tag": name, "server": server,
			"server_port": port, "method": str(p, "cipher"), "password": str(p, "password")}
	}
	return nil
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case string:
		return atoi(n, 0)
	}
	return 0
}

func str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func atoi(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func splitCSV(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}
