package watch

import (
	"github.com/sagernet/sing-box/daemon"
	"testing"
)

func TestFallbackUsesFreshResultsOnly(t *testing.T) {
	snap := &daemon.Groups{Group: []*daemon.Group{
		{Tag: "RealityAuto", Items: []*daemon.GroupItem{{Tag: "reality-1", UrlTestDelay: 100, UrlTestTime: 100}}},
		{Tag: "OthersAuto", Items: []*daemon.GroupItem{{Tag: "hy2-1", UrlTestDelay: 220, UrlTestTime: 201}}},
	}}
	if aliveFresh(snap, "RealityAuto", 200) {
		t.Fatal("stale reality latency must not keep RealityAuto alive")
	}
	if !aliveFresh(snap, "OthersAuto", 200) {
		t.Fatal("fresh fallback must be considered alive")
	}
	if tag, index := pickFallback(snap, []string{"RealityAuto", "OthersAuto"}, 200); tag != "OthersAuto" || index != 1 {
		t.Fatalf("should fall back to OthersAuto: %s %d", tag, index)
	}
	snap.Group[0].Items[0].UrlTestTime = 201
	if !aliveFresh(snap, "RealityAuto", 200) {
		t.Fatal("recovered reality must become eligible")
	}
	if tag, index := pickFallback(snap, []string{"RealityAuto", "OthersAuto"}, 200); tag != "RealityAuto" || index != 0 {
		t.Fatalf("should recover RealityAuto: %s %d", tag, index)
	}
}
