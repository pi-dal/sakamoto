package mobilecore

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestConfReportProxyDomainsUseSharedTargetSemantics(t *testing.T) {
	content := "[Rule]\nDOMAIN,B.Example,PROXY\nDOMAIN-SUFFIX,a.example,CustomGroup\nDOMAIN,a.example,PROXY\nDOMAIN,direct.example,DIRECT\nDOMAIN,reject.example,REJECT\nDOMAIN,tail.example,TAILSCALE\nDOMAIN-KEYWORD,video,PROXY\nIP-CIDR,192.0.2.0/24,PROXY\nRULE-SET,https://rules.example/list?token=secret,PROXY\nFINAL,PROXY\n"
	raw, err := ParseConfContentJSON(content)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		ProxyDomains        []string `json:"proxyDomains"`
		ProxyNonDomainRules int      `json:"proxyNonDomainRules"`
	}
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.ProxyDomains, []string{"a.example", "b.example"}) || report.ProxyNonDomainRules != 2 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if strings.Contains(raw, "secret") {
		t.Fatal("report leaked remote token")
	}
}
