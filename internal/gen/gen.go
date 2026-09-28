// Package gen — Shadowrocket conf/节点 → sing-box 1.14 配置生成器（sr2sb 的 Go 版）。
package gen

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"gopkg.in/yaml.v3"

	"github.com/pi-dal/sakamoto/internal/config"
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

func reportf(format string, args ...any) { fmt.Fprintf(reportOutput, format, args...) }

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
			resp.Body.Close()
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
	defer reader.Close()
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
	defer resp.Body.Close()
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

// ---------------- 节点 ----------------

func splitPortRange(s string) (ports []string, single int) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "-") {
		ab := strings.SplitN(s, "-", 2)
		return []string{fmt.Sprintf("%s:%s", ab[0], ab[1])}, 0
	}
	fmt.Sscanf(s, "%d", &single)
	return nil, single
}

// SRNode 是 Shadowrocket.json 的单条目（字段宽松解析）。
type SRNode map[string]any

func sstr(n SRNode, k string) string {
	if v, ok := n[k].(string); ok {
		return v
	}
	return ""
}

func srJSONNodes(path string, allowHosts map[string]bool) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var arr []SRNode
	if err := json.NewDecoder(f).Decode(&arr); err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, n := range arr {
		host := sstr(n, "host")
		if !allowHosts[host] || sstr(n, "type") == "Subscribe" {
			continue
		}
		switch strings.ToLower(sstr(n, "type")) {
		case "hysteria2":
			node := map[string]any{"type": "hysteria2", "tag": sstr(n, "title"),
				"server": host, "password": sstr(n, "password"),
				"tls": map[string]any{"enabled": true}}
			if ps, single := splitPortRange(sstr(n, "port")); len(ps) > 0 {
				node["server_ports"] = ps
			} else {
				node["server_port"] = single
			}
			if peer := sstr(n, "peer"); peer != "" {
				node["tls"].(map[string]any)["server_name"] = peer
			}
			out = append(out, node)
		case "socks5", "socks":
			node := map[string]any{"type": "socks", "tag": orEmpty(sstr(n, "title"), "Socks"),
				"server": host, "version": "5"}
			if _, single := splitPortRange(sstr(n, "port")); single > 0 {
				node["server_port"] = single
			}
			if u := sstr(n, "user"); u != "" {
				node["username"], node["password"] = u, sstr(n, "password")
			}
			out = append(out, node)
		case "vmess":
			node := map[string]any{"type": "vmess", "tag": orEmpty(sstr(n, "title"), host),
				"server": host, "uuid": sstr(n, "uuid"),
				"security": orEmpty(sstr(n, "method"), "auto")}
			if _, single := splitPortRange(sstr(n, "port")); single > 0 {
				node["server_port"] = single
			}
			out = append(out, node)
		}
	}
	return out, nil
}

