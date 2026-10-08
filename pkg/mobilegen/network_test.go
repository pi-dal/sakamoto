package mobilegen

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

type scriptedFetcher struct {
	calls     int
	responses []*SourceResponse
}

func (f *scriptedFetcher) Fetch(_ string) (*SourceResponse, error) {
	i := min(f.calls, len(f.responses)-1)
	f.calls++
	return f.responses[i], nil
}

func TestNativeFetchRetriesTransientRuleDownload(t *testing.T) {
	f := &scriptedFetcher{responses: []*SourceResponse{{Failure: "dns"}, {StatusCode: 200, Body: []byte("DOMAIN,remote.example,PROXY")}}}
	files := map[string]string{"nodes.txt": "trojan://test@example.com:443#node", "main.conf": "[Rule]\nRULE-SET,https://rules.example/private/list?token=hidden,PROXY\nFINAL,DIRECT"}
	if _, err := GenerateProfileWithFetcherJSON(request(t, files, "main.conf"), "", f); err != nil {
		t.Fatal(err)
	}
	if f.calls != 2 {
		t.Fatalf("requests=%d; wanted one retry", f.calls)
	}
}
func TestRemoteFailurePreservesStageHostAndStatusWithoutSecrets(t *testing.T) {
	f := &scriptedFetcher{responses: []*SourceResponse{{StatusCode: 404}}}
	files := map[string]string{"nodes.txt": "trojan://test@example.com:443#node", "main.conf": "[Rule]\nRULE-SET,https://user:password@rules.example/private/list?token=hidden,PROXY\nFINAL,DIRECT"}
	_, err := GenerateProfileWithFetcherJSON(request(t, files, "main.conf"), "", f)
	if err == nil {
		t.Fatal("missing rule source accepted")
	}
	text := err.Error()
	for _, part := range []string{"RULE-SET", "rules.example", "HTTP 404"} {
		if !strings.Contains(text, part) {
			t.Fatal("lost diagnostic:", text)
		}
	}
	for _, part := range []string{"password", "private", "hidden", "https://"} {
		if strings.Contains(text, part) {
			t.Fatal("credential leaked")
		}
	}
	if f.calls != 1 {
		t.Fatal("permanent error was retried")
	}
}
func TestHTMLRuleResponseIsRejected(t *testing.T) {
	f := &scriptedFetcher{responses: []*SourceResponse{{StatusCode: 200, Body: []byte("<!DOCTYPE html><html>Sign in</html>")}}}
	files := map[string]string{"nodes.txt": "trojan://test@example.com:443#node", "main.conf": "[Rule]\nRULE-SET,https://rules.example/list,PROXY\nFINAL,DIRECT"}
	_, err := GenerateProfileWithFetcherJSON(request(t, files, "main.conf"), "", f)
	if err == nil || !strings.Contains(err.Error(), "HTML page") {
		t.Fatal("HTML source not diagnosed")
	}
}
func TestNetworkReasonsAndNestedURLsRemainUseful(t *testing.T) {
	err := fmt.Errorf("download geoip-CN: %w", errors.New(`Get "https://name:secret@raw.example/key/secret?token=hidden": request timed out`))
	text := sanitizeGenerationError(err)
	if !strings.Contains(text, "geoip-CN") || !strings.Contains(text, "raw.example") || !strings.Contains(text, "timed out") {
		t.Fatal(text)
	}
	if strings.Contains(text, "secret") || strings.Contains(text, "hidden") {
		t.Fatal("credential leaked")
	}
}
