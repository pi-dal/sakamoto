package mobileconf

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ParseShareLines parses share-link lines into sing-box outbound maps. It is
// the single implementation used by the host generator (internal/gen) for
// nodes.txt, subscription bodies and single-link validation.
//
// Accepts standard share URIs and Shadowrocket exports with
// base64(userinfo@host:port) authorities, remarks= tags, and chain= detours.
//
// The second return value carries informational notices (today: nested
// sub:// subscription links). Callers decide whether to print them; the
// host generator forwards them to its report, mobile callers drop them — a
// notice is never a node.
func ParseShareLines(lines []string) (nodes []map[string]any, notices []string) {
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
		// Shadowrocket encodes the whole host portion of vmess/vless/socks URLs
		// as base64; save query and fragment before reparsing the authority.
		origQ, origFrag := u.RawQuery, u.Fragment
		if (sch == "vmess" || sch == "vless" || sch == "socks" || sch == "socks5") && u.User == nil && u.Host != "" {
			if dec, err := b64decode(u.Host); err == nil && strings.Contains(string(dec), "@") {
				if u2, err := url.Parse(sch + "://" + string(dec)); err == nil {
					u = u2 // Parse user, host, and port from the decoded authority.
				}
			}
		}
		q, _ := url.ParseQuery(origQ)
		tag, _ := url.PathUnescape(origFrag)
		if tag == "" {
			tag, _ = url.PathUnescape(q.Get("remarks")) // Shadowrocket uses remarks= instead of the fragment.
		}
		if tag == "" {
			tag = u.Hostname()
		}
		chain := strings.ToUpper(q.Get("chain")) // Shadowrocket chain parameter.
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
			// Standard VLESS uses uuid@host; Shadowrocket exports method:uuid@host.
			// Treat the password field as the actual credential when present.
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
			// Try standard JSON first, then the Shadowrocket base64 authority.
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
			// Report nested subscription links; configure them in sakamoto.yaml.
			if dec, err := b64decode(u.Host); err == nil {
				notices = append(notices, fmt.Sprintf("  · nested sub:// subscription: %s\n", string(dec)))
			}
		}
		// Shadowrocket obfs= selects websocket or gRPC transport.
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
			node["_chain"] = chain // Resolve the detour after parsing all nodes.
		}
		out = append(out, node)
	}
	return out, notices
}

// NodeLinkInfo is the credential-free view of one share link, safe to expose
// through gomobile and to render on screen. It never carries the raw link,
// passwords, UUIDs or public keys — internal/gen's NodeEntry keeps those
// host-side only.
type NodeLinkInfo struct {
	Tag    string
	Type   string
	Server string
	// Port is server_port when the link has a single port; 0 when only a
	// port range (ServerPorts) is present.
	Port        int32
	ServerPorts []string
	// HasCredential is true when the link carries a password, UUID, or
	// username. It is a fact about presence, never the value itself.
	HasCredential bool
}

// ErrSubLink marks a sub:// subscription link: these configure subscription
// sources, not manual nodes.
var ErrSubLink = fmt.Errorf("sub:// links configure subscriptions, not manual nodes")

// IsSubLink reports whether the raw string is a sub:// subscription link —
// these configure subscription sources, not manual nodes.
func IsSubLink(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && strings.ToLower(u.Scheme) == "sub"
}

// ParseShareLinkInfo validates exactly one share link and returns its
// credential-free summary. Errors mirror internal/gen.ParseNodeLink so both
// sides reject the same input; a sub:// link fails with ErrSubLink.
func ParseShareLinkInfo(raw string) (*NodeLinkInfo, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n") {
		return nil, fmt.Errorf("a node share link must fit on one line")
	}
	if IsSubLink(raw) {
		return nil, ErrSubLink
	}
	nodes, _ := ParseShareLines([]string{raw})
	if len(nodes) != 1 {
		return nil, fmt.Errorf("unsupported or invalid node share link")
	}
	n := nodes[0]
	tag, _ := n["tag"].(string)
	kind, _ := n["type"].(string)
	server, _ := n["server"].(string)
	if tag == "" || kind == "" || server == "" {
		return nil, fmt.Errorf("node is missing a name or server")
	}
	info := &NodeLinkInfo{Tag: tag, Type: kind, Server: server}
	_, hasPort := n["server_port"]
	_, hasPorts := n["server_ports"]
	if !hasPort && !hasPorts {
		return nil, fmt.Errorf("node is missing a port")
	}
	if hasPort {
		info.Port = int32(atoi(fmt.Sprint(n["server_port"]), 0))
	}
	if ps, ok := n["server_ports"].([]string); ok {
		info.ServerPorts = ps
	}
	for _, credential := range []string{"password", "uuid", "username"} {
		if v, _ := n[credential].(string); v != "" {
			info.HasCredential = true
			break
		}
	}
	return info, nil
}

// b64decode accepts standard, unpadded, and URL-safe base64.
func b64decode(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("bad base64")
}

// cleanKey handles doubly URL-encoded keys and stray whitespace.
func cleanKey(s string) string {
	if v, err := url.PathUnescape(s); err == nil {
		s = v
	}
	if v, err := url.PathUnescape(s); err == nil {
		s = v
	}
	return strings.Join(strings.Fields(s), "")
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

func orEmpty(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
