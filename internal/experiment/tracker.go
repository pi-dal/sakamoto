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
var directConnection = regexp.MustCompile(`(?i)connection: open connection to ([a-z0-9.-]+):(\d+) using outbound/direct\[direct\]`)

// Cloudflare's 1009 is the explicit country/region restriction. The text
// forms require a Cloudflare marker as well, avoiding generic HTTP 403s.
var cfRegionBlock = regexp.MustCompile(`(?i)(?:\b(?:error|code)\s*1009\b|\b1009\b.{0,80}(?:country|region)|cloudflare.{0,120}(?:country|region|error\s*1009)|(?:country|region).{0,120}(?:cloudflare|cf-ray))`)

type attempt struct {
	key, domain string
	seen        time.Time
}
type count struct {
	n           int
	first, last time.Time
}
type pendingFailure struct {
	message string
	seen    time.Time
}

type Tracker struct {
	active    map[string]attempt
	counts    map[string]count
	threshold int
	nextPrune time.Time
	pending   map[string]pendingFailure
}

func NewTracker(threshold int) *Tracker {
	return &Tracker{active: map[string]attempt{}, counts: map[string]count{}, pending: map[string]pendingFailure{}, threshold: threshold}
}

// prune bounds observed attempts even if no timeout error ever arrives.
func (t *Tracker) prune(now time.Time) {
	if now.Before(t.nextPrune) {
		return
	}
	t.nextPrune = now.Add(time.Second)
	for id, a := range t.active {
		if now.Sub(a.seen) > 2*time.Minute {
			delete(t.active, id)
		}
	}
	for key, failure := range t.pending {
		if now.Sub(failure.seen) > 2*time.Second {
			delete(t.pending, key)
		}
	}
	for domain, c := range t.counts {
		if now.Sub(c.first) >= 30*time.Minute {
			delete(t.counts, domain)
		}
	}
}

func (t *Tracker) Connection(ev *daemon.ConnectionEvent, now time.Time) {
	if ev == nil || ev.Connection == nil || ev.Type == daemon.ConnectionEventType_CONNECTION_EVENT_UPDATE {
		return
	}
	t.prune(now)
	c := ev.Connection
	if ev.Type == daemon.ConnectionEventType_CONNECTION_EVENT_CLOSED {
		if a, ok := t.active[ev.Id]; ok && c.DownlinkTotal > 0 {
			delete(t.counts, a.domain)
			delete(t.pending, a.key)
			delete(t.active, ev.Id) // A successful attempt must not match a later error.
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
	if len(t.active) >= 4096 {
		return
	}
	t.active[ev.Id] = attempt{key: domain + ":" + port, domain: domain, seen: now}
}

// CloudflareRegionBlock returns a direct hostname only for an explicit
// Cloudflare regional restriction, never for a generic 403 or challenge.
func (t *Tracker) CloudflareRegionBlock(log string, now time.Time, proxyHealthy bool) string {
	t.prune(now)
	if !proxyHealthy || !cfRegionBlock.MatchString(log) {
		return ""
	}
	match := directConnection.FindStringSubmatch(log)
	if len(match) != 3 {
		return ""
	}
	domain := Domain(match[1])
	if domain == "" {
		return ""
	}
	key := domain + ":" + match[2]
	for id, a := range t.active {
		if now.Sub(a.seen) > 2*time.Minute {
			delete(t.active, id)
			continue
		}
		if a.key == key {
			delete(t.active, id)
			return domain
		}
	}
	return ""
}

// Failure returns a newly learned domain only after spaced, independent
// direct timeouts. The caller must also establish a recent healthy proxy.
func (t *Tracker) Failure(log string, now time.Time, proxyHealthy bool) string {
	candidate := t.observeFailure(directFailure, log, now, proxyHealthy)
	if candidate != nil && candidate.Failures >= t.threshold {
		delete(t.counts, candidate.Domain)
		return candidate.Domain
	}
	return ""
}

var directDialFailure = regexp.MustCompile(`(?i)connection: open connection to ([a-z0-9.-]+):(\d+) using outbound/direct\[direct\]:.*dial tcp .*(?:i/o timeout|connection refused|connection reset|network is unreachable|no route to host)`)

// ObserveFailure allows the desktop to corroborate one observed dial failure
// with a bounded direct/proxy comparison instead of waiting for user retries.
func (t *Tracker) ObserveFailure(log string, now time.Time, proxyHealthy bool) *Candidate {
	candidate := t.observeFailure(directDialFailure, log, now, proxyHealthy)
	if candidate != nil || !proxyHealthy {
		return candidate
	}
	match := directDialFailure.FindStringSubmatch(log)
	if len(match) == 3 && Domain(match[1]) != "" && len(t.pending) < 256 {
		t.pending[Domain(match[1])+":"+match[2]] = pendingFailure{message: log, seen: now}
	}
	return nil
}

// ObserveConnection tolerates the independent log/connection gRPC streams
// arriving in either order, but retains unmatched logs for only two seconds.
func (t *Tracker) ObserveConnection(ev *daemon.ConnectionEvent, now time.Time) *Candidate {
	t.Connection(ev, now)
	if ev == nil || ev.Connection == nil || ev.Type != daemon.ConnectionEventType_CONNECTION_EVENT_NEW {
		return nil
	}
	a, ok := t.active[ev.Id]
	if !ok {
		return nil
	}
	failure, ok := t.pending[a.key]
	if !ok {
		return nil
	}
	delete(t.pending, a.key)
	if now.Sub(failure.seen) > 2*time.Second {
		return nil
	}
	return t.observeFailure(directDialFailure, failure.message, now, true)
}

func (t *Tracker) observeFailure(pattern *regexp.Regexp, log string, now time.Time, proxyHealthy bool) *Candidate {
	t.prune(now)
	if !proxyHealthy || !strings.Contains(log, "outbound/direct[direct]") {
		return nil
	}
	match := pattern.FindStringSubmatch(log)
	if len(match) != 3 {
		return nil
	}
	domain := Domain(match[1])
	if domain == "" {
		return nil
	}
	key := domain + ":" + match[2]
	matched := false
	for id, a := range t.active {
		if now.Sub(a.seen) <= 2*time.Minute && a.key == key {
			matched = true
			delete(t.active, id)
			break
		}
	}
	if !matched {
		return nil
	}
	c := t.counts[domain]
	if c.first.IsZero() || now.Sub(c.first) >= 30*time.Minute {
		if len(t.counts) >= 1024 {
			return nil
		}
		c = count{first: now}
	}
	if !c.last.IsZero() && now.Sub(c.last) < 10*time.Second {
		return nil
	}
	c.n++
	c.last = now
	t.counts[domain] = c
	return &Candidate{Domain: domain, Port: match[2], Failures: c.n, Required: t.threshold, Reason: "observed unmatched direct TCP dial failure"}
}

// ObserveRegionBlock retains the observed port for origin verification.
func (t *Tracker) ObserveRegionBlock(log string, now time.Time, proxyHealthy bool) *Candidate {
	domain := t.CloudflareRegionBlock(log, now, proxyHealthy)
	if domain == "" {
		return nil
	}
	match := directConnection.FindStringSubmatch(log)
	return &Candidate{Domain: domain, Port: match[2], Failures: 1, Required: t.threshold, Reason: "explicit Cloudflare regional restriction on unmatched direct traffic"}
}

func (t *Tracker) Clear(domain string) { delete(t.counts, strings.ToLower(domain)) }
