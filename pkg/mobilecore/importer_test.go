package mobilecore

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
)

// TestNormalizePolicyRuleParityWithSidecar asserts the layering contract: a
// rule the iOS Policy editor accepts (NormalizePolicyRule) must also pass
// internal/config.ValidatePolicy, which the host applies before generation.
// The device may be stricter (normalization is compile-level), never looser.
func TestNormalizePolicyRuleParityWithSidecar(t *testing.T) {
	corpus := []struct{ match, action string }{
		{"example.com", "proxy"},
		{"*.example.com", "direct"},
		{"keyword:ads", "reject"},
		{"cidr:10.0.0.0/8", "proxy"},
		{"10.0.0.0/8", "direct"},
		{"192.168.1.1", "direct"},
		{"https://Portal.Example.com/login", "proxy"},
		{"Example.COM", "Proxy"},
		{"", "proxy"},
		{"example.com", "allow"},
		{"example.com", ""},
		{"a b", "proxy"},
		{"a,b", "direct"},
		{"cidr:notacidr", "proxy"},
		{"cidr:", "proxy"},
		{"keyword:", "reject"},
		{"ftp://example.com", "proxy"},
		{"example.com/path", "proxy"},
	}
	for _, tc := range corpus {
		mobileErr := func() error {
			_, _, _, err := normalizePolicyRuleForTest(tc.match, tc.action)
			return err
		}()
		sidecar := &config.Config{}
		sidecar.PolicyRules = []config.PolicyRule{{Match: tc.match, Action: tc.action}}
		sidecarErr := sidecar.ValidatePolicy()

		if mobileErr == nil && sidecarErr != nil {
			t.Errorf("mobile accepted (%q, %q) but sidecar rejects: %v", tc.match, tc.action, sidecarErr)
		}
	}
}

func normalizePolicyRuleForTest(match, action string) (string, string, string, error) {
	info, err := NormalizePolicyRule(match, action)
	if err != nil {
		return "", "", "", err
	}
	return info.Kind, info.Value, info.Action, nil
}

func TestNormalizePolicyRuleInfo(t *testing.T) {
	info, err := NormalizePolicyRule(" https://Example.com/Path ", " DIRECT ")
	if err != nil {
		t.Fatalf("NormalizePolicyRule: %v", err)
	}
	if info.Match != "https://Example.com/Path" {
		t.Fatalf("Match = %q", info.Match)
	}
	if info.Kind != "domain" || info.Value != "example.com" || info.Action != "direct" {
		t.Fatalf("info = %+v", info)
	}
}

func TestParseShareLinkNoCredentialLeak(t *testing.T) {
	links := []string{
		"vless://SECRET-UUID@h.example:443?security=reality&pbk=PUBK&sid=S1&flow=xtls-rprx-vision#Node%201",
		"hy2://hunter2@h.example:443#Hy2",
		"socks5://alice:hunter2@s.example:1080#Socks",
		"tuic://uuid-abc:hunter2@t.example:443#Tuic",
		"trojan://:hunter2@tr.example:443#Trojan",
		"anytls://hunter2@a.example:443#AnyTLS",
		"vmess://eyJhZGQiOiJ2LmV4YW1wbGUiLCJwb3J0IjoiNDQzIiwiaWQiOiJ2bWVzcy1zZWNyZXQiLCJwcyI6Ik5vZGUiLCJzY3kiOiJhdXRvIn0=",
	}
	for _, link := range links {
		info, err := ParseShareLink(link)
		if err != nil {
			t.Fatalf("ParseShareLink(%q): %v", link, err)
		}
		rendered, marshalErr := json.Marshal(info)
		if marshalErr != nil {
			t.Fatalf("marshal: %v", marshalErr)
		}
		for _, secret := range []string{"SECRET-UUID", "hunter2", "PUBK", "uuid-abc", "vmess-secret", "alice"} {
			if strings.Contains(string(rendered), secret) {
				t.Fatalf("NodeLinkInfo for %q leaked %q: %s", link, secret, rendered)
			}
		}
		if !info.HasCredential {
			t.Fatalf("link %q should report HasCredential", link)
		}
	}
	// Parse errors stay generic and never echo the link.
	if _, err := ParseShareLink("vless://SECRET-UUID@h.example:443#x\nsecond line"); err == nil ||
		strings.Contains(err.Error(), "SECRET-UUID") {
		t.Fatalf("error must not echo the link: %v", err)
	}
}

