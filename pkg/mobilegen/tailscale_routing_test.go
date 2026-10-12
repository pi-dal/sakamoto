package mobilegen

import (
	"context"
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/route/rule"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"
)

// Only the peer-map boundary is faked. Matching, action decoding, and rule
// lifecycle use the pinned core that runs in the app's packet tunnel.
type testTailnetEndpoint struct {
	adapter.Outbound
	known bool
}

func (e testTailnetEndpoint) PreferredDomain(_ *adapter.InboundContext, _ string) bool { return false }
func (e testTailnetEndpoint) PreferredAddress(_ *adapter.InboundContext, address netip.Addr) bool {
	return e.known && (address == netip.MustParseAddr("100.83.12.34") || address == netip.MustParseAddr("fd7a:115c:a1e0::1234"))
}

type testTailnetManager struct {
	adapter.OutboundManager
	tag      string
	endpoint testTailnetEndpoint
}

func (m testTailnetManager) Outbound(tag string) (adapter.Outbound, bool) {
	return m.endpoint, tag == m.tag
}

func assertTailnetCoreRouting(t *testing.T, content string) {
	t.Helper()
	var config struct {
		Endpoints []struct{ Tag string }
		Route     struct{ Rules []json.RawMessage }
	}
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		t.Fatal(err)
	}
	tag := config.Endpoints[0].Tag
	for _, known := range []bool{false, true} {
		for _, address := range []string{"100.83.12.34", "fd7a:115c:a1e0::1234"} {
			ctx := service.ContextWith[adapter.OutboundManager](context.Background(), testTailnetManager{tag: tag, endpoint: testTailnetEndpoint{known: known}})
			selected := ""
			for _, raw := range config.Route.Rules {
				var options option.Rule
				if err := options.UnmarshalJSONContext(ctx, raw); err != nil {
					t.Fatal(err)
				}
				current, err := rule.NewRule(ctx, log.NewNOPFactory().Logger(), options, true)
				if err != nil {
					t.Fatal(err)
				}
				if err := current.Start(); err != nil {
					t.Fatal(err)
				}
				metadata := &adapter.InboundContext{Network: "tcp", Protocol: "ssh", Destination: M.SocksaddrFromNetIP(netip.AddrPortFrom(netip.MustParseAddr(address), 22))}
				matched := current.Match(metadata)
				_ = current.Close()
				if !matched {
					continue
				}
				if action, ok := current.Action().(*rule.RuleActionRoute); ok {
					selected = action.Outbound
					break
				}
			}
			if selected != tag {
				t.Fatalf("SSH to %s selected %q with peer map ready=%v", address, selected, known)
			}
		}
	}
}
