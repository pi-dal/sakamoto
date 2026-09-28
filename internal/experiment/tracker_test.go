package experiment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"
)

func conn(id, rule, domain string, downlink int64, event daemon.ConnectionEventType) *daemon.ConnectionEvent {
	return &daemon.ConnectionEvent{Id: id, Type: event, Connection: &daemon.Connection{Network: "tcp", Outbound: "direct", Rule: rule, Domain: domain, Destination: "93.184.215.14:443", DownlinkTotal: downlink}}
}
func TestTrackerOnlyCorrelatedFailedFinalDirect(t *testing.T) {
	t0 := time.Now()
	tr := NewTracker(3)
	log := "[12345 10s] connection: open connection to example.com:443 using outbound/direct[direct]: dial tcp 93.184.215.14:443: i/o timeout"
	if tr.Failure(log, t0, true) != "" {
		t.Fatal("unobserved error counted")
	}
	tr.Connection(conn("bad-dns", "", "example.com", 0, daemon.ConnectionEventType_CONNECTION_EVENT_NEW), t0)
	if tr.Failure("connection: open connection to example.com:443 using outbound/direct[direct]: lookup failed: i/o timeout", t0, true) != "" {
		t.Fatal("DNS timeout should not be a direct dial failure")
	}
	if tr.Failure("connection: open connection to example.com:443 using outbound/direct[direct]: dial tcp 93.184.215.14:443: connection refused", t0, true) != "" {
		t.Fatal("refused port should not poison domain")
	}
	tr.active = map[string]attempt{}
	tr.Connection(conn("explicit", "rs-direct", "example.com", 0, daemon.ConnectionEventType_CONNECTION_EVENT_NEW), t0)
	if tr.Failure(log, t0, true) != "" {
		t.Fatal("explicit DIRECT counted")
	}
	for i := 0; i < 2; i++ {
		at := t0.Add(time.Duration(i) * 11 * time.Second)
		tr.Connection(conn(string(rune('a'+i)), "", "example.com", 0, daemon.ConnectionEventType_CONNECTION_EVENT_NEW), at)
		if d := tr.Failure(log, at, false); d != "" {
			t.Fatal("unhealthy proxy counted")
		}
		if d := tr.Failure(log, at, true); d != "" {
			t.Fatal("promoted before threshold")
		}
		if d := tr.Failure(log, at.Add(time.Second), true); d != "" {
			t.Fatal("duplicate error counted")
		}
	}
	at := t0.Add(23 * time.Second)
	tr.Connection(conn("third", "", "example.com", 0, daemon.ConnectionEventType_CONNECTION_EVENT_NEW), at)
	if got := tr.Failure(log, at, true); got != "example.com" {
		t.Fatalf("expected promotion, got %q", got)
	}
	tr.Connection(conn("fourth", "", "example.com", 0, daemon.ConnectionEventType_CONNECTION_EVENT_NEW), at.Add(12*time.Second))
	tr.Connection(conn("fourth", "", "example.com", 2, daemon.ConnectionEventType_CONNECTION_EVENT_CLOSED), at.Add(14*time.Second))
	if got := tr.Failure(log, at.Add(15*time.Second), true); got != "" {
		t.Fatal("success should not promote")
	}
	for _, invalid := range []string{"localhost", "127.0.0.1", "foo.ts.net", "dev.local", "example..com"} {
		if Domain(invalid) != "" {
			t.Fatalf("allowed internal domain %q", invalid)
		}
	}
}
func TestRuleInsertionKeepsExplicitPolicies(t *testing.T) {
	original := []byte(`{"route":{"final":"direct","rules":[{"rule_set":["rs-reject"],"action":"reject"},{"rule_set":["rs-direct"],"outbound":"direct","action":"route"},{"rule_set":["rs-proxy"],"outbound":"Exit","action":"route"}]}}`)
	out, err := AddRule(original, []string{"example.com"}, "Exit")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Route struct {
			Final string           `json:"final"`
			Rules []map[string]any `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Route.Rules) != 4 || cfg.Route.Rules[1]["outbound"] != "direct" || cfg.Route.Rules[2]["outbound"] != "Exit" || cfg.Route.Rules[3]["rule_set"] == nil || cfg.Route.Final != "direct" {
		t.Fatalf("unsafe rule order: %v", cfg.Route.Rules)
	}
	if _, err := AddRule(original, []string{"bad.example"}, "wrong"); err == nil {
		t.Fatal("mismatched exit accepted")
	}
	if exit, err := ProxyOutbound(original); err != nil || exit != "Exit" {
		t.Fatal("exit detection failed", err)
	}
	dir := t.TempDir()
	if err := Save(dir, State{Domains: []string{"example.com"}}); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir)
	if err != nil || len(loaded.Domains) != 1 {
		t.Fatal(loaded, err)
	}
	stat, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil || stat.Mode().Perm() != 0o600 {
		t.Fatal("learned rules must be private", err)
	}
}
