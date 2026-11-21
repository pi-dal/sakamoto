package regionaldns

import (
	"encoding/json"
	"errors"
	"reflect"
)

var errRecoveryPolicy = errors.New("saved recovery configuration has a different routing, DNS, or connection policy")

type recoveryCore struct {
	Outbounds    []map[string]any `json:"outbounds"`
	Endpoints    []map[string]any `json:"endpoints"`
	Experimental map[string]any   `json:"experimental"`
	Services     []map[string]any `json:"services"`
	DNS          map[string]any   `json:"dns"`
	Route        map[string]any   `json:"route"`
	Inbounds     []map[string]any `json:"inbounds"`
}

func referencesRelay(v any) bool {
	switch x := v.(type) {
	case string:
		return x == ResolverTag
	case []any:
		for _, child := range x {
			if referencesRelay(child) {
				return true
			}
		}
	case map[string]any:
		for _, child := range x {
			if referencesRelay(child) {
				return true
			}
		}
	}
	return false
}

// Detect a leftover paired plain/absolute domain-dial guard even when the
// regional server and its DNS rules were removed independently.
func (c recoveryCore) hasRegionalPolicy() bool {
	if referencesRelay(c.DNS) {
		return true
	}
	rules, _ := c.Route["rules"].([]any)
	for _, item := range rules {
		r, _ := item.(map[string]any)
		if r["action"] != "resolve" || r["disable_cache"] != true || len(r) != 3 {
			continue
		}
		names, _ := r["domain"].([]any)
		if len(names) == 0 || len(names)%2 != 0 {
			continue
		}
		paired := true
		for i, n := range names[:len(names)/2] {
			s, ok := n.(string)
			if !ok || names[i+len(names)/2] != s+"." {
				paired = false
				break
			}
		}
		if paired {
			return true
		}
	}
	return false
}

func (c recoveryCore) relayTag() string {
	servers, _ := c.DNS["servers"].([]any)
	for _, item := range servers {
		s, _ := item.(map[string]any)
		if s["tag"] == ResolverTag {
			relay, _ := s["detour"].(string)
			return relay
		}
	}
	return ""
}

// CheckRecovery compares effective DNS, routing and the selected relay across
// an automatic recovery boundary, including changes behind stable outbound tags.
func CheckRecovery(current, retained []byte) error {
	var want, got *recoveryCore
	if json.Unmarshal(current, &want) != nil || want == nil || json.Unmarshal(retained, &got) != nil || got == nil {
		return errRecoveryPolicy
	}
	enabled, previous := want.hasRegionalPolicy(), got.hasRegionalPolicy()
	if enabled != previous {
		return errRecoveryPolicy
	}
	if want.Route == nil || !reflect.DeepEqual(want, got) {
		return errRecoveryPolicy
	}
	if !enabled {
		return nil
	}
	relay := want.relayTag()
	if relay == "" {
		return errRecoveryPolicy
	}
	pin, err := RelayFingerprint(current, relay)
	if err != nil {
		return errRecoveryPolicy
	}
	old, err := RelayFingerprint(retained, relay)
	if err != nil || old != pin {
		return errRecoveryPolicy
	}
	return nil
}