func TestParseShareLinkSubSentinel(t *testing.T) {
	if !IsSubLink("sub://aHR0cHM6Ly9leGFtcGxlLmNvbS9zdWI=") {
		t.Fatal("IsSubLink should detect sub:// links")
	}
	if IsSubLink("vless://u@h:443#x") {
		t.Fatal("IsSubLink must not flag node links")
	}
	// ParseShareLink still rejects sub:// links with the shared error.
	if _, err := ParseShareLink("sub://aHR0cHM6Ly9leGFtcGxlLmNvbS9zdWI="); err == nil ||
		!strings.Contains(err.Error(), "subscriptions") {
		t.Fatalf("want subscription error, got %v", err)
	}
}

func TestParseConfContentReport(t *testing.T) {
	conf := "[General]\ndns-server = 1.1.1.1, 8.8.8.8\ninclude = rules.conf\ninclude = https://remote.example.com/more.conf?token=TOKEN\n\n" +
		"[Rule]\nDOMAIN-SUFFIX,a.example,DIRECT\nDOMAIN,b.example,REJECT\nIP-CIDR,10.0.0.0/8,DIRECT\n" +
		"RULE-SET,https://lists.example.com/ads.txt?token=SECRET,REJECT\nFINAL,DIRECT\n" +
		"\n[URL Rewrite]\n^https?://x - reject\n[MITM]\nhostname=x\n"
	report, err := ParseConfContent(conf)
	if err != nil {
		t.Fatalf("ParseConfContent: %v", err)
	}
	if report.TotalRules != 3 || report.DirectRules != 2 || report.RejectRules != 1 || report.ProxyRules != 0 {
		t.Fatalf("counts: %+v", report)
	}
	if report.FinalTarget != "DIRECT" {
		t.Fatalf("FinalTarget = %q", report.FinalTarget)
	}
	if len(report.DNSResolvers) != 2 || report.DNSResolvers[0] != "1.1.1.1" {
		t.Fatalf("DNSResolvers = %v", report.DNSResolvers)
	}
	if len(report.IncludesPending) != 2 || len(report.RuleSetsPending) != 1 {
		t.Fatalf("pending: %v / %v", report.IncludesPending, report.RuleSetsPending)
	}
	rendered, _ := json.Marshal(report)
	for _, secret := range []string{"TOKEN", "SECRET"} {
		if strings.Contains(string(rendered), secret) {
			t.Fatalf("ConfReport leaked %q: %s", secret, rendered)
		}
	}
	if len(report.Unsupported) == 0 {
		t.Fatal("expected unsupported-section notes")
	}
}

func TestParseConfContentRejectsNonConf(t *testing.T) {
	if _, err := ParseConfContent("not a conf"); err == nil {
		t.Fatal("expected error for non-conf content")
	}
	// Error text must not echo arbitrary content.
	_, err := ParseConfContent("totally unrelated SECRETCONTENT")
	if err == nil || strings.Contains(err.Error(), "SECRETCONTENT") {
		t.Fatalf("error should be generic: %v", err)
	}
}

func TestValidateConfigJSON(t *testing.T) {
	valid := `{
	  "log": {"level": "info"},
	  "dns": {"servers": [{"type": "udp", "tag": "dns"}], "final": "dns"},
	  "inbounds": [{"type": "tun", "tag": "tun-in"}],
	  "outbounds": [{"type": "direct", "tag": "direct"}, {"type": "selector", "tag": "MainProxy"}],
	  "endpoints": [{"type": "tailscale", "tag": "ts"}],
	  "route": {"rules": [], "final": "direct"}
	}`
	if err := ValidateConfigJSON(valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := map[string]string{
		"not json":          `{`,
		"array root":        `[]`,
		"missing dns":       `{"log":{},"inbounds":[],"outbounds":[],"route":{"final":"direct"}}`,
		"missing route":     `{"log":{},"dns":{},"inbounds":[],"outbounds":[]}`,
		"empty final":       `{"log":{},"dns":{},"inbounds":[],"outbounds":[],"route":{"rules":[],"final":" "}}`,
		"outbound no tag":   `{"log":{},"dns":{},"inbounds":[],"outbounds":[{"type":"direct"}],"route":{"final":"direct"}}`,
		"outbound no type":  `{"log":{},"dns":{},"inbounds":[],"outbounds":[{"tag":"direct"}],"route":{"final":"direct"}}`,
		"endpoint no tag":   `{"log":{},"dns":{},"inbounds":[],"outbounds":[{"type":"direct","tag":"d"}],"endpoints":[{"type":"tailscale"}],"route":{"final":"direct"}}`,
		"outbound not list": `{"log":{},"dns":{},"inbounds":[],"outbounds":{"a":1},"route":{"final":"direct"}}`,
	}
	for name, body := range cases {
		if err := ValidateConfigJSON(body); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}
