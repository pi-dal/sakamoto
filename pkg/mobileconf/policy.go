package mobileconf

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Policy actions, matching internal/config ValidatePolicy and the TUI
// Config → Policy wording (docs/tui.md).
const (
	ActionProxy  = "proxy"
	ActionDirect = "direct"
	ActionReject = "reject"
)

// PolicyActions returns the accepted actions in display order.
func PolicyActions() []string { return []string{ActionProxy, ActionDirect, ActionReject} }

// NormalizePolicyMatch validates and folds one user-owned policy match into
// the (rule kind, sing-box value) pair used at generation time. Accepted
// forms (docs/tui.md Config → Policy):
//
//   - plain hostname                → domain (exact)
//   - *.example.com                 → domain_suffix
//   - keyword:foo                   → domain_keyword
//   - cidr:10.0.0.0/8, bare CIDR/IP → ip_cidr (a bare IP becomes /32 or /128)
//   - http(s)://… URL               → the host is used; paths are not routable
//
// This is the single implementation; internal/gen compiles with it and
// pkg/mobilecore exposes it to iOS, so both sides validate identically.
func NormalizePolicyMatch(raw string) (string, string, error) {
	s := strings.TrimSpace(raw)
	lower := strings.ToLower(s)
	for _, prefix := range []string{"keyword:", "domain:", "suffix:", "cidr:"} {
		if strings.HasPrefix(lower, prefix) {
			value := strings.TrimSpace(s[len(prefix):])
			if value == "" {
				return "", "", fmt.Errorf("empty %s match", strings.TrimSuffix(prefix, ":"))
			}
			kind := map[string]string{"keyword:": "domain_keyword", "domain:": "domain", "suffix:": "domain_suffix", "cidr:": "ip_cidr"}[prefix]
			if kind == "ip_cidr" {
				if _, _, err := net.ParseCIDR(value); err != nil {
					return "", "", fmt.Errorf("invalid CIDR %q", value)
				}
			}
			return strings.ToLower(value), kind, nil
		}
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return "", "", fmt.Errorf("invalid HTTP(S) URL %q", s)
		}
		s = u.Hostname()
	}
	if strings.HasPrefix(s, "*.") {
		if len(s) <= 2 {
			return "", "", fmt.Errorf("empty domain suffix")
		}
		return strings.ToLower(s[2:]), "domain_suffix", nil
	}
	if ip := net.ParseIP(s); ip != nil {
		if ip.To4() != nil {
			return ip.String() + "/32", "ip_cidr", nil
		}
		return ip.String() + "/128", "ip_cidr", nil
	}
	if strings.Contains(s, "/") {
		if _, _, err := net.ParseCIDR(s); err != nil {
			return "", "", fmt.Errorf("invalid CIDR %q", s)
		}
		return strings.ToLower(s), "ip_cidr", nil
	}
	if strings.ContainsAny(s, " \t,\x00\r\n") {
		return "", "", fmt.Errorf("match must be one hostname, URL, keyword, or CIDR")
	}
	return strings.ToLower(s), "domain", nil
}

// ValidatePolicyAction accepts only proxy, direct, or reject
// (case-insensitive, surrounding space ignored) — the same set the host
// sidecar and TUI accept.
func ValidatePolicyAction(action string) error {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case ActionProxy, ActionDirect, ActionReject:
		return nil
	default:
		return fmt.Errorf("action must be proxy, direct, or reject")
	}
}

// NormalizePolicyRule validates match and action together and returns the
// normalized triple for display. It applies BOTH host validation layers —
// the sidecar-level checks from internal/config.ValidatePolicy (non-empty,
// no control characters, single value) and the compile-level normalization
// above — so a rule accepted on device is accepted by the host importer.
func NormalizePolicyRule(match, action string) (kind, value, normalizedAction string, err error) {
	if err := ValidatePolicyAction(action); err != nil {
		return "", "", "", err
	}
	match = strings.TrimSpace(match)
	if match == "" || strings.ContainsAny(match, "\x00\r\n") {
		return "", "", "", fmt.Errorf("match must be one hostname, URL, keyword, or CIDR")
	}
	value, kind, err = NormalizePolicyMatch(match)
	if err != nil {
		return "", "", "", err
	}
	return kind, value, strings.ToLower(strings.TrimSpace(action)), nil
}
