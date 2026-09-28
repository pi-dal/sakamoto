package gen

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/experiment"
)

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
	if err := config.ValidateAPISecret(cfg.API.Secret); err != nil {
		return err
	}
	if err := cfg.ValidateAPIEndpoint(); err != nil {
		return err
	}
	if err := cfg.ValidateExperiment(); err != nil {
		return err
	}
	out := o.OutDir
	if out == "" {
		out = config.DefaultDir()
	}
	rulesDir := filepath.Join(out, "rules")
	if err := os.MkdirAll(rulesDir, 0700); err != nil {
		return err
	}

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

	ts := tailscaleInfo{}
	if cfg.TailscaleOptimize {
		ts = detectTailscale()
	}
	srsEntries, err := buildRules(p, rulesDir, hc, ts)
	if err != nil {
		return err
	}

	outbounds, nodes, exitTag, mainMembers, err := buildOutbounds(o, cfg, hc)
	if err != nil {
		return err
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
	if direct, ok := p.bk["direct"]; ok && len(direct["domain"])+len(direct["domain_suffix"])+len(direct["domain_keyword"]) > 0 {
		tag := "rs-direct"
		if len(direct["ip_cidr"]) > 0 {
			tag = "rs-direct-domains"
		}
		dnsDirectSets = []string{tag}
	}
	dnsRules := []map[string]any{}
	if len(p.hosts) > 0 {
		dnsServers = append(dnsServers, map[string]any{"type": "hosts", "tag": "sr-hosts", "predefined": p.hosts})
		names := make([]string, 0, len(p.hosts))
		for name := range p.hosts {
			names = append(names, name)
		}
		sort.Strings(names)
		dnsRules = append(dnsRules, map[string]any{"domain": names, "action": "route", "server": "sr-hosts"})
	}
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
	var udpProtections []map[string]any
	if cfg.BlockSTUN {
		// STUN (UDP) 可让网页获取公网映射地址；阻断已识别的 STUN 包。
		udpProtections = append(udpProtections, map[string]any{"protocol": "stun", "action": "reject"})
	}
	if cfg.BlockQUIC {
		// 拒 UDP:443 强制回落 TCP/TLS（代理下 QUIC 走 UDP-over-TCP 白损性能）。
		udpProtections = append(udpProtections, map[string]any{"network": "udp", "port": 443, "action": "reject"})
	}
	if len(udpProtections) > 0 {
		routeRules = append(append(append([]map[string]any{}, routeRules[:2]...), udpProtections...), routeRules[2:]...)
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

	finalTag := "direct"
	if cfg.Experiment.Mode == "on" {
		finalTag = exitTag
	}
	if cfg.Experiment.Mode == "auto" {
		learned, err := experiment.Load(out)
		if err != nil {
			return fmt.Errorf("load auto-proxy rules: %w", err)
		}
		if len(learned.Domains) > 0 {
			// Explicit DIRECT/REJECT stay authoritative: learn only the fallback case.
			at := len(routeRules) - 1
			routeRules = append(routeRules, nil)
			copy(routeRules[at+1:], routeRules[at:])
			routeRules[at] = map[string]any{"domain": learned.Domains, "action": "route", "outbound": exitTag}
		}
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
			"final": finalTag, "auto_detect_interface": true, "rule_set": srsEntries,
			"default_domain_resolver": map[string]any{"server": localPublic, "strategy": "ipv4_only"},
		},
		"services": []map[string]any{{
			"type": "api", "listen": "127.0.0.1", "listen_port": 9090,
			"secret": cfg.API.Secret, "dashboard": map[string]any{"enabled": false},
		}},
	}
	b, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sing-box config: %w", err)
	}
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
		if err := os.WriteFile(sidecarPath, []byte(yamlText), 0600); err != nil {
			return fmt.Errorf("write template: %w", err)
		}
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
	reportf("→ %s  最终规则=%s，代理出口=%s（detour→MainProxy）\n", cfgPath, finalTag, exitTag)
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
