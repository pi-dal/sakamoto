package regionaldns

import (
	"encoding/json"
	"errors"
	"reflect"
)

// Verify checks the saved sidecar against the actual candidate DNS rules and
// relay (including TLS and bootstrap). A stale or edited core cannot be promoted.
func Verify(raw []byte, p Profile, private []string) error {
	mismatch := errors.New("regional DNS settings differ from the core configuration; regenerate before connecting")
	if p.Domains == "" {
		var core *recoveryCore
		if json.Unmarshal(raw, &core) != nil || core == nil || core.hasRegionalPolicy() {
			return mismatch
		}
		return nil
	}
	var root map[string]json.RawMessage
	var original map[string]json.RawMessage
	var rules, servers []map[string]any
	if json.Unmarshal(raw, &root) != nil || json.Unmarshal(root["dns"], &original) != nil || json.Unmarshal(original["rules"], &rules) != nil || json.Unmarshal(original["servers"], &servers) != nil {
		return mismatch
	}
	start := 0
	for start < len(rules) && rules[start]["preferred_by"] != nil {
		start++
	}
	if len(rules) < start+4 || len(servers) == 0 {
		return mismatch
	}
	var stripped map[string]json.RawMessage
	if err := json.Unmarshal(root["dns"], &stripped); err != nil {
		return mismatch
	}
	stripped["rules"], _ = json.Marshal(append(rules[:start:start], rules[start+4:]...))
	stripped["servers"], _ = json.Marshal(servers[:len(servers)-1])
	root["dns"], _ = json.Marshal(stripped)
	names, err := p.names(private)
	if err != nil {
		return err
	}
	if err = scopeDomainDials(root, names, true); err != nil {
		return mismatch
	}
	base, err := json.Marshal(root)
	if err != nil {
		return mismatch
	}
	expected, err := Apply(base, p, private)
	if err != nil {
		return err
	}
	var want, got map[string]any
	if json.Unmarshal(expected, &want) != nil || json.Unmarshal(raw, &got) != nil || !reflect.DeepEqual(want["dns"], got["dns"]) {
		return mismatch
	}
	return nil
}
