package regionaldns

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
)

// RelayFingerprint covers the full outbound, including the peer address, TLS
// identity, credentials and transport. Hash canonical JSON so key order and
// whitespace changes across host and mobile serializers do not affect approval.
func RelayFingerprint(raw []byte, tag string) (string, error) {
	var core struct {
		Outbounds json.RawMessage `json:"outbounds"`
	}
	if err := json.Unmarshal(raw, &core); err != nil {
		return "", errors.New("invalid relay configuration")
	}
	return fingerprintOutbound(core.Outbounds, tag)
}

func fingerprintOutbound(raw []byte, tag string) (string, error) {
	var outbounds []json.RawMessage
	if json.Unmarshal(raw, &outbounds) != nil {
		return "", errors.New("invalid relay configuration")
	}
	var pin string
	for _, out := range outbounds {
		var node map[string]any
		dec := json.NewDecoder(bytes.NewReader(out))
		dec.UseNumber()
		if err := dec.Decode(&node); err != nil {
			return "", errors.New("invalid relay configuration")
		}
		if node["tag"] != tag {
			continue
		}
		if pin != "" {
			return "", errors.New("ambiguous relay node")
		}
		canonical, err := json.Marshal(node)
		if err != nil {
			return "", err
		}
		digest := sha256.Sum256(canonical)
		pin = hex.EncodeToString(digest[:])
	}
	if pin == "" {
		return "", errors.New("approved relay is missing from the saved core configuration")
	}
	return pin, nil
}

// The direct outbound's default_domain_resolver bypasses DNS rules. Resolve
// an incoming FQDN through the DNS rule engine before sniffing changes the
// match hostname. IP destinations are already resolved. The action continues
// with the usual routing policy and propagates DNS errors to the caller.
func scopeDomainDials(root map[string]json.RawMessage, names []string, verify bool) error {
	var route map[string]json.RawMessage
	if err := json.Unmarshal(root["route"], &route); err != nil || route == nil {
		return errors.New("regional DNS requires a route configuration")
	}
	var rules []json.RawMessage
	if raw := route["rules"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &rules); err != nil {
			return errors.New("invalid route rules")
		}
	}
	at := 0
	for at < len(rules) {
		var prefix struct{ Action string }
		if json.Unmarshal(rules[at], &prefix) != nil || prefix.Action != "hijack-dns" {
			break
		}
		at++
	}
	// SOCKS clients can supply the absolute (root-terminated) spelling too.
	dialNames := slices.Clone(names)
	for _, name := range names {
		dialNames = append(dialNames, name+".")
	}
	guard, _ := json.Marshal(map[string]any{"domain": dialNames, "action": "resolve", "disable_cache": true})
	if verify {
		if at == len(rules) {
			return errors.New("regional DNS domain dial rule missing")
		}
		var actual map[string]any
		if err := json.Unmarshal(rules[at], &actual); err != nil {
			return err
		}
		actualJSON, _ := json.Marshal(actual)
		if !bytes.Equal(guard, actualJSON) {
			return errors.New("regional DNS domain dial rule changed")
		}
		rules = slices.Delete(rules, at, at+1)
	} else {
		// Custom resolution or destination rewrites after the guard could force a
		// second lookup through another transport. Decline ambiguous profiles.
		for _, rule := range rules {
			var r struct {
				Action   string
				Override string `json:"override_address"`
			}
			if err := json.Unmarshal(rule, &r); err != nil {
				return err
			}
			if r.Action == "resolve" || r.Override != "" {
				return errors.New("regional DNS conflicts with custom route resolution")
			}
		}
		rules = slices.Insert(rules, at, json.RawMessage(guard))
	}
	route["rules"], _ = json.Marshal(rules)
	root["route"], _ = json.Marshal(route)
	return nil
}
