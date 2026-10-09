package mobileconf

import (
	"fmt"
	"strings"
	"testing"
)

func TestOnDemandConfBoundedAndDeduplicated(t *testing.T) {
	body := "[Rule]\n" + strings.Repeat("DOMAIN-SUFFIX,Example.COM,PROXY\n", 20_000) + "DOMAIN,direct.example,DIRECT\nIP-CIDR,192.0.2.0/24,PROXY\nDOMAIN-KEYWORD,test,PROXY\nRULE-SET,https://rules.example/rules,PROXY"
	got, err := OnDemandFromConf(body)
	if err != nil || len(got.Domains) != 1 || got.Domains[0] != "example.com" || !got.HasNonDomainRules || !got.HasRemoteRules {
		t.Fatalf("unexpected bounded summary: %+v, %v", got, err)
	}
}

func TestOnDemandConfRejectsOverflowInsteadOfTruncating(t *testing.T) {
	var body strings.Builder
	body.WriteString("[Rule]\n")
	for i := 0; i <= OnDemandMaxDomains; i++ {
		fmt.Fprintf(&body, "DOMAIN,d%d.example,PROXY\n", i)
	}
	if _, err := OnDemandFromConf(body.String()); err == nil {
		t.Fatal("domain overflow must require a smaller trigger list")
	}
	if _, err := OnDemandFromConf("[Rule]\n" + strings.Repeat("x", OnDemandMaxBytes)); err == nil {
		t.Fatal("oversized source accepted")
	}
}
