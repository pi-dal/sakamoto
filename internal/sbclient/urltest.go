package sbclient

import (
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/daemon"
)

// URLTest acknowledges a background job. Its RPC deadline is independent of
// the measurement deadline; cancelling the acknowledgement does not cancel it.
const URLTestRequestTimeout = 10 * time.Second

// URLTestGroupWait covers the native ten-worker batches and snapshot delivery.
func URLTestGroupWait(groups []*daemon.Group, tags []string) time.Duration {
	byTag := map[string]*daemon.Group{}
	for _, group := range groups {
		byTag[group.Tag] = group
	}
	largest := 1
	for _, tag := range tags {
		seen := map[string]bool{}
		var count func(string) int
		count = func(tag string) int {
			if seen[tag] {
				return 0
			}
			seen[tag] = true
			group := byTag[tag]
			if group == nil {
				return 1
			}
			n := 0
			for _, item := range group.Items {
				n += count(item.Tag)
			}
			return n
		}
		if n := count(tag); n > largest {
			largest = n
		}
	}
	return time.Duration((largest+9)/10)*C.TCPTimeout + 5*time.Second
}

// A leaf RPC has no surrounding group context: dial and HTTP phases each have
// a timeout, and multiplexing can warm up with a second request.
const URLTestNodeWait = 4*C.TCPTimeout + 5*time.Second