func orEmpty(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func isReality(n map[string]any) bool {
	tls, _ := n["tls"].(map[string]any)
	re, _ := tls["reality"].(map[string]any)
	b, _ := re["enabled"].(bool)
	return b
}

// ---------------- 主流程 ----------------

type Options struct {
	ConfPath   string
	SRJSONPath string
	NodesFile  string // 分享链接文件（可选）
	AllowHosts string // 手动节点 host 白名单，逗号分隔
	Cfg        *config.Config
	OutDir     string // 输出目录（默认 ~/.config/sakamoto）
	Quiet      bool   // TUI 调用时静默，避免生成过程写 stdout 破坏终端布局
}

func Run(o Options) error {
	reportMu.Lock()
	defer reportMu.Unlock()
	if o.Quiet {
		reportOutput = io.Discard
	} else {
		reportOutput = os.Stdout
	}
	defer func() { reportOutput = os.Stdout }()
	cfg := o.Cfg
	if cfg == nil {
		cfg = config.Default()
	}
	out := o.OutDir
	if out == "" {
		out = config.DefaultDir()
	}
	rulesDir := filepath.Join(out, "rules")
	os.MkdirAll(rulesDir, 0o755)

	hc := &http.Client{Timeout: 30 * time.Second} // 默认走 env HTTP(S)_PROXY

	p, err := parseConf(o.ConfPath, hc)
	if err != nil {
		return err
	}
	if isRemote(o.ConfPath) {
		cache := filepath.Join(out, "imports")
		if err := os.MkdirAll(cache, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(cache, "macOS.conf"), []byte(p.rawRoot), 0600); err != nil {
			return err
		}
	}

	// 0) skip-proxy → direct 桶（必须在编译前注入：域名+网段都是「不走代理」）
	for _, it := range splitCSV(p.general["skip-proxy"]) {
		switch {
		case strings.HasPrefix(it, "*."):
			bkAdd(p.bk, "direct", "domain_suffix", it[2:])
		case strings.Contains(it, "/"):
			bkAdd(p.bk, "direct", "ip_cidr", it)
		case net.ParseIP(it) != nil:
			bkAdd(p.bk, "direct", "ip_cidr", it+"/32")
		default:
			bkAdd(p.bk, "direct", "domain", it) // localhost / captive.apple.com 等
		}
	}

	// 1) 规则集：3 桶 → srs（原生编译，无需 exec sing-box）
	var srsEntries []map[string]any
	for _, b := range []string{"reject", "proxy", "direct"} {
		m := p.bk[b]
		if len(m) == 0 {
			continue
		}
		hr := option.DefaultHeadlessRule{}
		for _, k := range []string{"domain", "domain_suffix", "domain_keyword", "ip_cidr"} {
			if len(m[k]) == 0 {
				continue
			}
			items := make([]string, 0, len(m[k]))
			for it := range m[k] {
				items = append(items, it)
			}
			sort.Strings(items)
			switch k {
			case "domain":
				hr.Domain = items
			case "domain_suffix":
				hr.DomainSuffix = items
			case "domain_keyword":
				hr.DomainKeyword = items
			case "ip_cidr":
				hr.IPCIDR = items
			}
		}
		srsPath := filepath.Join(rulesDir, b+".srs")
		f, err := os.Create(srsPath)
		if err != nil {
			return err
		}
		err = srs.Write(f, option.PlainRuleSet{
			Rules: []option.HeadlessRule{{Type: "default", DefaultOptions: hr}},
		}, C.RuleSetVersion3)
		f.Close()
		if err != nil {
			return fmt.Errorf("compile rs-%s: %w", b, err)
		}
		srsEntries = append(srsEntries, map[string]any{
			"type": "local", "tag": "rs-" + b, "format": "binary", "path": srsPath})
		reportf("  rs-%s: %d 条\n", b, len(m["domain"])+len(m["domain_suffix"])+len(m["domain_keyword"])+len(m["ip_cidr"]))

		// 再出一个纯域名版（DNS 规则查询期匹配用；1.14 拒 dns rule 含 ip_cidr 且未 match_response）
		if len(m["ip_cidr"]) > 0 {
			hrDom := option.DefaultHeadlessRule{
				Domain: hr.Domain, DomainSuffix: hr.DomainSuffix, DomainKeyword: hr.DomainKeyword}
			p2 := filepath.Join(rulesDir, b+"-domains.srs")
			f2, _ := os.Create(p2)
			if err := srs.Write(f2, option.PlainRuleSet{
				Rules: []option.HeadlessRule{{Type: "default", DefaultOptions: hrDom}},
			}, C.RuleSetVersion3); err != nil {
				f2.Close()
				return err
			}
			f2.Close()
			srsEntries = append(srsEntries, map[string]any{
				"type": "local", "tag": "rs-" + b + "-domains", "format": "binary", "path": p2})
		}
	}

	// 2) GeoIP → 本地 .srs
	for _, gp := range p.geoip {
		cc, tgt := gp[0], gp[1]
		srsPath := filepath.Join(rulesDir, "geoip-"+strings.ToLower(cc)+".srs")
		if _, err := os.Stat(srsPath); os.IsNotExist(err) {
			url := fmt.Sprintf("https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geoip/%s.srs", strings.ToLower(cc))
			resp, err := hc.Get(url)
			if err != nil || resp.StatusCode != 200 {
				reportf("  ! geoip-%s 下载失败(跳过): %v\n", cc, err)
				continue
			}
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			os.WriteFile(srsPath, data, 0o644)
			reportf("  geoip-%s.srs 下载完成\n", strings.ToLower(cc))
		}
		srsEntries = append(srsEntries, map[string]any{
			"type": "local", "tag": "geoip-" + strings.ToLower(cc),
			"format": "binary", "path": srsPath})
		_ = tgt
	}

	// 3) 节点
	allow := map[string]bool{}
	for _, h := range strings.Split(o.AllowHosts, ",") {
		allow[strings.TrimSpace(h)] = true
	}
	// 3) 节点：手动（srjson 白名单 + nodes.txt）优先，订阅兜底；tag 去重
	nodes, err := srJSONNodes(o.SRJSONPath, allow)
	if err != nil {
		reportf("  ! srjson 读取失败(跳过): %v\n", err)
	}
	nodes = append(nodes, parseShareLinks(o.NodesFile)...)
	for _, s := range o.Cfg.Subscriptions {
		nodes = append(nodes, fetchSub(s, hc)...)
	}
	seen := map[string]bool{}
	dedup := nodes[:0]
	for _, n := range nodes {
		t, _ := n["tag"].(string)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		dedup = append(dedup, n)
	}
	nodes = dedup
	var tags []string
	for _, n := range nodes {
		tags = append(tags, n["tag"].(string))
	}
	reportf("  节点: %v\n", tags)

	// uTLS 指纹全局覆盖（对齐 SR Fingerprint=Safari15_5 全局生效）
	if cfg.UTLSFingerprint != "" {
		for _, n := range nodes {
			if tls, ok := n["tls"].(map[string]any); ok && tls["enabled"] == true {
				tls["utls"] = map[string]any{"enabled": true, "fingerprint": cfg.UTLSFingerprint}
			}
		}
	}

	// 4) 分组：RealityAuto(urltest reality) + OthersAuto(urltest 其他) + ManualPick(selector)
	var dataNodes, socksNodes []map[string]any
	for _, n := range nodes {
		if n["type"] == "socks" {
			socksNodes = append(socksNodes, n)
		} else {
			dataNodes = append(dataNodes, n)
		}
	}
	var realityTags, otherTags []string
	for _, n := range dataNodes {
		if isReality(n) || strings.Contains(strings.ToLower(n["tag"].(string)), "reality") {
			realityTags = append(realityTags, n["tag"].(string)) // reality 协议 或 名字含 reality
		} else {
			otherTags = append(otherTags, n["tag"].(string))
		}
	}
	urltest := func(tag string, members []string) map[string]any {
		return map[string]any{"type": "urltest", "tag": tag, "outbounds": members,
			"url": cfg.URLTest.URL, "interval": cfg.URLTest.Interval, "tolerance": cfg.URLTest.Tolerance}
	}
	var groups []map[string]any
	var mainMembers []string
	if len(realityTags) > 0 {
		groups = append(groups, urltest("RealityAuto", realityTags))
		mainMembers = append(mainMembers, "RealityAuto")
	}
	if len(otherTags) > 1 {
		groups = append(groups, urltest("OthersAuto", otherTags))
		mainMembers = append(mainMembers, "OthersAuto")
	}
	manual := make([]string, 0, len(dataNodes))
	for _, n := range dataNodes {
		manual = append(manual, n["tag"].(string))
	}
	if len(manual) == 0 {
		manual = []string{"direct"} // 无数据节点时的占位，等订阅/链接进来后重跑 gen
	}
	groups = append(groups, map[string]any{"type": "selector", "tag": "ManualPick", "outbounds": manual})
	mainMembers = append(mainMembers, "ManualPick")
	main := map[string]any{"type": "selector", "tag": "MainProxy", "outbounds": mainMembers,
		"default": mainMembers[0], "interrupt_exist_connections": true}
	// socks = 全局链式出口：detour→MainProxy（chain_enabled=false 时不挂链，流量直连节点）
	for _, n := range nodes {
		delete(n, "_chain")
		if n["type"] == "socks" && cfg.ChainEnabled {
			n["detour"] = "MainProxy"
		}
	}
	outbounds := append([]map[string]any{main}, groups...)
	outbounds = append(outbounds, nodes...)
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "direct"})
	exitTag := "MainProxy"
	if cfg.ChainEnabled {
		for _, s := range socksNodes { // 取 detour=MainProxy 的 socks 作链式出口
			if s["detour"] == "MainProxy" {
				exitTag = s["tag"].(string)
				break
			}
			exitTag = s["tag"].(string)
		}
	}

	// 5) Tailscale 自动检测与优化
	ts := tailscaleInfo{}
	if cfg.TailscaleOptimize {
		ts = detectTailscale()
	}
	if ts.Present {
		reportf("  + Tailscale 检测到: MagicDNS=%s\n", ts.MagicDNSSuffix)
		// CGNAT/ULA 全量绕开 TUN（bypass 之外补一层，幂等）
		for _, c := range []string{"100.64.0.0/10", "fd7a:115c:a1e0::/48"} {
			bkAdd(p.bk, "direct", "ip_cidr", c)
		}
	}

	// 6) [General] → DNS：dns-server 逐项解析（udp / https / #proxy 后缀 / tls）
	var directSets []string
	if _, ok := p.bk["direct"]; ok {
		directSets = append(directSets, "rs-direct")
	}
	for _, gp := range p.geoip {
		if gp[1] == "direct" {
			directSets = append(directSets, "geoip-"+strings.ToLower(gp[0]))
		}
	}
	var dnsServers []map[string]any
	localTags, remoteTags := []string{}, []string{}
	localPublic := "" // 第一个公网 UDP 解析器（私网 DNS 可能随 VPN 上下线，不做默认）
	addServer := func(tag, typ, server string, detour bool) {
		s := map[string]any{"tag": tag, "type": typ, "server": server}
		if detour {
			s["detour"] = exitTag
			if localPublic != "" {
				s["domain_resolver"] = localPublic // DoH 引导走公网本地 DNS
			}
		}
		dnsServers = append(dnsServers, s)
	}
	for _, d := range splitCSV(p.general["dns-server"]) {
		viaProxy := strings.HasSuffix(d, "#proxy")
		d = strings.TrimSuffix(d, "#proxy")
		switch {
		case strings.HasPrefix(d, "https://"):
			host := strings.TrimPrefix(d, "https://")
			host = strings.SplitN(host, "/", 2)[0] // 只留 host，路径默认 /dns-query
			tag := fmt.Sprintf("remote%d", len(remoteTags))
			remoteTags = append(remoteTags, tag)
			addServer(tag, "https", host, viaProxy)
		case strings.HasPrefix(d, "tls://"):
			tag := fmt.Sprintf("remote%d", len(remoteTags))
			remoteTags = append(remoteTags, tag)
			addServer(tag, "tls", strings.TrimPrefix(d, "tls://"), viaProxy)
		default: // 纯 IP → udp 本地池
			tag := fmt.Sprintf("local%d", len(localTags))
			localTags = append(localTags, tag)
			if localPublic == "" {
				if a, err := netip.ParseAddr(strings.SplitN(d, ":", 2)[0]); err == nil && !a.IsPrivate() {
					localPublic = tag
				}
			}
			addServer(tag, "udp", d, false)
		}
	}
	for _, d := range splitCSV(p.general["fallback-dns-server"]) { // fallback 并进本地池
		tag := fmt.Sprintf("local%d", len(localTags))
		localTags = append(localTags, tag)
		if localPublic == "" {
			if a, err := netip.ParseAddr(strings.SplitN(d, ":", 2)[0]); err == nil && !a.IsPrivate() {
				localPublic = tag
			}
		}
		addServer(tag, "udp", strings.TrimPrefix(strings.TrimPrefix(d, "tls://"), "https://"), false)
	}
	if len(localTags) == 0 {
		localTags = []string{"local0"}
		localPublic = "local0"
		addServer("local0", "udp", "223.5.5.5", false)
	}
	if localPublic == "" {
		localPublic = localTags[0]
	}
	// DoH 服务器的域名解析钉到公网本地 DNS
	for _, s := range dnsServers {
		if s["detour"] != nil {
			s["domain_resolver"] = localPublic
		}
	}
	if len(remoteTags) == 0 {
		remoteTags = []string{"remote0"}
		addServer("remote0", "https", "dns.google", true)
		dnsServers[len(dnsServers)-1]["domain_resolver"] = localPublic
	}
	// 节点域名钉死本地公网 DNS（避免链式鸡生蛋）
	for _, n := range nodes {
		n["domain_resolver"] = localPublic
	}
	// DNS 规则只用纯域名规则集（ip_cidr 规则集需响应期匹配，走 private-ip-answer 那条）
	var dnsDirectSets []string
	if _, ok := p.bk["direct"]; ok {
		dnsDirectSets = []string{"rs-direct-domains"}
	}
	dnsRules := []map[string]any{}
	if ts.Present {
		// *.ts.net → Tailscale quad100（MagicDNS 设备名可解析）；必须排在通用 direct 规则前
		dnsServers = append(dnsServers, map[string]any{
			"tag": "ts-dns", "type": "udp", "server": "100.100.100.100"})
		dnsRules = append(dnsRules, map[string]any{
			"domain_suffix": []string{"ts.net"}, "server": "ts-dns"})
	}
	if len(dnsDirectSets) > 0 {
		dnsRules = append(dnsRules, map[string]any{"rule_set": dnsDirectSets, "server": localPublic})
	}
	// private-ip-answer=true：先 evaluate 远程解析，应答含私网 IP 则 reject
	if strings.EqualFold(p.general["private-ip-answer"], "true") {
		dnsRules = append(dnsRules,
			map[string]any{"action": "evaluate", "server": remoteTags[0]},
			map[string]any{"match_response": true, "ip_is_private": true, "action": "reject"})
	}

	// 7) route + tun
	routeRules := []map[string]any{
		{"action": "sniff"},
		{"protocol": "dns", "action": "hijack-dns"}, // SR hijack-dns 的超集（全局劫持，更彻底）
		{"ip_is_private": true, "action": "route", "outbound": "direct"},
		{"rule_set": []string{"rs-reject"}, "action": "reject"},
		{"rule_set": directSets, "action": "route", "outbound": "direct"},
		{"rule_set": []string{"rs-proxy"}, "action": "route", "outbound": exitTag},
	}
	if cfg.BlockQUIC {
		// 拒 UDP:443 强制回落 TCP/TLS（代理下 QUIC 走 UDP-over-TCP 白损性能）
		routeRules = append([]map[string]any{routeRules[0], routeRules[1], routeRules[2],
			{"network": "udp", "port": 443, "action": "reject"}}, routeRules[3:]...)
	}
	bypass := splitCSV(p.general["bypass-tun"])
	bypass = append(bypass, splitCSV(p.general["tun-excluded-routes"])...)
	if ts.Present {
		for _, c := range []string{"100.64.0.0/10", "fd7a:115c:a1e0::/48"} {
			if !containsStr(bypass, c) {
				bypass = append(bypass, c)
			}
		}
	}
	{ // bypass 去重保序
		seen := map[string]bool{}
		dd := bypass[:0]
		for _, x := range bypass {
			if !seen[x] {
				seen[x] = true
				dd = append(dd, x)
			}
		}
		bypass = dd
	}
	tun := map[string]any{
		"type": "tun", "tag": "tun-in", "address": []string{"172.18.0.1/30"},
		// strict_route 对齐 SR TunnelEnforceRoutesKey
		"auto_route": true, "strict_route": cfg.StrictRoute, "stack": cfg.TunStack,
		"udp_timeout": "5m",
	}
	if len(bypass) > 0 {
		tun["route_exclude_address"] = bypass
	}
	if inc := splitCSV(p.general["tun-included-routes"]); len(inc) > 0 {
		tun["route_address"] = inc // SR tun-included-routes：只接管列出的网段（Tailscale 变体）
	}
	inbounds := []map[string]any{tun}
	if cfg.MixedInbound.Enabled { // SR ProxyServerType/Port + ProxyShareEnabled
		listen := "127.0.0.1"
		if cfg.MixedInbound.AllowLAN {
			listen = "0.0.0.0"
		}
		inbounds = append(inbounds, map[string]any{
			"type": "mixed", "tag": "in-mixed",
			"listen": listen, "listen_port": cfg.MixedInbound.Port,
		})
	}

	config := map[string]any{
		"log": map[string]any{"level": cfg.LogLevel, "timestamp": true},
		"dns": map[string]any{
			"servers": dnsServers,
			"rules":   dnsRules,
			"final":   remoteTags[0], "strategy": "ipv4_only",
		},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route": map[string]any{
			"rules": routeRules,
			"final": exitTag, "auto_detect_interface": true, "rule_set": srsEntries,
			"default_domain_resolver": map[string]any{"server": localPublic, "strategy": "ipv4_only"},
		},
		"services": []map[string]any{{
			"type": "api", "listen": "127.0.0.1", "listen_port": 9090,
			"secret": cfg.API.Secret, "dashboard": map[string]any{"enabled": true},
		}},
	}
	b, _ := json.MarshalIndent(config, "", "  ")
	cfgPath := filepath.Join(out, "config.json")
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
		return err
	}

	// 6) sakamoto.yaml 侧车：只在不存在时写模板（不覆盖用户编辑）
	var chain []string
	for _, t := range []string{"RealityAuto", "OthersAuto"} {
		for _, m := range mainMembers {
			if m == t {
				chain = append(chain, t)
			}
		}
	}
	sidecarPath := filepath.Join(out, "sakamoto.yaml")
	if _, err := os.Stat(sidecarPath); os.IsNotExist(err) {
		yamlText := fmt.Sprintf("api:\n  url: http://127.0.0.1:9090\n  secret: %s\n", cfg.API.Secret) +
			"check_interval: 30s\nrecover_after: 2\ntest_settle: 5s\nfallback_enabled: true\n"
		if len(chain) > 1 {
			yamlText += "fallbacks:\n  MainProxy: [" + strings.Join(chain, ", ") + "]\n"
		}
		yamlText += "# 如需订阅，在 Config 页面添加；手动节点放在 nodes.txt。\nsubscriptions: []\n"
		os.WriteFile(sidecarPath, []byte(yamlText), 0o600)
		reportf("→ %s (模板)\n", sidecarPath)
	}

	// 8) 迁移报告：不支持项如实列出
	if len(p.rewrites) > 0 {
		reportf("  ⚠ [URL Rewrite] %d 条已丢弃（sing-box 无 HTTP 改写层）: %s\n",
			len(p.rewrites), strings.Join(p.rewrites, " | "))
	}
	if len(p.mitm) > 0 {
		reportf("  ⚠ [MITM] 已丢弃（sing-box 不支持中间人解密）: %s\n", strings.Join(p.mitm, " | "))
	}
	if len(p.proxies) > 0 {
		reportf("  · [Proxy] %d 行未导入（节点由 nodes.txt/订阅提供）\n", len(p.proxies))
	}
	if len(p.pgroups) > 0 {
		reportf("  · [Proxy Group] %d 行 → 已由 RealityAuto/OthersAuto/ManualPick/MainProxy 替代\n", len(p.pgroups))
	}
	if len(p.scripts) > 0 {
		reportf("  ⚠ [Script]/[Host] %d 行已丢弃（sing-box 无脚本引擎/hosts 覆写）\n", len(p.scripts))
	}
	// [General] 未知/未覆盖键如实报告，保证审计完整性
	handled := map[string]bool{"ipv6": true, "prefer-ipv6": true, "bypass-system": true,
		"bypass-tun": true, "skip-proxy": true, "dns-server": true,
		"fallback-dns-server": true, "private-ip-answer": true, "dns-direct-system": true,
		"dns-direct-fallback-proxy": true, "icmp-auto-reply": true,
		"always-reject-url-rewrite": true, "hijack-dns": true,
		"tun-included-routes": true, "tun-excluded-routes": true,
		"always-real-ip": true}
	for k, v := range p.general {
		if !handled[k] {
			reportf("  ? [General] 未映射键: %s = %s\n", k, v)
		}
	}
	reportf("→ %s  出口链: 流量→%s→detour→MainProxy→节点\n", cfgPath, exitTag)
	reportf("   fallback 链: %v\n", chain)
	return nil
}

