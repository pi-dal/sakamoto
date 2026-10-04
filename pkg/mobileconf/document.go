package mobileconf

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"path"
	"regexp"
	"strings"
)

// commentRe strips only whitespace-prefixed comments; https:// URLs in rule
// values survive (same semantics as internal/gen).
var commentRe = regexp.MustCompile(`\s//`)

// Buckets is the parsed rule store: bucket target ("proxy"/"direct"/"reject")
// → rule kind (domain/domain_suffix/domain_keyword/ip_cidr) → values.
type Buckets = map[string]map[string]map[string]bool

// RuleSetRef is a RULE-SET entry that was NOT fetched during parsing —
// either because no fetch hook was supplied (mobile content-only parsing) or
// because parsing stopped on an earlier error. A ref is pending work for the
// host generator; it is never counted as generated rules.
type RuleSetRef struct {
	URL    string
	Target string
}

// Document is one parsed Shadowrocket conf document.
type Document struct {
	Buckets  Buckets
	General  map[string]string
	Hosts    map[string]string
	GeoIP    [][2]string // (country code, target)
	Final    string
	Includes []string     // raw include= refs, unresolved (relative or remote)
	RuleSets []RuleSetRef // pending when no fetch hook handled them

	// Unsupported sections retained for the migration report only.
	Rewrites []string // [URL Rewrite]
	Mitm     []string // [MITM]
	Proxies  []string // [Proxy] entries reported but not imported as nodes.
	PGroups  []string // [Proxy Group] entries replaced by sakamoto groups.
	Scripts  []string // [Script]/[Host]
}

// Hooks carries the host-side callbacks. All fields are optional; mobile
// callers pass the zero value.
type Hooks struct {
	// FetchRuleSet fetches one remote RULE-SET list. add applies each list
	// line to the document with the same parser as [Rule] lines. Errors abort
	// parsing with that error (matching the host importer).
	FetchRuleSet func(source, target string, add func(line string)) error
	// Report receives the host generator's console lines (RULE-SET entry
	// counts). Nil on mobile — content-only parsing reports nothing.
	Report func(format string, args ...any)
}

// RuleCount counts the values collected for one bucket target.
func (d *Document) RuleCount(target string) int {
	n := 0
	for _, s := range d.Buckets[target] {
		n += len(s)
	}
	return n
}

// TotalRules counts every rule value across all buckets.
func (d *Document) TotalRules() int {
	n := 0
	for _, m := range d.Buckets {
		for _, s := range m {
			n += len(s)
		}
	}
	return n
}

// AddRule records one value into a bucket, deduplicated.
func (d *Document) AddRule(target, kind, value string) {
	if d.Buckets[target] == nil {
		d.Buckets[target] = map[string]map[string]bool{}
	}
	if d.Buckets[target][kind] == nil {
		d.Buckets[target][kind] = map[string]bool{}
	}
	d.Buckets[target][kind][value] = true
}

// CheckContent is the content sanity gate shared with the host importer: a
// BOM is stripped and a Shadowrocket section marker is required.
func CheckContent(body []byte) (string, error) {
	text := strings.TrimPrefix(string(body), "\ufeff")
	if !strings.Contains(text, "[General]") && !strings.Contains(text, "[Rule]") {
		return "", errors.New("not a Shadowrocket conf (missing [General]/[Rule])")
	}
	return text, nil
}

// ParseDocument parses ONE conf document from memory. Includes are collected
// but never resolved here; RULE-SET entries are fetched only through the
// hook. Parsing fails on the first recorded error, mirroring the host
// importer's behavior of surfacing a single failure for the whole document.
func ParseDocument(body string, hooks Hooks) (*Document, error) {
	text, err := CheckContent([]byte(body))
	if err != nil {
		return nil, err
	}
	doc := &Document{Buckets: Buckets{}, General: map[string]string{}, Hosts: map[string]string{}}
	var errs []error
	var includes []string

	var add func(line, defaultTarget string)
	add = func(line, defaultTarget string) {
		line = commentRe.Split(line, 2)[0]
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			return
		}
		parts := strings.Split(line, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		rt := strings.ToUpper(parts[0])
		if key, ok := ruleTypes[rt]; ok && len(parts) >= 2 {
			if key == "" {
				return
			}
			tgt := defaultTarget
			if len(parts) >= 3 {
				tgt = parts[2]
			}
			if tgt == "" {
				return
			}
			doc.AddRule(normTarget(tgt), key, parts[1])
		} else if rt == "RULE-SET" && len(parts) >= 3 {
			target := normTarget(parts[2])
			if !IsRemote(parts[1]) {
				errs = append(errs, fmt.Errorf("RULE-SET requires an HTTP(S) URL: %s", DisplaySource(parts[1])))
				return
			}
			if hooks.FetchRuleSet == nil {
				doc.RuleSets = append(doc.RuleSets, RuleSetRef{URL: parts[1], Target: target})
				return
			}
			before := doc.TotalRules()
			if err := hooks.FetchRuleSet(parts[1], target, func(listLine string) {
				add(listLine, target)
			}); err != nil {
				errs = append(errs, err)
			} else if hooks.Report != nil {
				hooks.Report("  + RULE-SET %s: %d entries\n", path.Base(parts[1]), doc.TotalRules()-before)
			}
		} else if rt == "GEOIP" && len(parts) >= 3 {
			doc.GeoIP = append(doc.GeoIP, [2]string{strings.ToUpper(parts[1]), normTarget(parts[2])})
		} else if rt == "FINAL" && len(parts) >= 2 {
			doc.Final = parts[1]
		}
	}

	section := ""
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(line[1 : len(line)-1])
			continue
		}
		switch section {
		case "general":
			if k, v, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") {
				k, v = strings.TrimSpace(k), strings.TrimSpace(v)
				if k == "include" && v != "" {
					includes = append(includes, splitCSV(v)...)
					continue
				}
				doc.General[k] = v
			}
		case "rule":
			add(line, "")
		case "url rewrite":
			if line != "" && !strings.HasPrefix(line, "#") {
				doc.Rewrites = append(doc.Rewrites, line)
			}
		case "mitm":
			if line != "" && !strings.HasPrefix(line, "#") {
				doc.Mitm = append(doc.Mitm, line)
			}
		case "proxy":
			if line != "" && !strings.HasPrefix(line, "#") {
				doc.Proxies = append(doc.Proxies, line)
			}
		case "proxy group":
			if line != "" && !strings.HasPrefix(line, "#") {
				doc.PGroups = append(doc.PGroups, line)
			}
		case "host":
			if name, ip, ok := strings.Cut(line, "="); ok {
				name, ip = strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(ip)
				if name != "" && net.ParseIP(ip) != nil {
					doc.Hosts[name] = ip
				} else if line != "" {
					doc.Scripts = append(doc.Scripts, "Host: "+line)
				}
			}
		case "script", "script-url":
			if line != "" && !strings.HasPrefix(line, "#") {
				doc.Scripts = append(doc.Scripts, line)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return nil, errs[0]
	}
	doc.Includes = includes
	return doc, nil
}

// splitCSV splits a comma-separated general value, trimming space and
// dropping empties.
func splitCSV(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}
