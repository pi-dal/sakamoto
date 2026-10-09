package mobilecore

import (
	"encoding/json"

	"github.com/pi-dal/sakamoto/pkg/mobileconf"
)

// OnDemandConfJSON returns a bounded host-only preview for iOS VPN triggers.
// A limit error must preserve the existing editable list, never a partial list.
func OnDemandConfJSON(content string) (string, error) {
	summary, err := mobileconf.OnDemandFromConf(content)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(summary)
	return string(raw), err
}