// detectTailscale 检测本机 Tailscale 安装/运行状态与 MagicDNS 后缀。
type tailscaleInfo struct {
	Present        bool
	MagicDNSSuffix string
}

func detectTailscale() tailscaleInfo {
	var ts tailscaleInfo
	_, errApp := os.Stat("/Applications/Tailscale.app")
	cli, errCli := exec.LookPath("tailscale")
	if errCli != nil {
		if b, err := os.ReadDir("/Applications/Tailscale.app/Contents/MacOS"); err == nil && len(b) > 0 {
			cli = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
		}
	}
	ts.Present = errApp == nil || cli != ""
	if cli != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, cli, "status", "--json").Output(); err == nil {
			var st struct {
				MagicDNSSuffix string `json:"MagicDNSSuffix"`
				BackendState   string `json:"BackendState"`
			}
			if json.Unmarshal(out, &st) == nil {
				ts.MagicDNSSuffix = st.MagicDNSSuffix
				if st.BackendState == "Running" {
					ts.Present = true
				}
			}
		}
	}
	return ts
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func bkAdd(bk buckets, tgt, kind, val string) {
	if bk[tgt] == nil {
		bk[tgt] = map[string]map[string]bool{}
	}
	if bk[tgt][kind] == nil {
		bk[tgt][kind] = map[string]bool{}
	}
	bk[tgt][kind][val] = true
}

