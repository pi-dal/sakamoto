package mobileconf

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestValidSource(t *testing.T) {
	cases := []struct {
		source string
		ok     bool
	}{
		{"https://example.com/sr.conf", true},
		{"http://example.com/sr.conf", true},
		{"/Users/x/Downloads/sr.conf", true},
		{"relative/sr.conf", true},
		{"", false},
		{"   ", false},
		{"ftp://example.com/sr.conf", false},
		// "javascript:alert(1)" has no :// and is therefore treated as a
		// path-shaped string, exactly like the host importer; it fails later
		// when opened or fetched. This mirrors internal/gen semantics.
		{"https:///nohost", false},
	}
	for _, tc := range cases {
		err := ValidSource(tc.source)
		if tc.ok != (err == nil) {
			t.Errorf("ValidSource(%q) = %v, want ok=%v", tc.source, err, tc.ok)
		}
	}
}

func TestDisplaySourceMasksRemote(t *testing.T) {
	masked := DisplaySource("https://user:pass@example.com/secret.conf?token=abc#frag")
	for _, leaked := range []string{"user:pass", "token=abc", "secret.conf"} {
		if strings.Contains(masked, leaked) {
			t.Fatalf("DisplaySource leaked %q: %s", leaked, masked)
		}
	}
	if !strings.HasPrefix(masked, "https://example.com/") {
		t.Fatalf("DisplaySource = %q", masked)
	}
	if got := DisplaySource("/local/path.conf"); got != "/local/path.conf" {
		t.Fatalf("local path should pass through, got %q", got)
	}
}

func TestNormalizePolicyMatch(t *testing.T) {
	cases := []struct {
		raw  string
		kind string
		want string
		ok   bool
	}{
		{"Example.com", "domain", "example.com", true},
		{"  *.Example.COM ", "domain_suffix", "example.com", true},
		{"keyword:Ads", "domain_keyword", "ads", true},
		{"cidr:10.0.0.0/8", "ip_cidr", "10.0.0.0/8", true},
		{"cidr:nope", "", "", false},
		{"cidr:", "", "", false},
		{"1.2.3.4", "ip_cidr", "1.2.3.4/32", true},
		{"2001:db8::1", "ip_cidr", "2001:db8::1/128", true},
		{"10.0.0.0/8", "ip_cidr", "10.0.0.0/8", true},
		{"https://Example.com/Path?x=1", "domain", "example.com", true},
		{"ftp://example.com", "", "", false},
		{"a b", "", "", false},
		{"a,b", "", "", false},
		// NOTE: an empty match normalizes to an empty domain here, mirroring the
		// compile level; the empty value is rejected one layer earlier on both
		// sides (config.ValidatePolicy / NormalizePolicyRule below).
	}
	for _, tc := range cases {
		value, kind, err := NormalizePolicyMatch(tc.raw)
		if tc.ok != (err == nil) {
			t.Errorf("NormalizePolicyMatch(%q) err = %v, want ok=%v", tc.raw, err, tc.ok)
			continue
		}
		if tc.ok && (kind != tc.kind || value != tc.want) {
			t.Errorf("NormalizePolicyMatch(%q) = (%s, %s), want (%s, %s)", tc.raw, value, kind, tc.want, tc.kind)
		}
	}
}

