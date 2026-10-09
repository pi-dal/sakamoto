package mobilegen

import (
	"bytes"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/pi-dal/sakamoto/pkg/mobileconf"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
)

// OnDemandRuleSetJSON reads only a small trigger preview. Check the expanded
// SRS payload before recovery and do not marshal the complete routing snapshot.
func OnDemandRuleSetJSON(data []byte) (string, error) {
	if len(data) < 5 || len(data) > mobileconf.OnDemandMaxBytes || !bytes.Equal(data[:3], srs.MagicBytes[:]) {
		return "", fmt.Errorf("rule snapshot is invalid or too large for domain import; choose trigger domains manually")
	}
	reader, err := zlib.NewReader(bytes.NewReader(data[4:]))
	if err != nil {
		return "", fmt.Errorf("cannot read rule snapshot")
	}
	expanded, err := io.Copy(io.Discard, io.LimitReader(reader, mobileconf.OnDemandMaxBytes+1))
	_ = reader.Close()
	if err != nil {
		return "", fmt.Errorf("cannot read rule snapshot")
	}
	if expanded > mobileconf.OnDemandMaxBytes {
		return "", fmt.Errorf("expanded rule snapshot is too large for domain import; choose trigger domains manually")
	}
	rules, err := srs.Read(bytes.NewReader(data), true)
	if err != nil {
		return "", fmt.Errorf("cannot read rule snapshot")
	}
	summary := mobileconf.OnDemandSummary{Domains: []string{}}
	seen := map[string]bool{}
	for _, rule := range rules.Options.Rules {
		if rule.Type != C.RuleTypeDefault || rule.DefaultOptions.Invert {
			continue
		}
		opts := rule.DefaultOptions
		summary.HasNonDomainRules = summary.HasNonDomainRules || len(opts.DomainKeyword) > 0 || len(opts.IPCIDR) > 0
		for _, values := range [][]string{opts.Domain, opts.DomainSuffix} {
			for _, value := range values {
				host := strings.ToLower(strings.Trim(value, "."))
				if host == "" || len(host) > 253 || seen[host] {
					continue
				}
				if len(summary.Domains) == mobileconf.OnDemandMaxDomains {
					return "", mobileconf.OnDemandLimitError()
				}
				seen[host] = true
				summary.Domains = append(summary.Domains, host)
			}
		}
	}
	sort.Strings(summary.Domains)
	raw, err := json.Marshal(summary)
	return string(raw), err
}
