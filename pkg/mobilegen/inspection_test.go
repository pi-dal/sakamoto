package mobilegen

import (
	"bytes"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"strings"
	"testing"
)

func TestInspectCompiledRuleSnapshot(t *testing.T) {
	var buf bytes.Buffer
	rules := option.PlainRuleSet{Rules: []option.HeadlessRule{{Type: C.RuleTypeDefault, DefaultOptions: option.DefaultHeadlessRule{DomainSuffix: []string{"example.org"}, IPCIDR: []string{"192.0.2.0/24"}}}}}
	if err := srs.Write(&buf, rules, C.RuleSetVersion3); err != nil {
		t.Fatal(err)
	}
	raw, err := InspectRuleSetJSON(buf.Bytes())
	if err != nil || !strings.Contains(raw, "example.org") || !strings.Contains(raw, "192.0.2.0/24") {
		t.Fatalf("inspection lost compiled domains/IPs: %s %v", raw, err)
	}
	if _, err := InspectRuleSetJSON([]byte("broken")); err == nil {
		t.Fatal("accepted invalid binary")
	}
}
