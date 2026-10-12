package mobilegen

import (
	"encoding/json"
	"net/netip"
	"slices"
	"testing"

	"github.com/pi-dal/sakamoto/pkg/mobilecore"
)

func prefixesContain(prefixes []string, address string) bool {
	ip := netip.MustParseAddr(address)
	for _, text := range prefixes {
		if netip.MustParsePrefix(text).Contains(ip) {
			return true
		}
	}
	return false
}

func TestTailscaleRepairsImportedTunBypassAndRoutes(t *testing.T) {
	// This is the host/mobile generation path used by imported Shadowrocket
	// sources, rather than a hand-built final TUN configuration.
	generated, err := GenerateProfileJSON(request(t, map[string]string{
		"nodes.txt": "trojan://test@example.com:443#node",
		"main.conf": "[General]\nbypass-tun=10.0.0.0/8,100.64.0.0/10,fd7a:115c:a1e0::/48\ntun-included-routes=192.168.0.0/16\n[Rule]\nIP-CIDR,100.64.0.0/10,DIRECT\nFINAL,PROXY",
	}, "main.conf"), "")
	if err != nil {
		t.Fatal(err)
	}
	var result struct{ Config string }
	if err := json.Unmarshal([]byte(generated), &result); err != nil {
		t.Fatal(err)
	}
	var imported map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result.Config), &imported); err != nil {
		t.Fatal(err)
	}
	// Old versions added the endpoint without migrating the host bypass.
	imported["endpoints"] = json.RawMessage(`[{"type":"tailscale","tag":"tailscale-in"}]`)
	encoded, err := json.Marshal(imported)
	if err != nil {
		t.Fatal(err)
	}
	before := string(encoded)
	var profile struct {
		Inbounds []struct {
			Included []string `json:"route_address"`
			Excluded []string `json:"route_exclude_address"`
		}
		Route struct{ Rules []map[string]json.RawMessage }
	}
	if err := json.Unmarshal([]byte(before), &profile); err != nil {
		t.Fatal(err)
	}
	if !prefixesContain(profile.Inbounds[0].Excluded, "100.83.12.34") {
		t.Fatal("fixture did not reproduce the tailnet bypass")
	}
	after, err := mobilecore.PrepareTailscaleConnectivityJSON(before)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(after), &profile); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"100.83.12.34", "fd7a:115c:a1e0::1234"} {
		if prefixesContain(profile.Inbounds[0].Excluded, address) || !prefixesContain(profile.Inbounds[0].Included, address) {
			t.Fatalf("tailnet packet %s still misses the TUN", address)
		}
	}
	if !prefixesContain(profile.Inbounds[0].Excluded, "10.23.45.67") {
		t.Fatal("LAN exclusion was lost")
	}
	// Real core rule matching verifies both warm peer-map routing and a
	// cold/unknown peer, before a private/global/imported DIRECT rule can win.
	assertTailnetCoreRouting(t, after)
	again, err := mobilecore.PrepareTailscaleConnectivityJSON(after)
	if err != nil || again != after {
		t.Fatalf("runtime repair is not idempotent: %v", err)
	}
}

func TestTailscaleSubtractsOnlyItsAddressSpace(t *testing.T) {
	for _, excluded := range []string{
		`["100.64.0.0/10","fd7a:115c:a1e0::/48","10.0.0.0/8"]`,
		`["100.0.0.0/8","fd00::/8","10.0.0.0/8"]`,
		`["0.0.0.0/0","::/0"]`,
		`["100.83.12.34/32","fd7a:115c:a1e0::1234/128","10.0.0.0/8"]`,
		`"100.64.0.0/10"`,
	} {
		t.Run(excluded, func(t *testing.T) {
			content := `{"endpoints":[{"type":"tailscale","tag":"tailnet"}],"inbounds":[{"type":"tun","route_exclude_address":` + excluded + `}],"route":{"rules":[]}}`
			prepared, err := mobilecore.PrepareTailscaleConnectivityJSON(content)
			if err != nil {
				t.Fatal(err)
			}
			var root struct {
				Inbounds []struct {
					Excluded []string `json:"route_exclude_address"`
				}
			}
			if err := json.Unmarshal([]byte(prepared), &root); err != nil {
				t.Fatal(err)
			}
			for _, text := range root.Inbounds[0].Excluded {
				for _, tailnet := range []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fd7a:115c:a1e0::/48")} {
					if netip.MustParsePrefix(text).Overlaps(tailnet) {
						t.Fatalf("still excludes tailnet: %s", text)
					}
				}
			}
			var original []string
			if excluded[0] == '"' {
				var single string
				if err := json.Unmarshal([]byte(excluded), &single); err != nil {
					t.Fatal(err)
				}
				original = []string{single}
			} else if err := json.Unmarshal([]byte(excluded), &original); err != nil {
				t.Fatal(err)
			}
			for _, address := range []string{"100.0.0.1", "100.63.255.255", "100.128.0.0", "100.255.255.255", "10.0.0.1", "fd7a:115c:a1df::1", "fd7a:115c:a1e1::1", "fdff::1", "8.8.8.8", "2001:4860::1"} {
				if prefixesContain(original, address) != prefixesContain(root.Inbounds[0].Excluded, address) {
					t.Fatalf("changed exclusion for unrelated address %s", address)
				}
			}
		})
	}
}

func TestTailscaleRuntimeRepairsLegacyRulePriority(t *testing.T) {
	content := `{"endpoints":[{"type":"tailscale","tag":"tailnet"}],"inbounds":[{"type":"tun","route_address_set":"local"}],"route":{"rules":[{"action":"sniff"},{"protocol":"dns","action":"hijack-dns"},{"ip_is_private":true,"outbound":"direct"},{"clash_mode":"Global","outbound":"proxy"},{"preferred_by":["tailnet"],"action":"route","outbound":"tailnet"}]}}`
	prepared, err := mobilecore.PrepareTailscaleConnectivityJSON(content)
	if err != nil {
		t.Fatal(err)
	}
	assertTailnetCoreRouting(t, prepared)
	var root struct {
		Inbounds []struct {
			Included []string `json:"route_address"`
		}
	}
	if err := json.Unmarshal([]byte(prepared), &root); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(root.Inbounds[0].Included, []string{"100.64.0.0/10", "fd7a:115c:a1e0::/48"}) {
		t.Fatal("rule-set-only TUN did not gain tailnet routes")
	}
}

func TestTailscaleRuntimePreservesOtherProfilesAndInvalidInput(t *testing.T) {
	for _, content := range []string{`{"inbounds":[]}`, `{"endpoints":[{"type":"wireguard","tag":"wg"}]}`} {
		prepared, err := mobilecore.PrepareTailscaleConnectivityJSON(content)
		if err != nil || prepared != content {
			t.Fatalf("changed a non-Tailscale profile: %v", err)
		}
	}
	for _, content := range []string{`null`, `[]`, `{`, `{"endpoints":[{"type":"tailscale"}]}`, `{"endpoints":[{"type":"tailscale","tag":"t"}],"inbounds":[{"type":"tun","route_exclude_address":["invalid"]}]}`} {
		if _, err := mobilecore.PrepareTailscaleConnectivityJSON(content); err == nil {
			t.Fatalf("accepted invalid configuration: %s", content)
		}
	}
}
