package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/pi-dal/sakamoto/pkg/regionaldns"
)

// ApproveRegionalRelay binds an explicit settings edit to the already-generated
// local outbound. Refreshing subscriptions or regenerating never updates a pin.
func (c *Config) ApproveRegionalRelay(previous regionaldns.Profile, sidecar string) error {
	p := &c.RegionalDNS
	if p.Domains == "" {
		p.RelayFingerprint = ""
		return nil
	}
	if previous.Domains != "" && previous.Relay == p.Relay {
		p.RelayFingerprint = previous.RelayFingerprint
		if p.RelayFingerprint == "" {
			return errors.New("clear and save regional domains, then select the reviewed relay again")
		}
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(sidecar), "config.json"))
	if err != nil {
		return errors.New("generate the relay configuration before approving regional DNS")
	}
	p.RelayFingerprint, err = regionaldns.RelayFingerprint(raw, p.Relay)
	return err
}
