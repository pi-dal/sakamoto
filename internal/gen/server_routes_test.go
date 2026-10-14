package gen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/sagernet/sing-box/option"
)

func TestGenerationExcludesEveryLiteralProxyServerAndRefreshesWithNodes(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "main.conf")
	if err := os.WriteFile(conf, []byte("[General]\nbypass-tun=10.0.0.0/8,203.0.113.8/32\ntun-excluded-routes=fc00::/7\n[Rule]\nFINAL,PROXY\n"), 0600); err != nil {
		t.Fatal(err)
	}
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"outbounds":[{"type":"trojan","tag":"subscription","server":"198.51.100.20","server_port":443,"password":"synthetic"},{"type":"trojan","tag":"duplicate","server":"::ffff:203.0.113.8","server_port":443,"password":"synthetic"}]}`))
	}))
	defer feed.Close()
	nodes := filepath.Join(dir, "nodes.txt")
	links := []string{"trojan://test@203.0.113.8:443#manual", "trojan://test@[2001:db8::8]:443#v6", "trojan://test@never-resolve.invalid:443#hostname", "socks://dXNlcjpwYXNzQDE5OC41MS4xMDAuOTo5MDAw?chain=MAINPROXY#exit"}
	cfg := config.Default()
	cfg.TailscaleOptimize = false
	cfg.Subscriptions = []config.SubSource{{Name: "feed", URL: feed.URL, Format: "singbox"}}
	out := filepath.Join(dir, "output")
	for _, added := range []bool{false, true, false} {
		current := slices.Clone(links)
		if added {
			current = append(current, "trojan://test@203.0.113.99:443#new")
		}
		if err := os.WriteFile(nodes, []byte(strings.Join(current, "\n")), 0600); err != nil {
			t.Fatal(err)
		}
		lookup := func(_ context.Context, host string) ([]netip.Addr, error) {
			if host != "never-resolve.invalid" {
				t.Fatalf("unexpected lookup %s", host)
			}
			return []netip.Addr{netip.MustParseAddr("2001:db8::88"), netip.MustParseAddr("203.0.113.88")}, nil
		}
		if err := Run(Options{ConfPath: conf, NodesFile: nodes, Cfg: cfg, OutDir: out, Quiet: true, LookupProxyIPs: lookup}); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(out, "config.json"))
		if err != nil {
			t.Fatal(err)
		}
		var generated struct {
			Inbounds []json.RawMessage `json:"inbounds"`
		}
		if err := json.Unmarshal(raw, &generated); err != nil {
			t.Fatal(err)
		}
		var tun struct {
			Excluded []string `json:"route_exclude_address"`
		}
		if err := json.Unmarshal(generated.Inbounds[0], &tun); err != nil {
			t.Fatal(err)
		}
		want := []string{"10.0.0.0/8", "203.0.113.8/32", "fc00::/7", "2001:db8::8/128", "198.51.100.9/32", "198.51.100.20/32", "203.0.113.88/32", "2001:db8::88/128"}
		if added {
			want = append(want, "203.0.113.99/32")
		}
		if len(tun.Excluded) != len(want) {
			t.Fatalf("exclusions = %v, want %v", tun.Excluded, want)
		}
		for _, route := range want {
			if !slices.Contains(tun.Excluded, route) {
				t.Fatalf("proxy route %s missing: %v", route, tun.Excluded)
			}
		}
		var native option.TunInboundOptions
		if err := json.Unmarshal(generated.Inbounds[0], &native); err != nil {
			t.Fatalf("invalid native TUN: %v", err)
		}
	}
}
