// Package gen — Shadowrocket conf/节点 → sing-box 1.14 配置生成器（sr2sb 的 Go 版）。
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
)

// ---------------- SR conf 解析 ----------------

type buckets map[string]map[string]map[string]bool // target → ruletype → items

var ruleTypes = map[string]string{
	"DOMAIN": "domain", "DOMAIN-SUFFIX": "domain_suffix",
	"DOMAIN-KEYWORD": "domain_keyword", "IP-CIDR": "ip_cidr", "IP-CIDR6": "ip_cidr",
	"IP-ASN": "", // 无等价物，跳过
}

var commentRe = regexp.MustCompile(`\s//`) // 只剥 "\s//" 注释，不破坏 https://

const maxConfBytes = 16 << 20 // 允许大型广告规则，但拒绝意外的无限响应
var reportMu sync.Mutex
var reportOutput io.Writer = os.Stdout

func reportf(format string, args ...any) { _, _ = fmt.Fprintf(reportOutput, format, args...) }

// ValidSource accepts a local .conf path or an HTTP(S) URL. Other schemes are rejected.
func ValidSource(source string) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return fmt.Errorf("请输入 Shadowrocket .conf 地址或本地路径")
	}
	u, err := url.Parse(source)
	if err != nil {
		return err
	}
	if strings.Contains(source, "://") && u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("只支持 HTTP(S) 地址或本地路径")
	}
	if (u.Scheme == "http" || u.Scheme == "https") && u.Host == "" {
		return fmt.Errorf("URL 缺少主机名")
	}
	return nil
}
func isRemote(source string) bool {
	u, err := url.Parse(source)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https")
}
func displaySource(source string) string {
	if !isRemote(source) {
		return source
	}
	u, _ := url.Parse(source)
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	if u.Path != "" && u.Path != "/" {
		u.Path = "/…"
		u.RawPath = ""
	}
	return u.String()
}

var errorURL = regexp.MustCompile(`https?://[^\s"']+`)

func errString(err error) string {
	if err == nil {
		return ""
	}
	return errorURL.ReplaceAllStringFunc(err.Error(), displaySource)
}
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
			return nil, fmt.Errorf("拉取 %s: %s", displaySource(source), errString(err))
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("拉取 %s: HTTP %d", displaySource(source), resp.StatusCode)
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
		return nil, fmt.Errorf("配置超过 %d MiB 上限", maxConfBytes>>20)
	}
	body = []byte(strings.TrimPrefix(string(body), "\ufeff"))
	if !strings.Contains(string(body), "[General]") && !strings.Contains(string(body), "[Rule]") {
		return nil, fmt.Errorf("%s 不是 Shadowrocket conf（缺少 [General]/[Rule]）", displaySource(source))
	}
	return body, nil
}

func normTarget(t string) string {
	switch strings.ToUpper(strings.TrimSpace(t)) {
	case "REJECT", "REJECT-DROP", "REJECT-NO-DROP":
		return "reject"
	case "DIRECT", "TAILSCALE":
		return "direct" // TAILSCALE：桌面端 Tailscale 自己接管，不代进代理
	default:
		return "proxy"
	}
}

type parsedConf struct {
	bk      buckets
	general map[string]string
	rawRoot string
	errors  []error
	geoip   [][2]string // (cc, target)
	final   string
	// 不支持、仅收集用于报告的段
	rewrites []string // [URL Rewrite]
	mitm     []string // [MITM]
	proxies  []string // [Proxy]（节点不走这里，仅报告）
	pgroups  []string // [Proxy Group]（组架构已由 sakamoto 接管）
	scripts  []string // [Script]/[Host]
}

func (p *parsedConf) add(line, defaultTarget string, hc *http.Client) {
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
		if p.bk[normTarget(tgt)] == nil {
			p.bk[normTarget(tgt)] = map[string]map[string]bool{}
		}
		if p.bk[normTarget(tgt)][key] == nil {
			p.bk[normTarget(tgt)][key] = map[string]bool{}
		}
		p.bk[normTarget(tgt)][key][parts[1]] = true
	} else if rt == "RULE-SET" && len(parts) >= 3 {
		p.fetchList(parts[1], parts[2], hc)
	} else if rt == "GEOIP" && len(parts) >= 3 {
		p.geoip = append(p.geoip, [2]string{strings.ToUpper(parts[1]), normTarget(parts[2])})
	} else if rt == "FINAL" && len(parts) >= 2 {
		p.final = parts[1]
	}
}

