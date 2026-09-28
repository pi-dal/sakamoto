package gen

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/pi-dal/sakamoto/internal/config"
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

// parseShareLines 逐行解析分享链接；兼容标准 URI 与 Shadowrocket 导出格式
// （authority 段为 base64(userinfo@host:port)，tag 用 remarks= 参数，chain= → detour）。
func parseShareLines(lines []string) []map[string]any {
	var out []map[string]any
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil {
			continue
		}
		sch := strings.ToLower(u.Scheme)
		// SR 格式：vmess://vless://socks:// 的 host 段整个是 base64；
		// 重解析会丢 query，先把原始 query/fragment 存下来
		origQ, origFrag := u.RawQuery, u.Fragment
		if (sch == "vmess" || sch == "vless" || sch == "socks" || sch == "socks5") && u.User == nil && u.Host != "" {
			if dec, err := b64decode(u.Host); err == nil && strings.Contains(string(dec), "@") {
				if u2, err := url.Parse(sch + "://" + string(dec)); err == nil {
					u = u2 // 重解析出 user/host/port
				}
			}
		}
		q, _ := url.ParseQuery(origQ)
		tag, _ := url.PathUnescape(origFrag)
		if tag == "" {
			tag, _ = url.PathUnescape(q.Get("remarks")) // SR 用 remarks= 不用 #fragment
		}
		if tag == "" {
			tag = u.Hostname()
		}
		chain := strings.ToUpper(q.Get("chain")) // SR 链式参数
		if u.User == nil && sch != "vmess" && sch != "sub" {
			continue
		}
		var node map[string]any
		switch sch {
		case "hysteria2", "hy2":
			node = map[string]any{"type": "hysteria2", "tag": tag, "server": u.Hostname(),
				"password": u.User.Username(), "tls": map[string]any{"enabled": true}}
			if mp := q.Get("mport"); mp != "" {
				var ps []string
				for _, r := range strings.Split(mp, ",") {
					ps = append(ps, strings.Replace(r, "-", ":", 1))
				}
				node["server_ports"] = ps
			} else if p := u.Port(); p != "" {
				node["server_port"], _ = strconv.Atoi(p)
			}
			if sni := orEmpty(q.Get("peer"), q.Get("sni")); sni != "" {
				node["tls"].(map[string]any)["server_name"] = sni
			}
		case "vless":
			// 标准 VLESS 为 uuid@host；Shadowrocket 导出的是 method:uuid@host
			// （method 常为 none）。只要有 password 段，它才是真正的凭据。
			uuid := u.User.Username()
			if password, ok := u.User.Password(); ok && password != "" {
				uuid = password
			}
			node = map[string]any{"type": "vless", "tag": tag, "server": u.Hostname(),
				"server_port": atoi(u.Port(), 443), "uuid": uuid}
			if q.Get("xtls") == "2" {
				node["flow"] = "xtls-rprx-vision"
			} else if fl := q.Get("flow"); fl != "" {
				node["flow"] = fl
			}
			tls := map[string]any{"enabled": true,
				"server_name": orEmpty(q.Get("peer"), orEmpty(q.Get("sni"), u.Hostname()))}
			if q.Get("security") == "reality" || q.Get("pbk") != "" {
				tls["reality"] = map[string]any{"enabled": true,
					"public_key": cleanKey(q.Get("pbk")), "short_id": cleanKey(q.Get("sid"))}
				tls["utls"] = map[string]any{"enabled": true,
					"fingerprint": orEmpty(q.Get("fp"), orEmpty(q.Get("fingerprint"), "chrome"))}
			}
			node["tls"] = tls
			if q.Get("type") == "ws" {
				node["transport"] = map[string]any{"type": "ws", "path": orEmpty(q.Get("path"), "/"),
					"headers": map[string]any{"Host": q.Get("host")}}
			}
		case "trojan":
			pw, _ := u.User.Password()
			node = map[string]any{"type": "trojan", "tag": tag,
				"server": u.Hostname(), "server_port": atoi(u.Port(), 443), "password": pw,
				"tls": map[string]any{"enabled": true, "server_name": orEmpty(q.Get("sni"), u.Hostname())}}
		case "vmess":
			// 先试标准 JSON 格式，失败退 SR base64(user:uuid@host:port)
			var m map[string]any
			if dec, err := b64decode(strings.TrimPrefix(line, "vmess://")); err == nil && json.Unmarshal(dec, &m) == nil && str(m, "add") != "" {
				node = map[string]any{"type": "vmess", "tag": orEmpty(str(m, "ps"), str(m, "add")),
					"server": str(m, "add"), "server_port": atoi(str(m, "port"), 443),
					"uuid": str(m, "id"), "security": orEmpty(str(m, "scy"), "auto"),
					"alter_id": atoi(str(m, "aid"), 0)}
			} else if u.Host != "" {
				node = map[string]any{"type": "vmess", "tag": tag, "server": u.Hostname(),
					"server_port": atoi(u.Port(), 443), "uuid": u.User.Username(),
					"security": "auto", "alter_id": atoi(q.Get("alterId"), 0)}
				if pw, _ := u.User.Password(); pw != "" {
					node["uuid"] = pw // SR: userinfo=method:uuid
					node["security"] = orEmpty(u.User.Username(), "auto")
				}
			}
		case "tuic":
			pw, _ := u.User.Password()
			node = map[string]any{"type": "tuic", "tag": tag,
				"server": u.Hostname(), "server_port": atoi(u.Port(), 443),
				"uuid": u.User.Username(), "password": pw,
				"tls": map[string]any{"enabled": true,
					"server_name": orEmpty(q.Get("sni"), orEmpty(q.Get("peer"), u.Hostname()))}}
			if cc := q.Get("congestion_control"); cc != "" {
				node["congestion_control"] = cc
			}
			if rm := q.Get("udp_relay_mode"); rm != "" {
				node["udp_relay_mode"] = rm
			}
			if al := q.Get("alpn"); al != "" {
				node["tls"].(map[string]any)["alpn"] = []string{al}
			}
		case "anytls":
			node = map[string]any{"type": "anytls", "tag": tag,
				"server": u.Hostname(), "server_port": atoi(u.Port(), 443),
				"password": u.User.Username(),
				"tls": map[string]any{"enabled": true,
					"server_name": orEmpty(q.Get("peer"), orEmpty(q.Get("sni"), u.Hostname()))}}
		case "socks", "socks5":
			pw, _ := u.User.Password()
			node = map[string]any{"type": "socks", "tag": tag,
				"server": u.Hostname(), "server_port": atoi(u.Port(), 1080), "version": "5",
				"username": u.User.Username(), "password": pw}
		case "sub":
			// 嵌套订阅链接：解出来提示，订阅本体走 sakamoto.yaml subscriptions
			if dec, err := b64decode(u.Host); err == nil {
				reportf("  · sub:// 嵌套订阅: %s\n", string(dec))
			}
		}
		// SR obfs= 参数 = transport（websocket/grpc）
		if node != nil && node["type"] != "hysteria2" && node["type"] != "tuic" {
			switch q.Get("obfs") {
			case "websocket", "ws":
				node["transport"] = map[string]any{"type": "ws", "path": orEmpty(q.Get("path"), "/")}
			case "grpc":
				transport := map[string]any{"type": "grpc"}
				if service := q.Get("path"); service != "" {
					transport["service_name"] = strings.TrimPrefix(service, "/")
				}
				node["transport"] = transport
			}
		}
		if node == nil {
			continue
		}
		if chain != "" {
			node["_chain"] = chain // 后置解析成 detour
		}
		out = append(out, node)
	}
	return out
}

