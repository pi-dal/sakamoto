package mobilecore

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestProxyServerRoutesRepairImportsAndEditsWithoutChangingOtherFields(t *testing.T) {
	body := `{"inbounds":[{"type":"tun","route_exclude_address":"10.0.0.0/8"},{"type":"mixed","listen_port":2334},{"type":"tun","tag":"second"}],"outbounds":[{"type":"trojan","server":"203.0.113.8","server_port":443},{"type":"trojan","server":"2001:db8::8"},{"type":"socks","server":"::ffff:203.0.113.8","detour":"group"},{"type":"trojan","server":"never-resolve.invalid"}],"unknown":{"integer":9007199254740993}}`
	protected, err := EnsureProxyServerRoutesJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Inbounds []struct {
			Excluded []string `json:"route_exclude_address"`
			Type     string   `json:"type"`
		} `json:"inbounds"`
		Unknown json.RawMessage `json:"unknown"`
	}
	if err := json.Unmarshal([]byte(protected), &root); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(root.Inbounds[0].Excluded, []string{"10.0.0.0/8", "203.0.113.8/32", "2001:db8::8/128"}) {
		t.Fatal(root.Inbounds)
	}
	if root.Inbounds[1].Excluded != nil || !reflect.DeepEqual(root.Inbounds[2].Excluded, []string{"203.0.113.8/32", "2001:db8::8/128"}) {
		t.Fatal("wrong inbounds changed", root.Inbounds)
	}
	if string(root.Unknown) != `{"integer":9007199254740993}` {
		t.Fatal("unrelated field changed", string(root.Unknown))
	}
	again, err := EnsureProxyServerRoutesJSON(protected)
	if err != nil || again != protected {
		t.Fatal("repair is not idempotent", err)
	}
}

func TestMobileConnectivityProtectsProxySocketsButKeepsEmbeddedTailnetCaptured(t *testing.T) {
	body := `{"endpoints":[{"type":"tailscale","tag":"tailnet"}],"inbounds":[{"type":"tun"}],"outbounds":[{"type":"trojan","server":"203.0.113.8"},{"type":"socks","server":"100.64.0.8","detour":"tailnet"}]}`
	protected, err := PrepareTailscaleConnectivityJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Inbounds []struct {
			Excluded []string `json:"route_exclude_address"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(protected), &root); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(root.Inbounds[0].Excluded, []string{"203.0.113.8/32"}) {
		t.Fatal("embedded tailnet bypassed", root.Inbounds)
	}
	for _, invalid := range []string{`null`, `{`, `{"outbounds":{}}`, `{"outbounds":[{"server":"203.0.113.8"}],"inbounds":[{"type":"tun","route_exclude_address":42}]}`} {
		if _, err := EnsureProxyServerRoutesJSON(invalid); err == nil {
			t.Fatalf("invalid configuration accepted: %s", invalid)
		}
	}
}
