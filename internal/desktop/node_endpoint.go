package desktop

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/pi-dal/sakamoto/pkg/mobileconf"
)

// Endpoint exposes only editable, credential-free node fields. Empty Port means hopping ports.
type Endpoint struct {
	Remark, Server, Port string
}

func decodeNodeAuthority(raw string) ([]byte, error) {
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if data, err := encoding.DecodeString(raw); err == nil {
			return data, nil
		}
	}
	return nil, errors.New("invalid encoded node")
}

func vmessFields(raw string) map[string]json.RawMessage {
	if !strings.HasPrefix(raw, "vmess://") {
		return nil
	}
	data, err := decodeNodeAuthority(strings.TrimPrefix(raw, "vmess://"))
	if err != nil {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return nil
	}
	var server string
	if json.Unmarshal(fields["add"], &server) != nil || server == "" {
		return nil
	}
	return fields
}

func nodeURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("invalid node link")
	}
	scheme := strings.ToLower(u.Scheme)
	if u.User == nil && (scheme == "vmess" || scheme == "vless" || scheme == "socks" || scheme == "socks5") {
		if data, err := decodeNodeAuthority(u.Host); err == nil && strings.Contains(string(data), "@") {
			if expanded, err := url.Parse(scheme + "://" + string(data)); err == nil {
				expanded.RawQuery, expanded.Fragment, expanded.RawFragment = u.RawQuery, u.Fragment, u.RawFragment
				u = expanded
			}
		}
	}
	if u.Hostname() == "" || u.User == nil {
		return nil, errors.New("node is missing its server or credentials")
	}
	return u, nil
}

func NodeEndpoint(raw string) (Endpoint, error) {
	raw = strings.TrimSpace(raw)
	if vmessFields(raw) == nil {
		if _, err := nodeURL(raw); err != nil {
			return Endpoint{}, err
		}
	}
	info, err := mobileconf.ParseShareLinkInfo(raw)
	if err != nil {
		return Endpoint{}, errors.New("could not read node endpoint")
	}
	endpoint := Endpoint{Remark: info.Tag, Server: info.Server}
	if len(info.ServerPorts) == 0 {
		endpoint.Port = strconv.Itoa(int(info.Port))
	}
	return endpoint, nil
}

// UpdateNodeEndpoint edits source fields, never reconstructing a link from a
// generated outbound (which would discard credentials or unknown options).
func UpdateNodeEndpoint(raw, remark, server, port string) (string, error) {
	raw = strings.TrimSpace(raw)
	current, err := NodeEndpoint(raw)
	if err != nil {
		return "", err
	}
	remark, server, port = strings.TrimSpace(remark), strings.TrimSpace(server), strings.TrimSpace(port)
	if strings.HasPrefix(server, "[") && strings.HasSuffix(server, "]") {
		server = server[1 : len(server)-1]
	}
	if remark == "" || strings.ContainsFunc(remark, unicode.IsControl) || server == "" || strings.ContainsFunc(server, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) || strings.ContainsAny(server, "/?#@%\\[]") || (strings.Contains(server, ":") && net.ParseIP(server) == nil) {
		return "", errors.New("enter a remark and an IP address or hostname")
	}
	if net.ParseIP(server) == nil && strings.ContainsFunc(server, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '-' && r != '_'
	}) {
		return "", errors.New("enter an IP address or hostname")
	}
	if current.Port == "" {
		if port != "" {
			return "", errors.New("edit the share link to change hopping ports")
		}
	} else if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", errors.New("Port must be 1–65535")
	}
	if remark == current.Remark && server == current.Server && port == current.Port {
		return raw, nil
	}
	var updated string
	if fields := vmessFields(raw); fields != nil {
		fields["ps"], _ = json.Marshal(remark)
		fields["add"], _ = json.Marshal(server)
		if port != current.Port {
			fields["port"], _ = json.Marshal(port)
		}
		data, err := json.Marshal(fields)
		if err != nil {
			return "", errors.New("could not update node endpoint")
		}
		updated = "vmess://" + base64.StdEncoding.EncodeToString(data)
	} else {
		u, err := nodeURL(raw)
		if err != nil {
			return "", err
		}
		explicitPort := u.Port()
		if port != current.Port {
			explicitPort = port
		}
		u.Host = server
		if explicitPort != "" {
			u.Host = net.JoinHostPort(server, explicitPort)
		} else if strings.Contains(server, ":") {
			u.Host = "[" + server + "]"
		}
		if remark != current.Remark {
			// The shared importer unescapes the decoded fragment once more.
			u.Fragment, u.RawFragment = strings.ReplaceAll(remark, "%", "%25"), ""
		}
		updated = u.String()
	}
	if _, err := NodeEndpoint(updated); err != nil {
		return "", err
	}
	return updated, nil
}
