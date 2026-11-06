package desktop_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/desktop"
	"github.com/pi-dal/sakamoto/internal/experiment"
)

func TestDesktopManagementEndToEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sakamoto.yaml")
	cfg := config.Default()
	cfg.NodesFile = filepath.Join(dir, "nodes.txt")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(cfg.Save(path))
	must(os.WriteFile(cfg.NodesFile, []byte("# Keep comment\nsocks://fixture:secret@127.0.0.1:1080#Original\n"), 0600))
	must(os.WriteFile(filepath.Join(dir, "config.json"), []byte("runtime stays unchanged"), 0600))
	c := desktop.New(path, desktop.Options{})
	ctx := context.Background()
	m, err := c.Management()
	must(err)
	if len(m.Nodes) != 1 {
		t.Fatal("node projection missing")
	}
	must(c.SaveNode(ctx, m.Nodes[0], "socks://fixture:secret@127.0.0.1:1081#Edited"))
	if err = c.SaveNode(ctx, m.Nodes[0], "socks://fixture:secret@127.0.0.1:1082#Stale"); err == nil {
		t.Fatal("stale node overwrite accepted")
	}
	m, err = c.Management()
	must(err)
	endpoint, err := desktop.NodeEndpoint(m.Nodes[0].Raw)
	must(err)
	if endpoint.Server != "127.0.0.1" || endpoint.Port != "1081" {
		t.Fatal("endpoint missing")
	}
	edited, err := desktop.UpdateNodeEndpoint(m.Nodes[0].Raw, "Edited", "127.0.0.1", "1081")
	must(err)
	if edited != m.Nodes[0].Raw {
		t.Fatal("endpoint edit lost credentials/options")
	}
	must(c.SaveNode(ctx, desktop.ManualNode{}, "socks://fixture:secret@127.0.0.1:1082#Added"))
	if err = c.SaveNode(ctx, desktop.ManualNode{}, "bad link"); err == nil {
		t.Fatal("invalid node accepted")
	}
	m, err = c.Management()
	must(err)
	must(c.SaveSubscriptions(ctx, m, []config.SubSource{{Name: "Fixture", URL: "https://example.org/sub", Format: "auto"}}))
	if err = c.SaveSubscriptions(ctx, m, nil); err == nil {
		t.Fatal("stale subscriptions accepted")
	}
	m, err = c.Management()
	must(err)
	if err = c.SaveSubscriptions(ctx, m, []config.SubSource{{Name: "Bad", URL: "file:///tmp/sub", Format: "auto"}}); err == nil {
		t.Fatal("invalid subscription accepted")
	}
	must(c.SavePolicies(ctx, m, []config.PolicyRule{{Match: "protected.example", Action: "direct"}}))
	if err = c.SavePolicies(ctx, m, nil); err == nil {
		t.Fatal("stale policy overwrite accepted")
	}
	record := func(domain string, direct bool) {
		must(experiment.Record(dir, experiment.LearningResult{Candidate: experiment.Candidate{Domain: domain, Failures: 3, Required: 3}, Time: time.Now(), State: "no_improvement", Direct: experiment.ProbeResult{Reachable: direct, Error: func() string {
			if direct {
				return ""
			}
			return "timeout"
		}()}}))
	}
	record("failed.example", false)
	record("failed.example", false)
	record("protected.example", false)
	record("recovered.example", true)
	learning, err := c.Learning()
	must(err)
	if len(learning.Failures) != 2 {
		t.Fatalf("wrong candidates: %+v", learning.Failures)
	}
	must(c.AddRecentFailures(ctx, []string{"failed.example", "failed.example", "protected.example"}))
	must(c.AddRecentFailures(ctx, []string{"failed.example"}))
	if err = c.AddRecentFailures(ctx, []string{"unobserved.example"}); err == nil {
		t.Fatal("unobserved domain accepted")
	}
	saved, err := config.Load(path)
	must(err)
	if len(saved.PolicyRules) != 2 || saved.PolicyRules[0].Action != "direct" || saved.API.Secret != cfg.API.Secret {
		t.Fatal("policy/credential preservation failed")
	}
	raw, err := os.ReadFile(cfg.NodesFile)
	must(err)
	if string(raw) != "# Keep comment\nsocks://fixture:secret@127.0.0.1:1081#Edited\nsocks://fixture:secret@127.0.0.1:1082#Added\n" {
		t.Fatal("node comments or unrelated lines lost")
	}
	raw, err = os.ReadFile(filepath.Join(dir, "config.json"))
	must(err)
	if string(raw) != "runtime stays unchanged" {
		t.Fatal("runtime changed on source save")
	}
	// Field editing runs through the real source writer, not a replacement generator.
	vmess := "vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"add":"old.example","port":"443","ps":"Original","id":"fixture-id","aid":0,"extension":{"keep":true}}`))
	encoded := "vless://" + base64.RawURLEncoding.EncodeToString([]byte("fixture-id@old.example:443")) + "?security=reality&pbk=fixture%2Bkey&sid=01#Original"
	for _, link := range []string{
		"socks://fixture:fixture-secret@old.example:1080?chain=UPSTREAM#Original",
		"vless://fixture-id@old.example:443?type=ws&path=%2Fcustom&host=cdn.example#Original",
		"hy2://fixture-secret@old.example:443?mport=443,5000-6000&sni=cdn.example#Original",
		encoded, vmess,
	} {
		endpoint, e := desktop.NodeEndpoint(link)
		must(e)
		unchanged, e := desktop.UpdateNodeEndpoint(link, endpoint.Remark, endpoint.Server, endpoint.Port)
		must(e)
		if unchanged != link {
			t.Fatal("unchanged endpoint was rewritten")
		}
		port := "8443"
		if endpoint.Port == "" {
			port = ""
		}
		changed, e := desktop.UpdateNodeEndpoint(link, "Edited endpoint", "::1", port)
		must(e)
		must(c.SaveNode(ctx, desktop.ManualNode{}, link))
		loaded, e := c.Management()
		must(e)
		must(c.SaveNode(ctx, loaded.Nodes[len(loaded.Nodes)-1], changed))
		loaded, e = c.Management()
		must(e)
		actual, e := desktop.NodeEndpoint(loaded.Nodes[len(loaded.Nodes)-1].Raw)
		must(e)
		if actual.Remark != "Edited endpoint" || actual.Server != "::1" || actual.Port != port {
			t.Fatal("saved endpoint differs", actual)
		}
		if link == vmess {
			data, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(changed, "vmess://"))
			must(e)
			var fields map[string]json.RawMessage
			must(json.Unmarshal(data, &fields))
			if string(fields["id"]) != `"fixture-id"` || string(fields["extension"]) != `{"keep":true}` || string(fields["aid"]) != "0" {
				t.Fatal("VMess options changed")
			}
		} else {
			before, e := url.Parse(link)
			must(e)
			after, e := url.Parse(changed)
			must(e)
			if before.RawQuery != after.RawQuery {
				t.Fatal("query options changed")
			}
			if link != encoded && before.User.String() != after.User.String() {
				t.Fatal("credentials changed")
			}
			if link == encoded && after.User.Username() != "fixture-id" {
				t.Fatal("encoded credential changed")
			}
		}
		for _, invalid := range []struct{ host, port string }{{"bad/path", port}, {"host@other", port}, {"host?query", port}, {"bad:host", port}, {"example.org", "0"}, {"example.org", "65536"}} {
			if _, e := desktop.UpdateNodeEndpoint(link, "Edited", invalid.host, invalid.port); e == nil {
				t.Fatal("invalid endpoint accepted", invalid)
			}
		}
	}
	must(os.WriteFile(filepath.Join(dir, experiment.ReportFileName), []byte("bad history"), 0600))
	if _, err = c.Learning(); err == nil {
		t.Fatal("corrupt history hidden")
	}
	if out := os.Getenv("SAKAMOTO_MENUBAR_ARTIFACT_DIR"); out != "" {
		must(os.MkdirAll(out, 0755))
		must(os.WriteFile(filepath.Join(out, "management-e2e.json"), []byte("{\"passed\":true,\"staleEditsRejected\":true,\"duplicatesSkipped\":true,\"explicitOverridesPreserved\":true,\"runtimeUnchanged\":true,\"usesRealTUN\":false}\n"), 0644))
	}
}
