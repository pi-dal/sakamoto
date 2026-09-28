package experiment

import (
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/sagernet/sing-box/daemon"
)

// sing-box 1.14 connection events omit failures. Correlate its error log with
// an observed TCP connection that had no matching route rule (route.final).
// Never infer a failure merely from zero traffic or a closed connection.
var directFailure = regexp.MustCompile(`(?i)connection: open connection to ([a-z0-9.-]+):(\d+) using outbound/direct\[direct\]:.*dial tcp .*i/o timeout`)

type attempt struct {
	key, domain string
	seen        time.Time
}
type count struct {
	n           int
	first, last time.Time
}
type Tracker struct {
	active    map[string]attempt
	counts    map[string]count
	threshold int
}

func NewTracker(threshold int) *Tracker {
	return &Tracker{active: map[string]attempt{}, counts: map[string]count{}, threshold: threshold}
}
func (t *Tracker) Connection(ev *daemon.ConnectionEvent, now time.Time) {
	if ev == nil || ev.Connection == nil {
		return
	}
	c := ev.Connection
	if ev.Type == daemon.ConnectionEventType_CONNECTION_EVENT_CLOSED {
		if a, ok := t.active[ev.Id]; ok && c.DownlinkTotal > 0 {
			delete(t.counts, a.domain)
		}
		// Keep closed attempts briefly: the error log can arrive after CLOSED.
		return
	}
	if ev.Type != daemon.ConnectionEventType_CONNECTION_EVENT_NEW || c.Network != "tcp" || c.Outbound != "direct" || c.Rule != "" {
		return
	}
	domain := Domain(c.Domain)
	if domain == "" {
		return
	}
	_, port, err := net.SplitHostPort(c.Destination)
	if err != nil || port == "" {
		return
	}
	t.active[ev.Id] = attempt{key: domain + ":" + port, domain: domain, seen: now}
}

// Failure returns a newly learned domain only after spaced, independent
// direct timeouts. The caller must also establish a recent healthy proxy.
func (t *Tracker) Failure(log string, now time.Time, proxyHealthy bool) string {
	match := directFailure.FindStringSubmatch(log)
	if len(match) != 3 || !proxyHealthy {
		return ""
	}
	domain := Domain(match[1])
	if domain == "" {
		return ""
	}
	key := domain + ":" + match[2]
	matched := false
	for id, a := range t.active {
		if now.Sub(a.seen) > 2*time.Minute {
			delete(t.active, id)
			continue
		}
		if a.key == key {
			matched = true
			delete(t.active, id)
			break
		}
	}
	if !matched {
		return ""
	}
	c := t.counts[domain]
	if now.Sub(c.last) > 30*time.Minute {
		c = count{first: now}
	}
	if !c.last.IsZero() && now.Sub(c.last) < 10*time.Second {
		return ""
	}
	c.n++
	c.last = now
	t.counts[domain] = c
	if c.n >= t.threshold {
		delete(t.counts, domain)
		return domain
	}
	return ""
}
func (t *Tracker) Clear(domain string) { delete(t.counts, strings.ToLower(domain)) }
