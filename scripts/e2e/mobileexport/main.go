// Run by scripts/check-private-exports.py on a synthetic exported profile.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/pi-dal/sakamoto/pkg/mobilecore"
	"github.com/pi-dal/sakamoto/pkg/mobilegen"
	"github.com/pi-dal/sakamoto/pkg/regionaldns"
)

func main() {
	var pkg struct {
		HostMetadataJSON string
		SourceBundleJSON string
		Config           string
		DNSGuard         json.RawMessage
		RegionalDNS      regionaldns.Profile
	}
	data, err := os.ReadFile(os.Args[1])
	must(err)
	must(json.Unmarshal(data, &pkg))
	var metadata struct {
		Experiment struct {
			Mode      string
			Threshold int
		}
	}
	must(json.Unmarshal([]byte(pkg.HostMetadataJSON), &metadata))
	r := pkg.RegionalDNS
	settings, err := json.Marshal(map[string]any{"mode": metadata.Experiment.Mode, "threshold": metadata.Experiment.Threshold, "dnsGuard": pkg.DNSGuard, "regionalDNS": r})
	must(err)
	generated, err := mobilegen.GenerateProfileJSON(pkg.SourceBundleJSON, string(settings))
	must(err)
	var candidate struct{ Config string }
	must(json.Unmarshal([]byte(generated), &candidate))
	var runtime string
	for _, base := range []string{pkg.Config, candidate.Config} {
		runtime, err = mobilecore.ApplyRegionalDNS(base, r.Domains, r.ResolverIP, r.TLSName, r.Relay, r.RelayFingerprint)
		must(err)
		must(regionaldns.Verify([]byte(runtime), r, nil))
	}
	fmt.Print(runtime)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
