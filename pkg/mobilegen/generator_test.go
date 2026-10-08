package mobilegen

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func request(t *testing.T, files map[string]string, main string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"version": 1, "main_conf": main, "files": files})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func TestNodesOnlyGenerationCompilesPortableRules(t *testing.T) {
	raw, err := GenerateProfileJSON(request(t, map[string]string{"nodes.txt": "trojan://test@example.com:443#node"}, ""), `{"mode":"on","threshold":3}`)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Config           string
		Files            map[string][]byte
		SourceBundleJSON string
	}
	if err = json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Files) < 2 || !strings.Contains(result.Config, `"MainProxy"`) {
		t.Fatalf("missing generated rules or proxy: files=%d proxy=%v", len(result.Files), strings.Contains(result.Config, `"MainProxy"`))
	}
	var root struct {
		Route struct {
			Rules []map[string]any
			Final string
		}
	}
	if err := json.Unmarshal([]byte(result.Config), &root); err != nil {
		t.Fatal(err)
	}
	for _, rule := range root.Route.Rules {
		if names, exists := rule["rule_set"]; exists && (names == nil || len(names.([]any)) == 0) {
			t.Fatal("empty rule-set condition would route all traffic directly")
		}
	}
	if root.Route.Final != "MainProxy" {
		t.Fatal("On must route unmatched traffic through the proxy")
	}
	if strings.Contains(result.Config, "protected-dns") || strings.Contains(result.Config, "in-mixed") {
		t.Fatal("host-only inbound leaked")
	}
}
func TestIncludesAndPolicyAreCompiledUsingSharedHostPipeline(t *testing.T) {
	files := map[string]string{"nodes.txt": "trojan://test@example.com:443#node", "sources/main.conf": "[General]\ninclude = child.conf\n[Rule]\nFINAL,DIRECT", "sources/child.conf": "[Rule]\nDOMAIN,direct.example,DIRECT\nDOMAIN,proxy.example,PROXY", "policy.json": `[{"match":"blocked.example","action":"reject"}]`}
	raw, err := GenerateProfileJSON(request(t, files, "sources/main.conf"), "")
	if err != nil {
		t.Fatal(err)
	}
	var generated struct{ Files map[string][]byte }
	if err := json.Unmarshal([]byte(raw), &generated); err != nil {
		t.Fatal(err)
	}
	if len(generated.Files["rules/reject.srs"]) == 0 {
		t.Fatal("policy bucket not compiled")
	}
	delete(files, "sources/child.conf")
	if _, err = GenerateProfileJSON(request(t, files, "sources/main.conf"), ""); err == nil {
		t.Fatal("missing include accepted")
	}
	files["sources/main.conf"] = "[General]\ninclude = ../../escape.conf\n[Rule]\nFINAL,DIRECT"
	if _, err = GenerateProfileJSON(request(t, files, "sources/main.conf"), ""); err == nil {
		t.Fatal("include traversal accepted")
	}
}
func TestSubscriptionFetchAndInvalidSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("trojan://test@example.com:443#subscription"))
	}))
	defer server.Close()
	subscriptions, _ := json.Marshal([]map[string]string{{"name": "test", "url": server.URL, "format": "auto"}})
	if _, err := GenerateProfileJSON(request(t, map[string]string{"subscriptions.json": string(subscriptions)}, ""), ""); err != nil {
		t.Fatal(err)
	}
	for _, files := range []map[string]string{{"nodes.txt": "invalid"}, {"../escape.conf": "[Rule]"}, {"nodes.txt": ""}} {
		if _, err := GenerateProfileJSON(request(t, files, ""), ""); err == nil {
			t.Fatal("bad source accepted")
		}
	}
}
