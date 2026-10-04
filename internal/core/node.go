package core

// NodeStatus is the latency-test vocabulary from docs/tui.md and skills.md
// invariant 9: "Untested, Testing…, Reachable Nms, Failed/timed out; a
// selected dot is not a connectivity claim."
type NodeStatus string

const (
	// NodeUntested means no URL test result exists for this node yet.
	NodeUntested NodeStatus = "Untested"
	// NodeTesting means a URL test is in flight ("Testing…").
	NodeTesting NodeStatus = "Testing"
	// NodeReachable means the last URL test measured a positive latency.
	// It is a property of that single node, NOT of the whole system route
	// (docs/tui.md: "A node's latency likewise does not prove the whole
	// system route works.").
	NodeReachable NodeStatus = "Reachable"
	// NodeFailed means the last URL test failed or timed out
	// ("Failed/timed out"). A selected node may absolutely be Failed.
	NodeFailed NodeStatus = "Failed"
)

// NodeState is the front-end view of one proxy node. It exists so that both
// the macOS TUI and an iOS client derive display state from the same rule:
// selection (the filled dot) and reachability (the latency test) are
// orthogonal facts and must never be conflated.
type NodeState struct {
	Tag string // outbound tag, unique per config
	// Selected means the node is the active choice of its group (the filled
	// dot "●"). It records an intent/decision only.
	Selected bool
	// Status is derived from the latest URL test.
	Status NodeStatus
	// LatencyMS is the last measured latency in milliseconds; positive only
	// when Status == NodeReachable. -1 is the historical "failed" marker.
	LatencyMS int32
}

// Reachable reports whether the last URL test passed for this node. It is
// deliberately independent of Selected: {Selected: true, Reachable: false} is
// a valid and important state (the user pinned a node that just failed its
// test) and must render as selected-but-unreachable, never as "connected".
func (n NodeState) Reachable() bool { return n.Status == NodeReachable }

// StatusFromLatency derives a node status from a URL-test result.
//
//   - testing wins: while a test is in flight the node shows "Testing…" even
//     if a previous positive latency exists (the old value is stale during a
//     re-test).
//   - delay > 0: Reachable with that latency.
//   - delay < 0 (the historical failure marker, or any negative result) when
//     not testing: Failed — the test ran and did not pass.
//   - delay == 0 when not testing: Untested — no concluded result exists.
func StatusFromLatency(delayMS int32, testing bool) NodeStatus {
	if testing {
		return NodeTesting
	}
	if delayMS > 0 {
		return NodeReachable
	}
	if delayMS < 0 {
		return NodeFailed
	}
	return NodeUntested
}

// GroupKind mirrors the two selector-ish outbound types worth distinguishing
// in a front end. Selector groups accept a direct user choice; URL-test
// groups choose automatically, and a manual choice on their members is
// delegated through the manual-pick chain by the controller.
type GroupKind string

const (
	GroupSelector GroupKind = "selector"
	GroupURLTest  GroupKind = "urltest"
)

// GroupState is the front-end view of one proxy group and its members.
type GroupState struct {
	Tag        string
	Kind       GroupKind
	Selectable bool // selector groups are directly selectable
	// SelectedTag is the group's currently active outbound; empty means the
	// core has not reported a selection.
	SelectedTag string
	Items       []NodeState
}

// SelectedNode returns the currently selected member state, if present.
func (g GroupState) SelectedNode() (NodeState, bool) {
	for _, n := range g.Items {
		if n.Tag == g.SelectedTag {
			return n, true
		}
	}
	return NodeState{}, false
}
