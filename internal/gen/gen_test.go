package gen

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
)

func TestSingleNonRealityNodeStillHasFallbackGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.txt")
	if err := os.WriteFile(path, []byte("hysteria2://secret@203.0.113.10:443#only-hy2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Subscriptions = nil
	groups, _, _, members, err := buildOutbounds(Options{NodesFile: path, SRJSONPath: "/missing-backup"}, cfg, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) < 2 || members[0] != "OthersAuto" {
		t.Fatalf("single-node fallback missing: %v", members)
	}
}

func TestFailedSubscriptionDoesNotSilentlyDropNodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	cfg := config.Default()
	cfg.Subscriptions = []config.SubSource{{Name: "test", URL: server.URL}}
	_, _, _, _, err := buildOutbounds(Options{}, cfg, server.Client())
	if err == nil {
		t.Fatal("failed subscription must not silently generate partial config")
	}
}

func TestUserPolicyRulesNormalizeAndApply(t *testing.T) {
	cfg := config.Default()
	cfg.PolicyRules = []config.PolicyRule{
		{Match: "https://Example.com/login?token=hidden", Action: "proxy"},
		{Match: "*.local.test", Action: "direct"},
		{Match: "keyword:tracker", Action: "reject"},
		{Match: "cidr:192.0.2.0/24", Action: "direct"},
	}
	p := &parsedConf{bk: buckets{}, general: map[string]string{}}
	if err := applyPolicyRules(p, cfg); err != nil {
		t.Fatal(err)
	}
	if !p.bk["proxy"]["domain"]["example.com"] ||
		!p.bk["direct"]["domain_suffix"]["local.test"] ||
		!p.bk["reject"]["domain_keyword"]["tracker"] ||
		!p.bk["direct"]["ip_cidr"]["192.0.2.0/24"] {
		t.Fatalf("policy rules were not normalized: %#v", p.bk)
	}
}

func TestDirectOverridesAreCompiledBeforeRuleSets(t *testing.T) {
	p := &parsedConf{bk: buckets{}, general: map[string]string{
		"skip-proxy":     "*.local.test,10.0.0.0/8",
		"always-real-ip": "*.ts.net,controlplane.tailscale.com",
	}}
	entries, err := buildRules(p, t.TempDir(), &http.Client{}, tailscaleInfo{Present: true, MagicDNSSuffix: "example.ts.net"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.bk["direct"]["domain_suffix"]["ts.net"] || !p.bk["direct"]["ip_cidr"]["fd7a:115c:a1e0::/48"] {
		t.Fatal("direct overrides missing before compile")
	}
	if len(entries) == 0 {
		t.Fatal("no compiled rule sets")
	}
}

func TestShadowrocketVLESSCredentialsAndGRPC(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	encode := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	links := []string{
		"vless://" + encode("none:"+id+"@203.0.113.5:443") + "?remarks=Vision&tls=1&peer=example.com&xtls=2&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=abcd&fingerprint=chrome",
		"vless://" + encode(":"+id+"@203.0.113.6:443") + "?remarks=Azure&tls=1&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"vless://" + encode("none:"+id+"@203.0.113.7:443") + "?remarks=gPRC&obfs=grpc&path=grpc&tls=1&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"vless://" + id + "@203.0.113.8:443#standard",
	}
	nodes := parseShareLines(links)
	if len(nodes) != 4 {
		t.Fatalf("parsed %d nodes", len(nodes))
	}
	for _, n := range nodes {
		if n["uuid"] != id {
			t.Errorf("wrong VLESS uuid for %s: %v", n["tag"], n["uuid"])
		}
	}
	if nodes[0]["flow"] != "xtls-rprx-vision" {
		t.Fatal("vision flow lost")
	}
	transport, _ := nodes[2]["transport"].(map[string]any)
	if transport["type"] != "grpc" || transport["service_name"] != "grpc" {
		t.Fatalf("grpc service_name lost: %v", transport)
	}
}

func TestIncludeMergesParentAndAdRules(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "macOS.conf")
	ad := filepath.Join(dir, "ad.conf")
	if err := os.WriteFile(ad, []byte("[General]\nipv6=false\n[Rule]\nDOMAIN-SUFFIX,ads.example,REJECT\nFINAL,MainProxy\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("[General]\ninclude=ad.conf\n[Rule]\nDOMAIN-SUFFIX,app.example,DIRECT\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := parseConf(root, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	if !p.bk["reject"]["domain_suffix"]["ads.example"] || !p.bk["direct"]["domain_suffix"]["app.example"] {
		t.Fatal("parent or include rules missing")
	}
	if p.final != "MainProxy" || p.general["ipv6"] != "false" {
		t.Fatal("inherited general/final missing")
	}
}
func TestRemoteConfFollowsRelativeInclude(t *testing.T) {
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		switch r.URL.Path {
		case "/profiles/macOS.conf":
			_, _ = w.Write([]byte("[General]\ninclude=ad.conf\n[Rule]\nDOMAIN-SUFFIX,app.example,PROXY\n"))
		case "/profiles/ad.conf":
			_, _ = w.Write([]byte("[Rule]\nDOMAIN-SUFFIX,ads.example,REJECT\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p, err := parseConf(server.URL+"/profiles/macOS.conf", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if calls["/profiles/ad.conf"] != 1 || !p.bk["reject"]["domain_suffix"]["ads.example"] || !p.bk["proxy"]["domain_suffix"]["app.example"] {
		t.Fatal("remote include not merged")
	}
}
func TestRemoteImportRejectsMissingIncludeAndHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/macOS.conf" {
			_, _ = w.Write([]byte("[General]\ninclude=missing.conf\n"))
			return
		}
		if r.URL.Path == "/html" {
			_, _ = w.Write([]byte("<html>not a conf</html>"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	for _, path := range []string{"/macOS.conf", "/html"} {
		if _, err := parseConf(server.URL+path, server.Client()); err == nil {
			t.Fatalf("%s accepted invalid config", path)
		}
	}
}
func TestMissingIncludeCannotSilentlyDropAdRules(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "macOS.conf")
	if err := os.WriteFile(root, []byte("[General]\ninclude=missing-ad.conf\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := parseConf(root, &http.Client{})
	if err == nil || !strings.Contains(err.Error(), "missing-ad.conf") {
		t.Fatalf("missing include must fail: %v", err)
	}
}