// parseShareLinks 解析 nodes.txt 分享链接（vless+reality 自动归类 RealityAuto）。
func parseShareLinks(path string) []map[string]any {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return parseShareLines(lines)
}

// parseShareLines 逐行解析分享链接；兼容标准 URI 与 Shadowrocket 导出格式
// （authority 段为 base64(userinfo@host:port)，tag 用 remarks= 参数，chain= → detour）。
func parseShareLines(lines []string) []map[string]any {
	var out []map[string]any
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil {
			continue
		}
		sch := strings.ToLower(u.Scheme)
		// SR 格式：vmess://vless://socks:// 的 host 段整个是 base64；
		// 重解析会丢 query，先把原始 query/fragment 存下来
		origQ, origFrag := u.RawQuery, u.Fragment
		if (sch == "vmess" || sch == "vless" || sch == "socks" || sch == "socks5") && u.User == nil && u.Host != "" {
			if dec, err := b64decode(u.Host); err == nil && strings.Contains(string(dec), "@") {
				if u2, err := url.Parse(sch + "://" + string(dec)); err == nil {
					u = u2 // 重解析出 user/host/port
				}
			}
		}
		q, _ := url.ParseQuery(origQ)
		tag, _ := url.PathUnescape(origFrag)
		if tag == "" {
			tag, _ = url.PathUnescape(q.Get("remarks")) // SR 用 remarks= 不用 #fragment
		}
		if tag == "" {
			tag = u.Hostname()
		}
		chain := strings.ToUpper(q.Get("chain")) // SR 链式参数
		if u.User == nil && sch != "vmess" && sch != "sub" {
			continue
		}
		var node map[string]any
		switch sch {
		case "hysteria2", "hy2":
			node = map[string]any{"type": "hysteria2", "tag": tag, "server": u.Hostname(),
				"password": u.User.Username(), "tls": map[string]any{"enabled": true}}
			if mp := q.Get("mport"); mp != "" {
				var ps []string
				for _, r := range strings.Split(mp, ",") {
					ps = append(ps, strings.Replace(r, "-", ":", 1))
				}
				node["server_ports"] = ps
			} else if p := u.Port(); p != "" {
				node["server_port"], _ = strconv.Atoi(p)
			}
			if sni := orEmpty(q.Get("peer"), q.Get("sni")); sni != "" {
				node["tls"].(map[string]any)["server_name"] = sni
			}
		case "vless":
			// 标准 VLESS 为 uuid@host；Shadowrocket 导出的是 method:uuid@host
			// （method 常为 none）。只要有 password 段，它才是真正的凭据。
			uuid := u.User.Username()
			if password, ok := u.User.Password(); ok && password != "" {
				uuid = password
			}
			node = map[string]any{"type": "vless", "tag": tag, "server": u.Hostname(),
				"server_port": atoi(u.Port(), 443), "uuid": uuid}
			if q.Get("xtls") == "2" {
				node["flow"] = "xtls-rprx-vision"
			} else if fl := q.Get("flow"); fl != "" {
				node["flow"] = fl
			}
			tls := map[string]any{"enabled": true,
				"server_name": orEmpty(q.Get("peer"), orEmpty(q.Get("sni"), u.Hostname()))}
			if q.Get("security") == "reality" || q.Get("pbk") != "" {
				tls["reality"] = map[string]any{"enabled": true,
					"public_key": cleanKey(q.Get("pbk")), "short_id": cleanKey(q.Get("sid"))}
				tls["utls"] = map[string]any{"enabled": true,
					"fingerprint": orEmpty(q.Get("fp"), orEmpty(q.Get("fingerprint"), "chrome"))}
			}
			node["tls"] = tls
			if q.Get("type") == "ws" {
				node["transport"] = map[string]any{"type": "ws", "path": orEmpty(q.Get("path"), "/"),
					"headers": map[string]any{"Host": q.Get("host")}}
			}
		case "trojan":
			pw, _ := u.User.Password()
			node = map[string]any{"type": "trojan", "tag": tag,
				"server": u.Hostname(), "server_port": atoi(u.Port(), 443), "password": pw,
				"tls": map[string]any{"enabled": true, "server_name": orEmpty(q.Get("sni"), u.Hostname())}}
		case "vmess":
			// 先试标准 JSON 格式，失败退 SR base64(user:uuid@host:port)
			var m map[string]any
			if dec, err := b64decode(strings.TrimPrefix(line, "vmess://")); err == nil && json.Unmarshal(dec, &m) == nil && str(m, "add") != "" {
				node = map[string]any{"type": "vmess", "tag": orEmpty(str(m, "ps"), str(m, "add")),
					"server": str(m, "add"), "server_port": atoi(str(m, "port"), 443),
					"uuid": str(m, "id"), "security": orEmpty(str(m, "scy"), "auto"),
					"alter_id": atoi(str(m, "aid"), 0)}
			} else if u.Host != "" {
				node = map[string]any{"type": "vmess", "tag": tag, "server": u.Hostname(),
					"server_port": atoi(u.Port(), 443), "uuid": u.User.Username(),
					"security": "auto", "alter_id": atoi(q.Get("alterId"), 0)}
				if pw, _ := u.User.Password(); pw != "" {
					node["uuid"] = pw // SR: userinfo=method:uuid
					node["security"] = orEmpty(u.User.Username(), "auto")
				}
			}
		case "tuic":
			pw, _ := u.User.Password()
			node = map[string]any{"type": "tuic", "tag": tag,
				"server": u.Hostname(), "server_port": atoi(u.Port(), 443),
				"uuid": u.User.Username(), "password": pw,
				"tls": map[string]any{"enabled": true,
					"server_name": orEmpty(q.Get("sni"), orEmpty(q.Get("peer"), u.Hostname()))}}
			if cc := q.Get("congestion_control"); cc != "" {
				node["congestion_control"] = cc
			}
			if rm := q.Get("udp_relay_mode"); rm != "" {
				node["udp_relay_mode"] = rm
			}
			if al := q.Get("alpn"); al != "" {
				node["tls"].(map[string]any)["alpn"] = []string{al}
			}
		case "anytls":
			node = map[string]any{"type": "anytls", "tag": tag,
				"server": u.Hostname(), "server_port": atoi(u.Port(), 443),
				"password": u.User.Username(),
				"tls": map[string]any{"enabled": true,
					"server_name": orEmpty(q.Get("peer"), orEmpty(q.Get("sni"), u.Hostname()))}}
		case "socks", "socks5":
			pw, _ := u.User.Password()
			node = map[string]any{"type": "socks", "tag": tag,
				"server": u.Hostname(), "server_port": atoi(u.Port(), 1080), "version": "5",
				"username": u.User.Username(), "password": pw}
		case "sub":
			// 嵌套订阅链接：解出来提示，订阅本体走 sakamoto.yaml subscriptions
			if dec, err := b64decode(u.Host); err == nil {
				reportf("  · sub:// 嵌套订阅: %s\n", string(dec))
			}
		}
		// SR obfs= 参数 = transport（websocket/grpc）
		if node != nil && node["type"] != "hysteria2" && node["type"] != "tuic" {
			switch q.Get("obfs") {
			case "websocket", "ws":
				node["transport"] = map[string]any{"type": "ws", "path": orEmpty(q.Get("path"), "/")}
			case "grpc":
				transport := map[string]any{"type": "grpc"}
				if service := q.Get("path"); service != "" {
					transport["service_name"] = strings.TrimPrefix(service, "/")
				}
				node["transport"] = transport
			}
		}
		if node == nil {
			continue
		}
		if chain != "" {
			node["_chain"] = chain // 后置解析成 detour
		}
		out = append(out, node)
	}
	return out
}

