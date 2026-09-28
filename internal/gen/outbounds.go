package gen

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/pi-dal/sakamoto/internal/config"
)

// buildOutbounds merges manual nodes before subscriptions, classifies automatic groups,
// and keeps the SOCKS exit separate from the selectable entry nodes.
func buildOutbounds(o Options, cfg *config.Config, hc *http.Client) ([]map[string]any, []map[string]any, string, []string, error) {
	// 3) 节点
	allow := map[string]bool{}
	for _, h := range strings.Split(o.AllowHosts, ",") {
		if h = strings.TrimSpace(h); h != "" {
			allow[h] = true
		}
	}
	// 3) 节点：手动（srjson 白名单 + nodes.txt）优先，订阅兜底；tag 去重
	var nodes []map[string]any
	if len(allow) > 0 {
		imported, err := srJSONNodes(o.SRJSONPath, allow)
		if err != nil {
			return nil, nil, "", nil, fmt.Errorf("读取 Shadowrocket 备份: %w", err)
		}
		nodes = append(nodes, imported...)
	}
	nodes = append(nodes, parseShareLinks(o.NodesFile)...)
	for _, s := range cfg.Subscriptions {
		imported, err := fetchSub(s, hc)
		if err != nil {
			return nil, nil, "", nil, err
		}
		nodes = append(nodes, imported...)
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
	if len(otherTags) > 0 {
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

	return outbounds, nodes, exitTag, mainMembers, nil
}
