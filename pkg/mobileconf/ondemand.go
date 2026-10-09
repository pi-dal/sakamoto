package mobileconf

import (
	"bufio"
	"fmt"
	"sort"
	"strings"
)

const OnDemandMaxDomains = 128
const OnDemandMaxBytes = 2 << 20

// OnDemandSummary is deliberately small: routing snapshots must never be
// copied wholesale into iOS text views or NetworkExtension preferences.
type OnDemandSummary struct {
	Domains           []string `json:"domains"`
	HasNonDomainRules bool     `json:"hasNonDomainRules"`
	HasRemoteRules    bool     `json:"hasRemoteRules"`
}

func OnDemandLimitError() error {
	return fmt.Errorf("choose up to %d trigger domains; the complete proxy rules still apply once the VPN connects", OnDemandMaxDomains)
}

// OnDemandFromConf scans only the predicates relevant to VPN triggers. It
// shares ruleTypes, comment handling and target folding with ParseDocument,
// without allocating all IP/keyword rules or a large import report.
func OnDemandFromConf(content string) (OnDemandSummary, error) {
	summary := OnDemandSummary{Domains: []string{}}
	if len(content) > OnDemandMaxBytes {
		return summary, fmt.Errorf("configuration is too large for automatic domain import; choose trigger domains manually")
	}
	seen := map[string]bool{}
	section := ""
	hasSection := false
	scanner := bufio.NewScanner(strings.NewReader(strings.TrimPrefix(content, "\ufeff")))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	for scanner.Scan() {
		line := strings.TrimSpace(commentRe.Split(scanner.Text(), 2)[0])
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(line)
			hasSection = hasSection || section == "[rule]" || section == "[general]"
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if section == "[general]" {
			if key, value, ok := strings.Cut(line, "="); ok && strings.EqualFold(strings.TrimSpace(key), "include") {
				for _, include := range splitCSV(value) {
					summary.HasRemoteRules = summary.HasRemoteRules || IsRemote(include)
				}
			}
			continue
		}
		if section != "[rule]" {
			continue
		}
		parts := strings.SplitN(line, ",", 4)
		if len(parts) < 3 || normTarget(parts[2]) != "proxy" {
			continue
		}
		kind := ruleTypes[strings.ToUpper(strings.TrimSpace(parts[0]))]
		if strings.EqualFold(strings.TrimSpace(parts[0]), "RULE-SET") {
			summary.HasRemoteRules = true
		}
		if kind == "domain_keyword" || kind == "ip_cidr" {
			summary.HasNonDomainRules = true
		}
		if kind != "domain" && kind != "domain_suffix" {
			continue
		}
		host := strings.ToLower(strings.Trim(strings.TrimSpace(parts[1]), "."))
		if host == "" || len(host) > 253 || seen[host] {
			continue
		}
		if len(summary.Domains) == OnDemandMaxDomains {
			return summary, OnDemandLimitError()
		}
		seen[host] = true
		summary.Domains = append(summary.Domains, host)
	}
	if scanner.Err() != nil {
		return summary, fmt.Errorf("configuration line is too large for domain import")
	}
	if !hasSection {
		return summary, fmt.Errorf("not a Shadowrocket conf (missing [General]/[Rule])")
	}
	sort.Strings(summary.Domains)
	return summary, nil
}
