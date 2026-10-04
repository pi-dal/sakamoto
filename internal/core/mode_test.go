package core

import (
	"errors"
	"testing"
)

func TestParseRoutingMode(t *testing.T) {
	cases := []struct {
		in      string
		want    RoutingMode
		wantErr bool
	}{
		{"rule", ModeRule, false},
		{"Rule", ModeRule, false},
		{"GLOBAL", ModeGlobal, false},
		{" direct ", ModeDirect, false},
		{"", "", true},
		{"manual", "", true},
	}
	for _, tc := range cases {
		got, err := ParseRoutingMode(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParseRoutingMode(%q) expected an error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseRoutingMode(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseRoutingMode(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// TestNextRoutingMode pins the Mode-button cycle from docs/tui.md:
// Rule → Global → Direct → Rule. Unknown/empty values start at Rule, which is
// what the macOS TUI does before the first mode report arrives.
func TestNextRoutingMode(t *testing.T) {
	cases := []struct {
		current RoutingMode
		want    RoutingMode
	}{
		{ModeRule, ModeGlobal},
		{ModeGlobal, ModeDirect},
		{ModeDirect, ModeRule},
		{"", ModeRule},
		{"Bogus", ModeRule},
	}
	for _, tc := range cases {
		if got := NextRoutingMode(tc.current); got != tc.want {
			t.Fatalf("NextRoutingMode(%q) = %q, want %q", tc.current, got, tc.want)
		}
	}
}

// TestRequestModeUnavailableRequiresRegenerate pins the boundary from
// docs/tui.md: "an old generated config is reported as needing
// regeneration/reconnection rather than showing a false success."
func TestRequestModeUnavailableRequiresRegenerate(t *testing.T) {
	available := []RoutingMode{"rule", "global"} // raw clash-mode strings from the native API
	if err := RequestMode(ModeRule, available); err != nil {
		t.Fatalf("available mode rejected: %v", err)
	}
	err := RequestMode(ModeDirect, available)
	if !errors.Is(err, ErrRoutingModeUnavailable) {
		t.Fatalf("unavailable mode must map to ErrRoutingModeUnavailable, got %v", err)
	}
}

func TestModeAvailableCaseInsensitive(t *testing.T) {
	available := []RoutingMode{"Global", "Direct"}
	if !ModeAvailable(ModeGlobal, available) || !ModeAvailable("direct", available) {
		t.Fatal("case differences in native API strings must not matter")
	}
	if ModeAvailable(ModeRule, available) {
		t.Fatal("unlisted mode reported as available")
	}
}
