package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"
)

func TestAllSpeedtestsDeduplicateLeafNodes(t *testing.T) {
	now := time.Unix(1800000000, 0)
	groups := []*daemon.Group{
		{Tag: "MainProxy", Type: "selector", Items: []*daemon.GroupItem{{Tag: "RealityAuto", Type: "urltest"}, {Tag: "ManualPick", Type: "selector"}}},
		{Tag: "RealityAuto", Type: "urltest", Items: []*daemon.GroupItem{{Tag: "vision-jp", Type: "vless"}, {Tag: "vmess-us", Type: "vmess"}}},
		{Tag: "OthersAuto", Type: "urltest", Items: []*daemon.GroupItem{{Tag: "hy2-us", Type: "hysteria2"}, {Tag: "tuic-us", Type: "tuic"}, {Tag: "anytls-us", Type: "anytls"}}},
		{Tag: "ManualPick", Type: "selector", Items: []*daemon.GroupItem{{Tag: "vision-jp", Type: "vless"}, {Tag: "hy2-us", Type: "hysteria2"}}},
	}
	batch := newTestBatch(groups, now)
	if len(batch.tags) != 5 {
		t.Fatalf("wanted 5 distinct protocol nodes, got %v", batch.tags)
	}
	if strings.Contains(strings.Join(batch.tags, ","), "RealityAuto") {
		t.Fatal("group tested as node")
	}
	groups[1].Items[0].UrlTestTime = now.Unix() + 1
	groups[1].Items[0].UrlTestDelay = 120
	groups[1].Items[1].UrlTestTime = now.Unix() + 2
	groups[1].Items[1].UrlTestDelay = 205
	batch.observe(groups)
	if len(batch.results) != 2 || batch.done() {
		t.Fatal(batch.message())
	}
	batch.expire(now.Add(20 * time.Second))
	if !batch.done() || batch.results["vision-jp"] != 120 || batch.results["anytls-us"] != -1 {
		t.Fatal(batch.message())
	}
	if !strings.Contains(batch.message(), "成功 2") || !strings.Contains(batch.message(), "失败/超时 3") {
		t.Fatal(batch.message())
	}
}
