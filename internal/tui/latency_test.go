package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"
)

func TestRepeatedIdenticalLatencyRequiresANewTimestampAndDispatchCompletion(t *testing.T) {
	now := time.Unix(1800000000, 600000000)
	item := &daemon.GroupItem{Tag: "JP Lite", Type: "vless", UrlTestDelay: 120, UrlTestTime: now.Unix()}
	groups := []*daemon.Group{{Tag: "RealityAuto", Items: []*daemon.GroupItem{item}}}
	batch := newTestBatch(groups, now)
	batch.observe(groups)
	if len(batch.results) != 0 {
		t.Fatal("cached result counted as a fresh test")
	}
	batch.expire(now.Add(time.Hour))
	if batch.done() {
		t.Fatal("results expired while request dispatch was pending")
	}
	item.UrlTestTime = batch.start.Unix()
	batch.observe(groups)
	if batch.results["JP Lite"] != 120 {
		t.Fatal("fresh identical latency was ignored")
	}
	if !strings.Contains(latencySnapshot(item, now.Add(time.Hour), 2*time.Minute), "stale") {
		t.Fatal("old history advertised as current")
	}
	if got := testAge(item.UrlTestTime, batch.start.Add(37*time.Second)); got != "37s ago" {
		t.Fatal(got)
	}
}
