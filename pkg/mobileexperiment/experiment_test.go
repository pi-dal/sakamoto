package mobileexperiment

import (
	"strings"
	"testing"
)

func TestMobileTrackerUsesHostEvidenceAndSpacing(t *testing.T) {
	tracker, err := NewExperimentTracker(3)
	if err != nil {
		t.Fatal(err)
	}
	message := "connection: open connection to example.com:443 using outbound/direct[direct]: dial tcp 93.184.215.14:443: i/o timeout"
	for i := 0; i < 3; i++ {
		now := int64(1_000_000 + i*11_000)
		tracker.Connection(string(rune('a'+i)), "example.com", "93.184.215.14:443", "tcp", "direct", "", false, 0, now)
		if got := tracker.ObserveLog(message, now, false, true, false); got != "" {
			t.Fatal("unhealthy proxy learned", got)
		}
		got := tracker.ObserveLog(message, now, true, true, false)
		if i < 2 && got != "" {
			t.Fatal("early promotion", got)
		}
		if i == 2 && got != "example.com" {
			t.Fatal("host threshold not honored", got)
		}
		if again := tracker.ObserveLog(message, now, true, true, false); again != "" {
			t.Fatal("duplicate counted")
		}
	}
}

func TestMobileTrackerRejectsExplicitDirectAndGeneric403(t *testing.T) {
	tracker, _ := NewExperimentTracker(1)
	tracker.Connection("explicit", "example.com", "93.184.215.14:443", "tcp", "direct", "rs-direct", false, 0, 1_000_000)
	message := "connection: open connection to example.com:443 using outbound/direct[direct]: HTTP 403 Cloudflare error 1009 country blocked"
	if got := tracker.ObserveLog(message, 1_000_000, true, false, true); got != "" {
		t.Fatal("explicit rule learned")
	}
	tracker.Connection("final", "example.com", "93.184.215.14:443", "tcp", "direct", "", false, 0, 1_000_000)
	if got := tracker.ObserveLog("HTTP 403 forbidden", 1_000_000, true, false, true); got != "" {
		t.Fatal("generic 403 learned")
	}
	if got := tracker.ObserveLog(message, 1_000_000, true, false, true); got != "example.com" {
		t.Fatal("explicit CF evidence lost", got)
	}
	if _, err := NewExperimentTracker(0); err == nil {
		t.Fatal("threshold accepted")
	}
}

func TestRulesKeepDirectPriorityAndValidateDomain(t *testing.T) {
	config := `{"route":{"final":"direct","rules":[{"action":"reject","rule_set":["rs-reject"]},{"action":"route","outbound":"direct","rule_set":["rs-direct"]},{"action":"route","outbound":"Exit","rule_set":["rs-proxy"]}]}}`
	got, err := RulesJSON(config, `["learned.example"]`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(got, "rs-direct") > strings.Index(got, "learned.example") || strings.Index(got, "learned.example") > strings.Index(got, "rs-proxy") {
		t.Fatal("priority changed")
	}
	if _, err := RulesJSON(config, `["127.0.0.1"]`); err == nil {
		t.Fatal("IP learned")
	}
	if _, err := RulesJSON(config, `["private.ts.net"]`); err == nil {
		t.Fatal("private namespace learned")
	}
}