func (p *parsedConf) fetchList(source, target string, hc *http.Client) {
	if !isRemote(source) {
		p.errors = append(p.errors, fmt.Errorf("RULE-SET 需要 HTTP(S) 地址: %s", displaySource(source)))
		return
	}
	resp, err := hc.Get(source)
	if err != nil {
		p.errors = append(p.errors, fmt.Errorf("拉取 RULE-SET %s: %v", displaySource(source), errString(err)))
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		p.errors = append(p.errors, fmt.Errorf("RULE-SET %s: HTTP %d", displaySource(source), resp.StatusCode))
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxConfBytes+1))
	if err != nil || len(body) > maxConfBytes {
		p.errors = append(p.errors, fmt.Errorf("RULE-SET %s 读取失败或超过大小限制: %v", displaySource(source), errString(err)))
		return
	}
	n0 := p.count()
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	for sc.Scan() {
		p.add(sc.Text(), target, hc)
	}
	if err := sc.Err(); err != nil {
		p.errors = append(p.errors, fmt.Errorf("RULE-SET %s: %v", displaySource(source), errString(err)))
		return
	}
	reportf("  + RULE-SET %s: %d 条\n", filepath.Base(source), p.count()-n0)
}

func (p *parsedConf) count() int {
	n := 0
	for _, m := range p.bk {
		for _, s := range m {
			n += len(s)
		}
	}
	return n
}

func parseConf(path string, hc *http.Client) (*parsedConf, error) {
	return parseConfRec(path, hc, map[string]bool{})
}

func parseConfRec(path string, hc *http.Client, seen map[string]bool) (*parsedConf, error) {
	if len(seen) >= 8 {
		return nil, fmt.Errorf("配置 include 超过 8 层，已停止递归")
	}
	if !isRemote(path) {
		var err error
		path, err = filepath.Abs(path)
		if err != nil {
			return nil, err
		}
	}
	if seen[path] {
		return nil, fmt.Errorf("配置 include 循环: %s", path)
	}
	seen[path] = true
	defer delete(seen, path)
	body, err := readConfSource(path, hc)
	if err != nil {
		return nil, err
	}
	p := &parsedConf{bk: buckets{}, general: map[string]string{}, rawRoot: string(body)}
	section := ""
	var includes []string
	sc := bufio.NewScanner(strings.NewReader(string(body)))
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
				p.general[k] = v
			}
		case "rule":
			p.add(line, "", hc)
		case "url rewrite":
			if line != "" && !strings.HasPrefix(line, "#") {
				p.rewrites = append(p.rewrites, line)
			}
		case "mitm":
			if line != "" && !strings.HasPrefix(line, "#") {
				p.mitm = append(p.mitm, line)
			}
		case "proxy":
			if line != "" && !strings.HasPrefix(line, "#") {
				p.proxies = append(p.proxies, line)
			}
		case "proxy group":
			if line != "" && !strings.HasPrefix(line, "#") {
				p.pgroups = append(p.pgroups, line)
			}
		case "script", "script-url", "host":
			if line != "" && !strings.HasPrefix(line, "#") {
				p.scripts = append(p.scripts, line) // Script/Host 无等价，报告用
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(p.errors) > 0 {
		return nil, p.errors[0]
	}
	// include 递归：URL 源按地址解析相对路径，本地源按文件目录解析。
	for _, inc := range includes {
		includePath, err := resolveInclude(path, inc)
		if err != nil {
			return nil, fmt.Errorf("非法 include %q: %w", inc, err)
		}
		sub, err := parseConfRec(includePath, hc, seen)
		if err != nil {
			return nil, fmt.Errorf("无法合并 include %s: %v（请将广告规则 conf 导出到同一目录）", displaySource(inc), errString(err))
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
			if _, ok := p.general[k]; !ok {
				p.general[k] = v // 本文件优先
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
