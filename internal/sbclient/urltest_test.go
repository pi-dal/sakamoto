package sbclient

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"
)

func TestURLTestWaitUsesConcurrentBatchesAndDeduplicatesNestedMembers(t *testing.T) {
	groups := []*daemon.Group{{Tag: "RealityAuto"}, {Tag: "Manual", Items: []*daemon.GroupItem{{Tag: "RealityAuto"}, {Tag: "same"}, {Tag: "Manual"}}}}
	for i := 0; i < 7; i++ {
		groups[0].Items = append(groups[0].Items, &daemon.GroupItem{Tag: string(rune('A' + i))})
	}
	if wait := URLTestGroupWait(groups, []string{"RealityAuto"}); wait != 20*time.Second {
		t.Fatalf("7-member native batch: %s", wait)
	}
	for i := 7; i < 21; i++ {
		groups[0].Items = append(groups[0].Items, &daemon.GroupItem{Tag: string(rune('A' + i))})
	}
	if wait := URLTestGroupWait(groups, []string{"RealityAuto"}); wait != 50*time.Second {
		t.Fatalf("21-member native batch: %s", wait)
	}
	groups[1].Items = append(groups[1].Items, &daemon.GroupItem{Tag: "A"})
	if wait := URLTestGroupWait(groups, []string{"Manual"}); wait != 50*time.Second {
		t.Fatalf("nested duplicate/cycle count: %s", wait)
	}
}