func TestValidatePolicyAction(t *testing.T) {
	for _, ok := range []string{"proxy", "DIRECT", " Reject ", "reject"} {
		if err := ValidatePolicyAction(ok); err != nil {
			t.Errorf("ValidatePolicyAction(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "allow", "block", "proxy,direct"} {
		if err := ValidatePolicyAction(bad); err == nil {
			t.Errorf("ValidatePolicyAction(%q) accepted", bad)
		}
	}
}

func TestNormalizePolicyRule(t *testing.T) {
	kind, value, action, err := NormalizePolicyRule("*.Example.com", "Proxy")
	if err != nil || kind != "domain_suffix" || value != "example.com" || action != "proxy" {
		t.Fatalf("NormalizePolicyRule = (%s, %s, %s), %v", kind, value, action, err)
	}
	for _, bad := range []struct{ match, action string }{
		{"", "proxy"},                  // empty match rejected (sidecar layer)
		{"example.com", ""},            // empty action
		{"example.com", "allow"},       // unknown action
		{"a\x00b", "proxy"},            // control characters
		{"example.com/path", "direct"}, // paths are not routable distinctions
	} {
		if _, _, _, err := NormalizePolicyRule(bad.match, bad.action); err == nil {
			t.Errorf("NormalizePolicyRule(%q, %q) accepted", bad.match, bad.action)
		}
	}
}

func shareLinkInfo(t *testing.T, raw string) *NodeLinkInfo {
	t.Helper()
	info, err := ParseShareLinkInfo(raw)
	if err != nil {
		t.Fatalf("ParseShareLinkInfo(%q): %v", raw, err)
	}
	return info
}

func TestParseShareLinkInfoVlessReality(t *testing.T) {
	info := shareLinkInfo(t,
		"vless://uuid-SECRET@server.example:443?security=reality&pbk=PBKVALUE&sid=SHORTID&fp=chrome&sni=www.example.com#My%20Node")
	if info.Tag != "My Node" || info.Type != "vless" || info.Server != "server.example" || info.Port != 443 {
		t.Fatalf("unexpected info: %+v", info)
	}
	if !info.HasCredential {
		t.Fatal("vless link should report HasCredential")
	}
	// Leak regression: no credential material may appear in any returned field.
	rendered := serialize(info)
	for _, secret := range []string{"uuid-SECRET", "PBKVALUE", "SHORTID"} {
		if strings.Contains(rendered, secret) {
			t.Fatalf("NodeLinkInfo leaked %q: %s", secret, rendered)
		}
	}
}

func TestParseShareLinkInfoVariants(t *testing.T) {
	t.Run("hysteria2 mport", func(t *testing.T) {
		info := shareLinkInfo(t, "hy2://pw@h.example:8443?mport=20000-30000&sni=s.example#hy")
		if info.Type != "hysteria2" || info.Port != 0 {
			t.Fatalf("unexpected: %+v", info)
		}
		if len(info.ServerPorts) != 1 || info.ServerPorts[0] != "20000:30000" {
			t.Fatalf("server_ports: %v", info.ServerPorts)
		}
	})
	t.Run("vmess json authority", func(t *testing.T) {
		info := shareLinkInfo(t, "vmess://eyJhZGQiOiJ2LmV4YW1wbGUiLCJwb3J0IjoiNDQzIiwiaWQiOiJ2bWVzcy1zZWNyZXQiLCJwcyI6Ik5vZGUiLCJzY3kiOiJhdXRvIn0=")
		if info.Tag != "Node" || info.Type != "vmess" || info.Server != "v.example" || info.Port != 443 {
			t.Fatalf("unexpected: %+v", info)
		}
		if !info.HasCredential {
			t.Fatal("vmess should report HasCredential")
		}
		if strings.Contains(serialize(info), "vmess-secret") {
			t.Fatal("NodeLinkInfo leaked vmess uuid")
		}
	})
	t.Run("shadowrocket base64 authority", func(t *testing.T) {
		// method:uuid@host:port encoded as base64 authority, remarks= tag.
		info := shareLinkInfo(t, "vless://Y2hhY2hhMjAtaWV0Zi1wb2x5MTMwNTpzci11dWlk@host.example:443?remarks=SRNode")
		if info.Tag != "SRNode" || info.Server != "host.example" || info.Port != 443 {
			t.Fatalf("unexpected: %+v", info)
		}
	})
	t.Run("socks5", func(t *testing.T) {
		info := shareLinkInfo(t, "socks5://user:pw@socks.example:1080#S")
		if info.Type != "socks" || info.Port != 1080 || !info.HasCredential {
			t.Fatalf("unexpected: %+v", info)
		}
	})
	t.Run("tuic", func(t *testing.T) {
		info := shareLinkInfo(t, "tuic://uuid:pw@t.example:443?sni=t.example&congestion_control=bbr#T")
		if info.Type != "tuic" || info.Port != 443 {
			t.Fatalf("unexpected: %+v", info)
		}
	})
}

func TestParseShareLinkInfoRejections(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want error
	}{
		{"empty", "", nil},
		{"multiline", "vless://u@h:443#a\nsecond", nil},
		{"unsupported scheme", "ss://YWVzLTI1Ni1nY206cHc@a:1#x", nil},
		{"no port", "hy2://pw@h.example#x", nil},
		{"subscription link", "sub://aHR0cHM6Ly9leGFtcGxlLmNvbS9zdWI=", ErrSubLink},
	}
	for _, tc := range cases {
		_, err := ParseShareLinkInfo(tc.raw)
		if err == nil {
			t.Errorf("%s: expected error", tc.name)
			continue
		}
		if tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}

const testConf = `# Shadowrocket export
[General]
dns-server = 1.1.1.1, 8.8.8.8
skip-proxy = 192.168.0.0/16, *.local
bypass-tun = 10.0.0.0/8
include = extra.conf

[Host]
api.example.com = 127.0.0.1

[Rule]
DOMAIN-SUFFIX,example.org,DIRECT
DOMAIN,ads.example.com,REJECT
DOMAIN-KEYWORD,tracker,REJECT
IP-CIDR,10.0.0.0/8,DIRECT
GEOIP,CN,DIRECT
RULE-SET,https://lists.example.com/ads.txt,REJECT
FINAL,PROXY

[URL Rewrite]
^https?://example\.com/ad - reject

[MITM]
hostname = example.com

[Proxy]
SR = socks5, 127.0.0.1, 1080
`

func TestParseDocumentContentOnly(t *testing.T) {
	doc, err := ParseDocument(testConf, Hooks{})
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if doc.RuleCount("direct") != 2 { // DOMAIN-SUFFIX + IP-CIDR (GEOIP is tracked separately)
		t.Fatalf("direct count = %d", doc.RuleCount("direct"))
	}
	if doc.RuleCount("reject") != 2 {
		t.Fatalf("reject count = %d", doc.RuleCount("reject"))
	}
	if doc.RuleCount("proxy") != 0 {
		t.Fatalf("proxy count = %d", doc.RuleCount("proxy"))
	}
	if doc.Final != "PROXY" {
		t.Fatalf("final = %q", doc.Final)
	}
	if len(doc.Hosts) != 1 || doc.Hosts["api.example.com"] != "127.0.0.1" {
		t.Fatalf("hosts: %v", doc.Hosts)
	}
	if len(doc.GeoIP) != 1 || doc.GeoIP[0] != [2]string{"CN", "direct"} {
		t.Fatalf("geoip: %v", doc.GeoIP)
	}
	if len(doc.Includes) != 1 || doc.Includes[0] != "extra.conf" {
		t.Fatalf("includes: %v", doc.Includes)
	}
	if len(doc.RuleSets) != 1 || doc.RuleSets[0].URL != "https://lists.example.com/ads.txt" || doc.RuleSets[0].Target != "reject" {
		t.Fatalf("rulesets: %v", doc.RuleSets)
	}
	if len(doc.Rewrites) != 1 || len(doc.Mitm) != 1 || len(doc.Proxies) != 1 {
		t.Fatalf("unsupported sections: %+v", doc)
	}
}

func TestParseDocumentWithFetchHook(t *testing.T) {
	hooks := Hooks{
		FetchRuleSet: func(source, target string, add func(line string)) error {
			if source != "https://lists.example.com/ads.txt" || target != "reject" {
				t.Errorf("unexpected fetch %s -> %s", source, target)
			}
			add("DOMAIN,one.example,REJECT")
			add("DOMAIN,two.example,REJECT")
			add("DOMAIN,one.example,REJECT") // duplicate: not a new value
			return nil
		},
		Report: func(format string, args ...any) {
			if got := fmt.Sprintf(format, args...); !strings.Contains(got, "2 entries") {
				t.Errorf("report line = %q", got)
			}
		},
	}
	doc, err := ParseDocument(testConf, hooks)
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if doc.RuleCount("reject") != 4 { // 2 conf + 2 fetched
		t.Fatalf("reject count = %d", doc.RuleCount("reject"))
	}
	if len(doc.RuleSets) != 0 {
		t.Fatalf("fetched rule sets should not stay pending: %v", doc.RuleSets)
	}
}

func TestParseDocumentFetchError(t *testing.T) {
	hooks := Hooks{FetchRuleSet: func(source, target string, add func(line string)) error {
		return errors.New("HTTP 503")
	}}
	if _, err := ParseDocument(testConf, hooks); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("expected fetch error, got %v", err)
	}
}

func TestParseDocumentRejectsNonConf(t *testing.T) {
	for _, body := range []string{"", "just text", "\ufeff[Proxy]\nx = y"} {
		if _, err := ParseDocument(body, Hooks{}); err == nil {
			t.Errorf("ParseDocument(%q) accepted non-conf", body)
		}
	}
}

func TestParseDocumentCommentAndBOM(t *testing.T) {
	body := "\ufeff[Rule]\nDOMAIN-SUFFIX,a.example,DIRECT // inline comment\n# full comment\nDOMAIN,b.example,DIRECT\n"
	doc, err := ParseDocument(body, Hooks{})
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if doc.RuleCount("direct") != 2 {
		t.Fatalf("direct count = %d", doc.RuleCount("direct"))
	}
}

// serialize renders every exported field of the info struct so leak tests
// can assert absence of credential material.
func serialize(info *NodeLinkInfo) string {
	return fmt.Sprintf("%s|%s|%s|%d|%v|%v",
		info.Tag, info.Type, info.Server, info.Port, info.ServerPorts, info.HasCredential)
}
