// Package gen converts Shadowrocket conf rules and share links to sing-box 1.14.
package gen

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/pi-dal/sakamoto/pkg/mobileconf"
)

// buckets is the shared parsed-rule store; the parse implementation lives in
// pkg/mobileconf so the iOS app validates with the exact same semantics.
type buckets = mobileconf.Buckets

const maxConfBytes = 16 << 20 // Allow large ad lists, but reject unbounded responses.
var reportMu sync.Mutex
var reportOutput io.Writer = os.Stdout

func reportf(format string, args ...any) { _, _ = fmt.Fprintf(reportOutput, format, args...) }

var errorURL = regexp.MustCompile(`https?://[^\s"']+`)

func errString(err error) string {
	if err == nil {
		return ""
	}
	return errorURL.ReplaceAllStringFunc(err.Error(), displaySource)
}

// isRemote and displaySource are thin delegates: the implementation is
// shared with the iOS importer via pkg/mobileconf.
func isRemote(source string) bool        { return mobileconf.IsRemote(source) }
func displaySource(source string) string { return mobileconf.DisplaySource(source) }

// ValidSource accepts a local .conf path or an HTTP(S) URL. Other schemes are rejected.
func ValidSource(source string) error { return mobileconf.ValidSource(source) }

func resolveInclude(parent, inc string) (string, error) {
	if err := ValidSource(inc); err != nil {
		return "", err
	}
	if isRemote(inc) {
		return inc, nil
	}
	if isRemote(parent) {
		base, _ := url.Parse(parent)
		ref, err := url.Parse(inc)
		if err != nil {
			return "", err
		}
		return base.ResolveReference(ref).String(), nil
	}
	return filepath.Join(filepath.Dir(parent), inc), nil
}
func readConfSource(source string, hc *http.Client) ([]byte, error) {
	if err := ValidSource(source); err != nil {
		return nil, err
	}
	var reader io.ReadCloser
	if isRemote(source) {
		resp, err := hc.Get(source)
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %s", displaySource(source), errString(err))
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("fetch %s: HTTP %d", displaySource(source), resp.StatusCode)
		}
		reader = resp.Body
	} else {
		f, err := os.Open(source)
		if err != nil {
			return nil, err
		}
		reader = f
	}
	defer func() { _ = reader.Close() }()
	body, err := io.ReadAll(io.LimitReader(reader, maxConfBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxConfBytes {
		return nil, fmt.Errorf("configuration exceeds the %d MiB limit", maxConfBytes>>20)
	}
	body = []byte(strings.TrimPrefix(string(body), "\ufeff"))
	if !strings.Contains(string(body), "[General]") && !strings.Contains(string(body), "[Rule]") {
		return nil, fmt.Errorf("%s is not a Shadowrocket conf (missing [General]/[Rule])", displaySource(source))
	}
	return body, nil
}

// parsedConf is one parsed conf document plus the host-side raw copy for the
// imports cache. Parsing itself lives in pkg/mobileconf (shared with the iOS
// app); this struct keeps the field names the rest of the generator consumes.
type parsedConf struct {
	bk      buckets
	general map[string]string
	hosts   map[string]string
	rawRoot string
	geoip   [][2]string // (cc, target)
	final   string
	// Unsupported sections retained only for the migration report.
	rewrites []string // [URL Rewrite]
	mitm     []string // [MITM]
	proxies  []string // [Proxy] entries reported but not imported as nodes.
	pgroups  []string // [Proxy Group] entries replaced by sakamoto groups.
	scripts  []string // [Script]/[Host]
}

// ruleSetFetcher supplies pkg/mobileconf with the host-only RULE-SET fetch:
// HTTP(S) lists are downloaded with the shared size limit and fed back line
// by line. Error wording matches the historical importer messages.
func ruleSetFetcher(hc *http.Client) func(source, target string, add func(line string)) error {
	return func(source, target string, add func(line string)) error {
		resp, err := hc.Get(source)
		if err != nil {
			return fmt.Errorf("fetch RULE-SET %s: %v", displaySource(source), errString(err))
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("RULE-SET %s: HTTP %d", displaySource(source), resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxConfBytes+1))
		if err != nil || len(body) > maxConfBytes {
			return fmt.Errorf("RULE-SET %s could not be read or exceeded the size limit: %v", displaySource(source), errString(err))
		}
		sc := bufio.NewScanner(strings.NewReader(string(body)))
		for sc.Scan() {
			add(sc.Text())
		}
		if err := sc.Err(); err != nil {
			return fmt.Errorf("RULE-SET %s: %v", displaySource(source), errString(err))
		}
		return nil
	}
}

func parseConf(path string, hc *http.Client) (*parsedConf, error) {
	return parseConfRec(path, hc, map[string]bool{})
}

var mergedGeneralKeys = map[string]bool{
	"tun-excluded-routes": true, "bypass-tun": true, "skip-proxy": true,
	"dns-server": true, "fallback-dns-server": true, "always-real-ip": true,
}

func joinUniqueCSV(primary, inherited string) string {
	seen := map[string]bool{}
	var result []string
	for _, item := range append(splitCSV(primary), splitCSV(inherited)...) {
		key := strings.ToLower(item)
		if !seen[key] {
			seen[key] = true
			result = append(result, item)
		}
	}
	return strings.Join(result, ",")
}

func parseConfRec(path string, hc *http.Client, seen map[string]bool) (*parsedConf, error) {
	if len(seen) >= 8 {
		return nil, fmt.Errorf("configuration include depth exceeds 8 levels")
	}
	if !isRemote(path) {
		var err error
		path, err = filepath.Abs(path)
		if err != nil {
			return nil, err
		}
	}
	if seen[path] {
		return nil, fmt.Errorf("configuration include cycle: %s", path)
	}
	seen[path] = true
	defer delete(seen, path)
	body, err := readConfSource(path, hc)
	if err != nil {
		return nil, err
	}
	doc, err := mobileconf.ParseDocument(string(body), mobileconf.Hooks{
		FetchRuleSet: ruleSetFetcher(hc),
		Report:       reportf,
	})
	if err != nil {
		return nil, err
	}
	p := &parsedConf{
		bk: doc.Buckets, general: doc.General, hosts: doc.Hosts,
		geoip: doc.GeoIP, final: doc.Final,
		rewrites: doc.Rewrites, mitm: doc.Mitm, proxies: doc.Proxies,
		pgroups: doc.PGroups, scripts: doc.Scripts,
		rawRoot: string(body),
	}
	// Resolve relative includes against their URL or local parent directory.
	for _, inc := range doc.Includes {
		includePath, err := resolveInclude(path, inc)
		if err != nil {
			return nil, fmt.Errorf("invalid include %q: %w", inc, err)
		}
		sub, err := parseConfRec(includePath, hc, seen)
		if err != nil {
			return nil, fmt.Errorf("cannot merge include %s: %v (keep relative rule files alongside the conf)", displaySource(inc), errString(err))
		}
		reportf("  + include: %s\n", inc)
		for tgt, m := range sub.bk {
			for k, s := range m {
				if p.bk[tgt] == nil {
					p.bk[tgt] = map[string]map[string]bool{}
				}
				if p.bk[tgt][k] == nil {
					p.bk[tgt][k] = map[string]bool{}
				}
				for it := range s {
					p.bk[tgt][k][it] = true
				}
			}
		}
		for k, v := range sub.general {
			if current, ok := p.general[k]; !ok {
				p.general[k] = v
			} else if mergedGeneralKeys[k] {
				p.general[k] = joinUniqueCSV(current, v)
			}
		}
		for name, ip := range sub.hosts {
			if _, ok := p.hosts[name]; !ok {
				p.hosts[name] = ip
			}
		}
		p.geoip = append(p.geoip, sub.geoip...)
		if p.final == "" {
			p.final = sub.final
		}
		p.rewrites = append(p.rewrites, sub.rewrites...)
		p.mitm = append(p.mitm, sub.mitm...)
		p.proxies = append(p.proxies, sub.proxies...)
		p.pgroups = append(p.pgroups, sub.pgroups...)
	}
	return p, nil
}
