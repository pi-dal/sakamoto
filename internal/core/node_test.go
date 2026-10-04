package core

import "testing"

// TestStatusFromLatency pins the latency-test vocabulary from docs/tui.md.
func TestStatusFromLatency(t *testing.T) {
	cases := []struct {
		name    string
		delayMS int32
		testing bool
		want    NodeStatus
	}{
		{"no result", 0, false, NodeUntested},
		{"in flight", 0, true, NodeTesting},
		{"re-test in flight keeps testing", 120, true, NodeTesting},
		{"measured", 42, false, NodeReachable},
		{"failure marker", -1, false, NodeFailed},
		{"negative latency", -5, false, NodeFailed},
	}
	for _, tc := range cases {
		if got := StatusFromLatency(tc.delayMS, tc.testing); got != tc.want {
			t.Fatalf("%s: StatusFromLatency(%d, %v) = %s, want %s", tc.name, tc.delayMS, tc.testing, got, tc.want)
		}
	}
}

// TestSelectedIsNotReachable pins skills.md invariant 9: "a selected dot is
// not a connectivity claim". Selection and reachability are orthogonal; a
// selected node that failed its test must stay Failed while remaining
// selected, and an unselected node may be perfectly reachable.
func TestSelectedIsNotReachable(t *testing.T) {
	pinnedButDead := NodeState{Tag: "Realm", Selected: true, Status: StatusFromLatency(-1, false)}
	if pinnedButDead.Selected != true {
		t.Fatal("selection lost")
	}
	if pinnedButDead.Reachable() {
		t.Fatal("a selected node with a failed test must not report Reachable")
	}
	if pinnedButDead.Status != NodeFailed {
		t.Fatalf("status = %s, want Failed", pinnedButDead.Status)
	}

	idleButHealthy := NodeState{Tag: "Azure", Status: StatusFromLatency(80, false)}
	if !idleButHealthy.Reachable() {
		t.Fatal("a tested healthy node is Reachable regardless of selection")
	}
	if idleButHealthy.Selected {
		t.Fatal("reachability must never imply selection")
	}
}

func TestGroupStateSelectedNode(t *testing.T) {
	g := GroupState{
		Tag:         "MainProxy",
		Kind:        GroupSelector,
		Selectable:  true,
		SelectedTag: "Realm",
		Items: []NodeState{
			{Tag: "Realm", Status: NodeFailed},
			{Tag: "Azure", Status: NodeReachable, LatencyMS: 90},
		},
	}
	got, ok := g.SelectedNode()
	if !ok || got.Tag != "Realm" {
		t.Fatalf("SelectedNode() = %+v, ok=%v, want Realm", got, ok)
	}
	// The selected member being Failed while a sibling is Reachable is a
	// normal, reportable state — never an error to paper over.
	if got.Reachable() {
		t.Fatal("selected node inherited a sibling's reachability")
	}
	missing := GroupState{SelectedTag: "missing", Items: g.Items}
	if _, ok := missing.SelectedNode(); ok {
		t.Fatal("unknown selected tag must not resolve")
	}
}
