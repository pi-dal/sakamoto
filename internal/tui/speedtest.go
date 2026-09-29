package tui

import (
	"fmt"
	"sort"
	"time"

	"github.com/sagernet/sing-box/daemon"
)

// testBatch tracks leaf proxies, not selector/urltest groups or direct.
type testBatch struct {
	tags          []string
	baseline      map[string]int64
	baselineDelay map[string]int32
	results       map[string]int32 // >0 success; -1 failure or timeout
	start         time.Time
	deadline      time.Time
}

func nodeTags(groups []*daemon.Group) []string {
	seen := map[string]bool{}
	var tags []string
	for _, g := range groups {
		for _, it := range g.Items {
			switch it.Type {
			case "selector", "urltest", "direct", "block", "dns", "":
				continue
			}
			if !seen[it.Tag] {
				seen[it.Tag] = true
				tags = append(tags, it.Tag)
			}
		}
	}
	sort.Strings(tags)
	return tags
}

func newTestBatch(groups []*daemon.Group, now time.Time) *testBatch {
	tags := nodeTags(groups)
	baseline := map[string]int64{}
	delay := map[string]int32{}
	for _, tag := range tags {
		baseline[tag] = 0
	}
	for _, g := range groups {
		for _, it := range g.Items {
			if _, ok := baseline[it.Tag]; ok && it.UrlTestTime >= baseline[it.Tag] {
				baseline[it.Tag] = it.UrlTestTime
				delay[it.Tag] = it.UrlTestDelay
			}
		}
	}
	return &testBatch{tags: tags, baseline: baseline, baselineDelay: delay, results: map[string]int32{}, start: now, deadline: now.Add(15*time.Second + time.Duration(len(tags))*50*time.Millisecond)}
}
func (b *testBatch) observe(groups []*daemon.Group) {
	for _, g := range groups {
		for _, it := range g.Items {
			if _, ok := b.baseline[it.Tag]; !ok {
				continue
			}
			if b.results[it.Tag] != 0 {
				continue
			}
			if it.UrlTestDelay > 0 && (it.UrlTestTime > b.baseline[it.Tag] ||
				(it.UrlTestTime >= b.start.Unix()-1 && it.UrlTestDelay != b.baselineDelay[it.Tag])) {
				b.results[it.Tag] = it.UrlTestDelay
			}
		}
	}
}
func (b *testBatch) expire(now time.Time) {
	if now.Before(b.deadline) {
		return
	}
	for _, tag := range b.tags {
		if b.results[tag] == 0 {
			b.results[tag] = -1
		}
	}
}
func (b *testBatch) done() bool { return len(b.results) == len(b.tags) }
func (b *testBatch) message() string {
	ok, bad := 0, 0
	for _, v := range b.results {
		if v > 0 {
			ok++
		} else {
			bad++
		}
	}
	if b.done() {
		return fmt.Sprintf("All tests complete: %d nodes · %d passed · %d failed/timed out", len(b.tags), ok, bad)
	}
	return fmt.Sprintf("Node tests %d/%d · %d passed · %d failed", len(b.results), len(b.tags), ok, bad)
}