// b64decode 兼容 std / raw / urlsafe。
func b64decode(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("bad base64")
}

// cleanKey 处理 SR 双重 URL 编码及混入空白（pbk/sid）。
func cleanKey(s string) string {
	if v, err := url.PathUnescape(s); err == nil {
		s = v
	}
	if v, err := url.PathUnescape(s); err == nil {
		s = v
	}
	return strings.Join(strings.Fields(s), "")
}

// ---------------- 订阅拉取 ----------------

// fetchSub 拉取订阅并自动识别格式：sing-box JSON / Clash YAML / base64 分享链接。
func fetchSub(src config.SubSource, hc *http.Client) ([]map[string]any, error) {
	resp, err := hc.Get(src.URL)
	if err != nil {
		return nil, fmt.Errorf("订阅 %s 拉取失败: %s", src.Name, errString(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("订阅 %s: HTTP %d", src.Name, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxConfBytes+1))
	if err != nil {
		return nil, fmt.Errorf("订阅 %s 读取失败: %w", src.Name, err)
	}
	if len(body) > maxConfBytes {
		return nil, fmt.Errorf("订阅 %s 超过大小限制", src.Name)
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
			return nil, fmt.Errorf("订阅 %s JSON 无法解析: %w", src.Name, err)
		}
		for _, o := range cfg.Outbounds {
			switch o["type"] { // 只收叶子协议节点
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
			return nil, fmt.Errorf("订阅 %s YAML 无法解析: %w", src.Name, err)
		}
		for _, p := range c.Proxies {
			if n := clashToOutbound(p); n != nil {
				nodes = append(nodes, n)
			}
		}
	default: // base64 分享链接
		if dec, err := base64.StdEncoding.DecodeString(txt); err == nil {
			txt = string(dec)
		}
		nodes = parseShareLines(strings.Split(txt, "\n"))
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("订阅 %s 未解析到支持的节点（%s）", src.Name, format)
	}
	reportf("  订阅 %s: %d 节点 (%s)\n", src.Name, len(nodes), format)
	return nodes, nil
}

// clashToOutbound 把 Clash/mihomo proxy 条目转成 sing-box outbound。
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
