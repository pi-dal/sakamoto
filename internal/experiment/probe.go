package experiment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const directProbeTag = "learning-direct"
const proxyProbeTag = "learning-proxy"
const routeProbeTag = "learning-route"
const probeUser = "sakamoto-learning"

// Candidate contains observed fallback traffic, never a URL or app credentials.
type Candidate struct {
	Domain   string `json:"domain"`
	Port     string `json:"port"`
	Failures int    `json:"failures"`
	Required int    `json:"required"`
	Reason   string `json:"reason"`
}

type ProbeResult struct {
	HTTPStatus int    `json:"http_status,omitempty"`
	Reachable  bool   `json:"reachable"`
	Error      string `json:"error,omitempty"`
}

// PrepareProbe adds private authenticated ingress for comparing the exact
// configured exits without changing routes for ordinary application traffic.
// Existing valid listeners are reused across generation and restarts.
func PrepareProbe(raw []byte) ([]byte, error) {
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	route, ok := data["route"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing route")
	}
	outbound, err := ProxyOutbound(raw)
	if err != nil {
		return nil, err
	}
	inbounds, _ := data["inbounds"].([]any)
	rules, _ := route["rules"].([]any)
	for _, tag := range []string{directProbeTag, proxyProbeTag, routeProbeTag} {
		if _, err := probeURL(raw, tag); err == nil {
			continue
		}
		// Never silently take over an unrelated listener using a reserved tag.
		for _, v := range inbounds {
			if m, ok := v.(map[string]any); ok && m["tag"] == tag {
				return nil, fmt.Errorf("invalid reserved listener %s", tag)
			}
		}
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			return nil, e
		}
		port := listener.Addr().(*net.TCPAddr).Port
		if e = listener.Close(); e != nil {
			return nil, e
		}
		secret := make([]byte, 32)
		if _, e = rand.Read(secret); e != nil {
			return nil, e
		}
		inbounds = append(inbounds, map[string]any{"type": "mixed", "tag": tag, "listen": "127.0.0.1", "listen_port": port, "users": []any{map[string]any{"username": probeUser, "password": hex.EncodeToString(secret)}}})
		exit := "direct"
		if tag == proxyProbeTag {
			exit = outbound
		}
		if tag != routeProbeTag {
			at := 0
			if len(rules) > 0 {
				first, _ := rules[0].(map[string]any)
				if first["action"] == "hijack-dns" {
					at = 1
				}
			}
			rules = append(rules, nil)
			copy(rules[at+1:], rules[at:])
			rules[at] = map[string]any{"inbound": []string{tag}, "action": "route", "outbound": exit}
		}
	}
	data["inbounds"] = inbounds
	route["rules"] = rules
	return json.MarshalIndent(data, "", "  ")
}

// ProbeReady requires both private listeners and their matching routes.
func ProbeReady(raw []byte) bool {
	for _, tag := range []string{directProbeTag, proxyProbeTag, routeProbeTag} {
		if _, err := probeURL(raw, tag); err != nil {
			return false
		}
	}
	return true
}

func probeURL(raw []byte, tag string) (*url.URL, error) {
	var data struct {
		Inbounds []struct {
			Type, Tag, Listen string
			Port              int `json:"listen_port"`
			Users             []struct{ Username, Password string }
		} `json:"inbounds"`
		Route struct {
			Rules []struct {
				Inbound          []string
				Action, Outbound string
			}
		} `json:"route"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	for _, in := range data.Inbounds {
		if in.Tag != tag {
			continue
		}
		if in.Type != "mixed" || in.Listen != "127.0.0.1" || in.Port < 1 || in.Port > 65535 || len(in.Users) != 1 || in.Users[0].Username != probeUser || len(in.Users[0].Password) < 32 {
			break
		}
		endpoint := &url.URL{Scheme: "socks5h", Host: net.JoinHostPort(in.Listen, strconv.Itoa(in.Port)), User: url.UserPassword(probeUser, in.Users[0].Password)}
		if tag == routeProbeTag {
			return endpoint, nil
		}
		expected := "direct"
		if tag == proxyProbeTag {
			var err error
			expected, err = ProxyOutbound(raw)
			if err != nil {
				return nil, err
			}
		}
		for _, r := range data.Route.Rules {
			if len(r.Inbound) == 1 && r.Inbound[0] == tag && r.Action == "route" && r.Outbound == expected {
				return &url.URL{Scheme: "socks5h", Host: net.JoinHostPort(in.Listen, strconv.Itoa(in.Port)), User: url.UserPassword(probeUser, in.Users[0].Password)}, nil
			}
		}
	}
	return nil, fmt.Errorf("private learning listener %s is unavailable; regenerate the core configuration", tag)
}

// Probe checks only an origin HEAD response. It deliberately rejects denials,
// rate limits and server errors, and never follows redirects to another host.
func Probe(ctx context.Context, raw []byte, c Candidate, viaProxy bool) ProbeResult {
	tag := directProbeTag
	if viaProxy {
		tag = proxyProbeTag
	}
	return probeOrigin(ctx, raw, c, tag)
}

// ProbeRoute verifies the installed ordinary route, including rule precedence.
func ProbeRoute(ctx context.Context, raw []byte, c Candidate) ProbeResult {
	return probeOrigin(ctx, raw, c, routeProbeTag)
}

func probeOrigin(ctx context.Context, raw []byte, c Candidate, tag string) ProbeResult {
	if Domain(c.Domain) != c.Domain || (c.Port != "80" && c.Port != "443") {
		return ProbeResult{Error: "only observed HTTP(S) destinations on ports 80/443 can be verified"}
	}
	endpoint, err := probeURL(raw, tag)
	if err != nil {
		return ProbeResult{Error: err.Error()}
	}
	scheme := "http"
	if c.Port == "443" {
		scheme = "https"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, scheme+"://"+net.JoinHostPort(c.Domain, c.Port)+"/", nil)
	if err != nil {
		return ProbeResult{Error: "invalid observed destination"}
	}
	transport := &http.Transport{Proxy: http.ProxyURL(endpoint), DialContext: (&net.Dialer{Timeout: 6 * time.Second}).DialContext, DisableKeepAlives: true, MaxResponseHeaderBytes: 32 << 10, TLSHandshakeTimeout: 6 * time.Second, ResponseHeaderTimeout: 6 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		// Do not persist transport errors containing private probe credentials.
		if ctx.Err() != nil {
			return ProbeResult{Error: "verification canceled"}
		}
		return ProbeResult{Error: "origin request failed (connection, TLS or response timeout)"}
	}
	_ = response.Body.Close()
	code := response.StatusCode
	ok := (code >= 200 && code < 400) || code == 401 || code == 404 || code == 405
	result := ProbeResult{HTTPStatus: code, Reachable: ok}
	if !ok {
		result.Error = fmt.Sprintf("origin returned HTTP %d", code)
	}
	return result
}
