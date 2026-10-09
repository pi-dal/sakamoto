package mobilegen

import (
	"bytes"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/pi-dal/sakamoto/pkg/mobileconf"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

func TestOnDemandCompiledRuleSetAndOverflow(t *testing.T) {
	for _, count := range []int{2, mobileconf.OnDemandMaxDomains + 1} {
		domains := make([]string, count)
		for i := range domains {
			domains[i] = fmt.Sprintf("d%d.example", i)
		}
		var data bytes.Buffer
		err := srs.Write(&data, option.PlainRuleSet{Rules: []option.HeadlessRule{{Type: C.RuleTypeDefault, DefaultOptions: option.DefaultHeadlessRule{Domain: domains, IPCIDR: []string{"192.0.2.0/24"}}}}}, C.RuleSetVersionCurrent)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := OnDemandRuleSetJSON(data.Bytes())
		if count > mobileconf.OnDemandMaxDomains {
			if err == nil || raw != "" {
				t.Fatal("overflow must not return a partial import")
			}
			continue
		}
		var got mobileconf.OnDemandSummary
		if err != nil || json.Unmarshal([]byte(raw), &got) != nil || len(got.Domains) != count || !got.HasNonDomainRules {
			t.Fatalf("unexpected compiled summary: %s, %v", raw, err)
		}
	}
}

func TestOnDemandRejectsExpandedPayloadBeforeRecovery(t *testing.T) {
	var data bytes.Buffer
	data.Write(srs.MagicBytes[:])
	data.WriteByte(C.RuleSetVersionCurrent)
	writer := zlib.NewWriter(&data)
	_, _ = writer.Write(bytes.Repeat([]byte{0}, mobileconf.OnDemandMaxBytes+1))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if data.Len() > 16<<10 {
		t.Fatal("fixture must exercise a small compressed payload")
	}
	if raw, err := OnDemandRuleSetJSON(data.Bytes()); err == nil || raw != "" || !strings.Contains(err.Error(), "expanded") {
		t.Fatalf("expanded budget not enforced: %s, %v", raw, err)
	}
}
