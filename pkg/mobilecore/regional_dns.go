package mobilecore

import "github.com/pi-dal/sakamoto/pkg/regionaldns"

// CheckRegionalDNSRecovery refuses snapshots with different effective privacy settings.
func CheckRegionalDNSRecovery(current, retained string) error {
	return regionaldns.CheckRecovery([]byte(current), []byte(retained))
}

// RegionalRelayFingerprint captures the selected node during an explicit settings save.
func RegionalRelayFingerprint(content, relay string) (string, error) {
	return regionaldns.RelayFingerprint([]byte(content), relay)
}

// ApplyRegionalDNS compiles device-local DNS settings into a connection candidate.
// The native clients validate this candidate with Libbox before saving or applying.
func ApplyRegionalDNS(content, domains, resolverIP, tlsName, relay, fingerprint string) (string, error) {
	if domains == "" {
		if err := regionaldns.Verify([]byte(content), regionaldns.Profile{}, nil); err != nil {
			return "", err
		}
		return content, nil
	}
	result, err := regionaldns.Apply([]byte(content), regionaldns.Profile{
		Domains: domains, ResolverIP: resolverIP, TLSName: tlsName, Relay: relay, RelayFingerprint: fingerprint,
	}, nil)
	return string(result), err
}