// b64decode 兼容 std / raw / urlsafe。
func b64decode(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("bad base64")
}

// cleanKey 处理 SR 双重 URL 编码及混入空白（pbk/sid）。
func cleanKey(s string) string {
	if v, err := url.PathUnescape(s); err == nil {
		s = v
	}
	if v, err := url.PathUnescape(s); err == nil {
		s = v
	}
	return strings.Join(strings.Fields(s), "")
}

// ---------------- 订阅拉取 ----------------

// fetchSub 拉取订阅并自动识别格式：sing-box JSON / Clash YAML / base64 分享链接。
func fetchSub(src config.SubSource, hc *http.Client) []map[string]any {
	resp, err := hc.Get(src.URL)
	if err != nil {
		reportf("  ! 订阅拉取失败(%s): %v\n", src.Name, err)
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	txt := strings.TrimSpace(string(body))
	format := src.Format
	if format == "" || format == "auto" {
		switch {
		case strings.HasPrefix(txt, "{"):
			format = "singbox"
		case strings.Contains(txt, "proxies:"):
			format = "clash"
		default:
			format = "base64"
		}
	}
	var nodes []map[string]any
	switch format {
	case "singbox":
		var cfg struct {
			Outbounds []map[string]any `json:"outbounds"`
		}
		if json.Unmarshal(body, &cfg) == nil {
			for _, o := range cfg.Outbounds {
				switch o["type"] { // 只收叶子协议节点
				case "selector", "urltest", "direct", "block", "dns", "", nil:
				default:
					nodes = append(nodes, o)
				}
			}
		}
	case "clash":
		var c struct {
			Proxies []map[string]any `yaml:"proxies"`
		}
		if yaml.Unmarshal(body, &c) == nil {
			for _, p := range c.Proxies {
				if n := clashToOutbound(p); n != nil {
					nodes = append(nodes, n)
				}
			}
		}
	default: // base64 分享链接
		if dec, err := base64.StdEncoding.DecodeString(txt); err == nil {
			txt = string(dec)
		}
		nodes = parseShareLines(strings.Split(txt, "\n"))
	}
	reportf("  订阅 %s: %d 节点 (%s)\n", src.Name, len(nodes), format)
	return nodes
}

// clashToOutbound 把 Clash/mihomo proxy 条目转成 sing-box outbound。
func clashToOutbound(p map[string]any) map[string]any {
	name, _ := p["name"].(string)
	server, _ := p["server"].(string)
	port := intFromAny(p["port"])
	switch strings.ToLower(str(p, "type")) {
	case "vless":
		n := map[string]any{"type": "vless", "tag": name, "server": server,
			"server_port": port, "uuid": str(p, "uuid")}
		if fl := str(p, "flow"); fl != "" {
			n["flow"] = fl
		}
		tls := map[string]any{"enabled": true, "server_name": orEmpty(str(p, "servername"), server)}
		if ro, ok := p["reality-opts"].(map[string]any); ok {
			tls["reality"] = map[string]any{"enabled": true,
				"public_key": str(ro, "public-key"), "short_id": str(ro, "short-id")}
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": orEmpty(str(p, "client-fingerprint"), "chrome")}
		}
		n["tls"] = tls
		return n
	case "vmess":
		return map[string]any{"type": "vmess", "tag": name, "server": server,
			"server_port": port, "uuid": str(p, "uuid"),
			"security": orEmpty(str(p, "cipher"), "auto"), "alter_id": intFromAny(p["alterId"])}
	case "trojan":
		return map[string]any{"type": "trojan", "tag": name, "server": server,
			"server_port": port, "password": str(p, "password"),
			"tls": map[string]any{"enabled": true, "server_name": orEmpty(str(p, "sni"), server)}}
	case "hysteria2":
		return map[string]any{"type": "hysteria2", "tag": name, "server": server,
			"server_port": port, "password": str(p, "password"),
			"tls": map[string]any{"enabled": true, "server_name": orEmpty(str(p, "sni"), server)}}
	case "ss":
		return map[string]any{"type": "shadowsocks", "tag": name, "server": server,
			"server_port": port, "method": str(p, "cipher"), "password": str(p, "password")}
	}
	return nil
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case string:
		return atoi(n, 0)
	}
	return 0
}

func str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func atoi(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func splitCSV(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}
